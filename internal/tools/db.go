package tools

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	dbTimeout = 30 * time.Second
	dbRowCap  = 20
	dbCellCap = 300
)

type DBTool struct{}

func (DBTool) Name() string { return "db" }
func (DBTool) Description() string {
	return "Query MongoDB/PostgreSQL/MySQL (DSN per call, never stored). Args: kind (mongo|postgres|mysql), dsn, action (tables|schema|query), collection/table, filter (mongo JSON) or query (SQL), limit (default 20), write (bool, needed for non-SELECT). Reads allowed in plan mode."
}
func (DBTool) Schema() string {
	return `{"type":"object","required":["kind","dsn","action"],"properties":{"kind":{"type":"string"},"dsn":{"type":"string"},"action":{"type":"string"},"collection":{"type":"string"},"table":{"type":"string"},"filter":{"type":"string"},"query":{"type":"string"},"limit":{"type":"integer"},"write":{"type":"boolean"}}}`
}

type dbArgs struct {
	Kind       string
	Action     string
	Collection string
	Table      string
	Filter     string
	Query      string
	Limit      int
	Write      bool
}

func (DBTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var raw struct {
		Kind       string `json:"kind"`
		DSN        string `json:"dsn"`
		Action     string `json:"action"`
		Collection string `json:"collection"`
		Table      string `json:"table"`
		Filter     string `json:"filter"`
		Query      string `json:"query"`
		Limit      int    `json:"limit"`
		Write      bool   `json:"write"`
	}
	if err := decodeArgs(args, &raw); err != nil {
		return "", err
	}
	if strings.TrimSpace(raw.DSN) == "" {
		return "", fmt.Errorf("dsn required (ask the user for it — never invent one)")
	}
	a := dbArgs{Kind: raw.Kind, Action: raw.Action, Collection: raw.Collection, Table: raw.Table, Filter: raw.Filter, Query: raw.Query, Limit: raw.Limit, Write: raw.Write}
	if a.Limit <= 0 || a.Limit > 100 {
		a.Limit = dbRowCap
	}
	if IsReadOnly(ctx) && a.Action == "query" && !isReadQuery(a.Query) {
		return "", fmt.Errorf("db writes blocked in read-only mode")
	}
	ctx, cancel := context.WithTimeout(ctx, dbTimeout)
	defer cancel()
	switch strings.ToLower(a.Kind) {
	case "mongo", "mongodb":
		return mongoRun(ctx, raw.DSN, a)
	case "postgres", "postgresql", "pg":
		return pgRun(ctx, raw.DSN, a)
	case "mysql", "mariadb":
		return mysqlRun(ctx, raw.DSN, a)
	default:
		return "", fmt.Errorf("unknown kind %q (mongo|postgres|mysql)", a.Kind)
	}
}

func mongoRun(ctx context.Context, dsn string, a dbArgs) (string, error) {
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(dsn))
	if err != nil {
		return "", fmt.Errorf("mongo connect: %w", err)
	}
	defer func() { _ = client.Disconnect(ctx) }()
	dbs, err := client.ListDatabaseNames(ctx, bson.D{})
	if err != nil {
		return "", fmt.Errorf("mongo: %w", err)
	}
	target := ""
	for _, d := range dbs {
		if d != "admin" && d != "local" && d != "config" {
			target = d
			break
		}
	}
	if target == "" {
		return "", fmt.Errorf("no user databases found")
	}
	dbh := client.Database(target)
	switch a.Action {
	case "tables":
		names, err := dbh.ListCollectionNames(ctx, bson.D{})
		if err != nil {
			return "", err
		}
		if len(names) == 0 {
			return "(no collections)", nil
		}
		return "db " + target + ":\n- " + strings.Join(names, "\n- "), nil
	case "schema":
		if a.Collection == "" {
			return "", fmt.Errorf("collection required for schema")
		}
		var doc bson.M
		if err := dbh.Collection(a.Collection).FindOne(ctx, bson.D{}).Decode(&doc); err != nil {
			return "", fmt.Errorf("sample: %w", err)
		}
		var keys []string
		for k := range doc {
			keys = append(keys, k)
		}
		return a.Collection + " fields: " + strings.Join(keys, ", "), nil
	case "query":
		if a.Collection == "" {
			return "", fmt.Errorf("collection required for query")
		}
		filterDoc := bson.D{}
		if strings.TrimSpace(a.Filter) != "" {
			if err := bson.UnmarshalExtJSON([]byte(a.Filter), true, &filterDoc); err != nil {
				return "", fmt.Errorf("bad filter JSON: %w", err)
			}
		}
		cur, err := dbh.Collection(a.Collection).Find(ctx, filterDoc)
		if err != nil {
			return "", err
		}
		defer func() { _ = cur.Close(ctx) }()
		var out strings.Builder
		n := 0
		for cur.Next(ctx) && n < a.Limit {
			var doc bson.M
			if err := cur.Decode(&doc); err != nil {
				break
			}
			b, _ := json.Marshal(doc)
			out.WriteString(truncate(string(b), dbCellCap*4) + "\n")
			n++
		}
		if n == 0 {
			return "(no rows)", nil
		}
		return out.String(), nil
	default:
		return "", fmt.Errorf("mongo supports tables|schema|query (reads only)")
	}
}

func pgRun(ctx context.Context, dsn string, a dbArgs) (string, error) {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return "", fmt.Errorf("postgres connect: %w", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	switch a.Action {
	case "tables":
		rows, err := conn.Query(ctx, `SELECT tablename FROM pg_tables WHERE schemaname='public' ORDER BY 1 LIMIT $1`, a.Limit)
		if err != nil {
			return "", err
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var t string
			if err := rows.Scan(&t); err == nil {
				out = append(out, t)
			}
		}
		if len(out) == 0 {
			return "(no tables)", nil
		}
		return strings.Join(out, "\n"), nil
	case "schema":
		if a.Table == "" || !safeIdent(a.Table) {
			return "", fmt.Errorf("table required for schema")
		}
		rows, err := conn.Query(ctx, `SELECT column_name, data_type FROM information_schema.columns WHERE table_name=$1 ORDER BY ordinal_position`, a.Table)
		if err != nil {
			return "", err
		}
		defer rows.Close()
		var b strings.Builder
		for rows.Next() {
			var c, t string
			_ = rows.Scan(&c, &t)
			b.WriteString(c + " " + t + "\n")
		}
		if b.Len() == 0 {
			return "(no such table)", nil
		}
		return b.String(), nil
	case "query":
		if strings.TrimSpace(a.Query) == "" {
			return "", fmt.Errorf("query required")
		}
		if !a.Write && !isReadQuery(a.Query) {
			return "", fmt.Errorf("refusing non-SELECT without write:true")
		}
		rows, err := conn.Query(ctx, a.Query)
		if err != nil {
			return "", err
		}
		defer rows.Close()
		return pgRows(rows, a.Limit)
	default:
		return "", fmt.Errorf("unknown action %q", a.Action)
	}
}

func pgRows(rows pgx.Rows, limit int) (string, error) {
	fields := rows.FieldDescriptions()
	var b strings.Builder
	names := make([]string, len(fields))
	for i, f := range fields {
		names[i] = f.Name
	}
	b.WriteString(strings.Join(names, " | ") + "\n")
	n := 0
	for rows.Next() && n < limit {
		vals, err := rows.Values()
		if err != nil {
			break
		}
		cells := make([]string, len(vals))
		for i, v := range vals {
			cells[i] = truncate(fmt.Sprintf("%v", v), dbCellCap)
		}
		b.WriteString(strings.Join(cells, " | ") + "\n")
		n++
	}
	return b.String(), rows.Err()
}

func mysqlRun(ctx context.Context, dsn string, a dbArgs) (string, error) {
	conn, err := sql.Open("mysql", dsn)
	if err != nil {
		return "", err
	}
	defer func() { _ = conn.Close() }()
	if err := conn.PingContext(ctx); err != nil {
		return "", fmt.Errorf("connect: %w", err)
	}
	switch a.Action {
	case "tables":
		rows, err := conn.QueryContext(ctx, "SHOW TABLES")
		if err != nil {
			return "", err
		}
		defer func() { _ = rows.Close() }()
		var out []string
		for rows.Next() {
			var t string
			if err := rows.Scan(&t); err == nil {
				out = append(out, t)
			}
			if len(out) >= a.Limit {
				break
			}
		}
		if len(out) == 0 {
			return "(no tables)", nil
		}
		return strings.Join(out, "\n"), nil
	case "schema":
		if a.Table == "" || !safeIdent(a.Table) {
			return "", fmt.Errorf("table required for schema")
		}
		rows, err := conn.QueryContext(ctx, "DESCRIBE `"+a.Table+"`")
		if err != nil {
			return "", err
		}
		defer func() { _ = rows.Close() }()
		return scanCells(rows, a.Limit)
	case "query":
		if strings.TrimSpace(a.Query) == "" {
			return "", fmt.Errorf("query required")
		}
		if !a.Write && !isReadQuery(a.Query) {
			return "", fmt.Errorf("refusing non-SELECT without write:true")
		}
		rows, err := conn.QueryContext(ctx, a.Query)
		if err != nil {
			return "", err
		}
		defer func() { _ = rows.Close() }()
		return scanCells(rows, a.Limit)
	default:
		return "", fmt.Errorf("unknown action %q", a.Action)
	}
}

func scanCells(rows *sql.Rows, limit int) (string, error) {
	cols, _ := rows.Columns()
	var b strings.Builder
	b.WriteString(strings.Join(cols, " | ") + "\n")
	n := 0
	for rows.Next() && n < limit {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		_ = rows.Scan(ptrs...)
		cells := make([]string, len(vals))
		for i, v := range vals {
			if bv, ok := v.([]byte); ok {
				cells[i] = truncate(string(bv), dbCellCap)
			} else {
				cells[i] = truncate(fmt.Sprintf("%v", v), dbCellCap)
			}
		}
		b.WriteString(strings.Join(cells, " | ") + "\n")
		n++
	}
	return b.String(), rows.Err()
}

func isReadQuery(q string) bool {
	first := strings.ToUpper(strings.TrimSpace(q))
	for _, p := range []string{"SELECT", "WITH", "EXPLAIN", "SHOW", "DESCRIBE", "DESC"} {
		if strings.HasPrefix(first, p+" ") || strings.HasPrefix(first, p+"(") || first == p {
			return true
		}
	}
	return false
}

func safeIdent(s string) bool {
	if s == "" || strings.ContainsAny(s, " ;'\"`()") {
		return false
	}
	return true
}
