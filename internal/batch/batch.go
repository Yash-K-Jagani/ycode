package batch

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Yash-K-Jagani/ycode/internal/config"
	"github.com/Yash-K-Jagani/ycode/internal/db"
	"github.com/Yash-K-Jagani/ycode/internal/headless"
	"github.com/Yash-K-Jagani/ycode/internal/modes"
)

type Job struct {
	ID      string    `json:"id"`
	Prompt  string    `json:"prompt"`
	Mode    string    `json:"mode"`
	Agent   string    `json:"agent"`
	Workdir string    `json:"workdir"`
	Created time.Time `json:"created"`
	Status  string    `json:"status"` // queued|done|failed
	Result  string    `json:"result,omitempty"`
}

type Queue struct {
	file string
	conn *sql.DB
	jobs []Job
}

func filePath() string { return filepath.Join(config.Dir(), "batch.json") }

func Load() *Queue {
	q := &Queue{file: filePath(), conn: db.Shared()}
	if q.conn != nil {
		if jobs, err := readTable(q.conn); err == nil {
			if len(jobs) == 0 {
				// one-time import from legacy JSON
				if data, err := os.ReadFile(q.file); err == nil {
					var legacy []Job
					if _ = json.Unmarshal(data, &legacy); len(legacy) > 0 {
						q.jobs = legacy
						_ = q.saveTable()
					}
				}
			} else {
				q.jobs = jobs
			}
			return q
		}
		q.conn = nil
	}
	data, _ := os.ReadFile(q.file)
	_ = json.Unmarshal(data, &q.jobs)
	return q
}

func readTable(conn *sql.DB) ([]Job, error) {
	rows, err := conn.Query(`SELECT data FROM batch_jobs ORDER BY created`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Job
	for rows.Next() {
		var raw string
		var j Job
		if err := rows.Scan(&raw); err != nil {
			continue
		}
		if err := json.Unmarshal([]byte(raw), &j); err != nil {
			continue
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

func (q *Queue) save() error {
	if q.conn != nil {
		if err := q.saveTable(); err == nil {
			return nil
		}
		q.conn = nil
	}
	data, _ := json.MarshalIndent(q.jobs, "", "  ")
	_ = os.MkdirAll(config.Dir(), 0o755)
	return os.WriteFile(q.file, data, 0o644)
}

func (q *Queue) saveTable() error {
	tx, err := q.conn.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DELETE FROM batch_jobs`); err != nil {
		return err
	}
	for _, j := range q.jobs {
		raw, _ := json.Marshal(j)
		if _, err := tx.Exec(`INSERT INTO batch_jobs(id,created,data) VALUES(?,?,?)`,
			j.ID, j.Created.Format(time.RFC3339), string(raw)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (q *Queue) Add(prompt, mode, agent, workdir string) Job {
	j := Job{
		ID:     fmt.Sprintf("%d", time.Now().UnixNano()),
		Prompt: prompt, Mode: mode, Agent: agent, Workdir: workdir,
		Created: time.Now(), Status: "queued",
	}
	q.jobs = append(q.jobs, j)
	_ = q.save()
	return j
}

func (q *Queue) List() []Job { return append([]Job(nil), q.jobs...) }

func (q *Queue) Clear(doneOnly bool) int {
	n := 0
	kept := q.jobs[:0]
	for _, j := range q.jobs {
		if !doneOnly || j.Status == "queued" {
			kept = append(kept, j)
		} else {
			n++
		}
	}
	// doneOnly=false clears everything
	if !doneOnly {
		n = len(q.jobs)
		kept = nil
	}
	q.jobs = kept
	_ = q.save()
	return n
}

// RunPending executes queued jobs headlessly in order.
func (q *Queue) RunPending(ctx context.Context) {
	cfg, _ := config.Load()
	for i, j := range q.jobs {
		if j.Status != "queued" {
			continue
		}
		mode := modes.Build
		if j.Mode != "" {
			if m, err := modes.Parse(j.Mode); err == nil {
				mode = m
			}
		}
		answer, err := headless.Run(ctx, cfg, j.Prompt, headless.Options{Mode: mode, Agent: j.Agent, Workdir: j.Workdir})
		if err != nil {
			q.jobs[i].Status = "failed"
			q.jobs[i].Result = err.Error()
		} else {
			q.jobs[i].Status = "done"
			q.jobs[i].Result = answer
		}
		_ = q.save()
	}
}
