package sessions

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Yash-K-Jagani/ycode/pkg/apitypes"
)

type SQLStore struct {
	db *sql.DB
}

func (s *SQLStore) Backend() string { return "sqlite" }

func (s *SQLStore) Save(sess *Session) error {
	sess.UpdatedAt = time.Now()
	msgs, err := json.Marshal(sess.Messages)
	if err != nil {
		return err
	}
	sess.MessageCount = len(sess.Messages)
	_, err = s.db.Exec(`INSERT INTO sessions(id,title,created,updated,provider,model,msgs,messages)
		VALUES(?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET title=excluded.title, updated=excluded.updated,
		provider=excluded.provider, model=excluded.model, msgs=excluded.msgs, messages=excluded.messages`,
		sess.ID, sess.Title, sess.CreatedAt.Format(time.RFC3339), sess.UpdatedAt.Format(time.RFC3339),
		sess.Provider, sess.Model, sess.MessageCount, string(msgs))
	return err
}

func (s *SQLStore) List() ([]Session, error) {
	rows, err := s.db.Query(`SELECT id,title,created,updated,provider,model,msgs,messages FROM sessions ORDER BY updated DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Session
	for rows.Next() {
		sess, err := scanSession(rows)
		if err != nil {
			continue
		}
		out = append(out, sess)
	}
	return out, rows.Err()
}

// ListMeta returns the same list as List without the message blobs, so
// filtering and pruning do not read every transcript in the database.
//
// The messages column is left out of the SELECT rather than selected and
// discarded: for a long session it is most of the row, and pulling it into
// memory only to throw it away is the cost being avoided. The stored count
// stands in for it, so a caller that displayed "N msgs" still can.
func (s *SQLStore) ListMeta() ([]Session, error) {
	rows, err := s.db.Query(`SELECT id,title,created,updated,provider,model,msgs FROM sessions ORDER BY updated DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Session
	for rows.Next() {
		var sess Session
		var created, updated string
		if err := rows.Scan(&sess.ID, &sess.Title, &created, &updated,
			&sess.Provider, &sess.Model, &sess.MessageCount); err != nil {
			continue
		}
		sess.CreatedAt, _ = time.Parse(time.RFC3339, created)
		sess.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
		sess.Messages = []apitypes.Message{}
		out = append(out, sess)
	}
	return out, rows.Err()
}

func (s *SQLStore) Load(id string) (*Session, error) {
	row := s.db.QueryRow(`SELECT id,title,created,updated,provider,model,msgs,messages FROM sessions WHERE id=?`, id)
	sess, err := scanSession(row)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("session not found: %s", id)
	}
	return &sess, err
}

func (s *SQLStore) Delete(id string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE id=?`, id)
	return err
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanSession(r rowScanner) (Session, error) {
	var s Session
	var created, updated, msgs string
	if err := r.Scan(&s.ID, &s.Title, &created, &updated, &s.Provider, &s.Model, &s.MessageCount, &msgs); err != nil {
		return Session{}, err
	}
	s.CreatedAt, _ = time.Parse(time.RFC3339, created)
	s.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
	_ = json.Unmarshal([]byte(msgs), &s.Messages)
	if s.Messages == nil {
		s.Messages = []apitypes.Message{}
	}
	// A row written before the count existed reports zero; the messages are
	// right here, so the real number is available for free.
	if s.MessageCount == 0 {
		s.MessageCount = len(s.Messages)
	}
	return s, nil
}

// ImportJSON copies JSON-file sessions missing from SQLite. Idempotent.
func (s *SQLStore) ImportJSON() (int, error) {
	existing := map[string]bool{}
	rows, err := s.db.Query(`SELECT id FROM sessions`)
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	_ = rows.Close()
	_ = rows.Err()
	for _, id := range ids {
		existing[id] = true
	}
	jsons, err := jsonStore{}.all()
	if err != nil {
		return 0, nil // no JSON dir yet — nothing to import
	}
	n := 0
	for i := range jsons {
		if existing[jsons[i].ID] {
			continue
		}
		if err := s.Save(&jsons[i]); err != nil {
			continue
		}
		n++
	}
	return n, nil
}
