package sessions

import (
	"database/sql"
	"os"
	"path/filepath"

	"github.com/Yash-K-Jagani/ycode/internal/config"
	"github.com/Yash-K-Jagani/ycode/internal/db"
)

// Store abstracts session persistence (JSON files or SQLite).
type Store interface {
	Save(*Session) error
	List() ([]Session, error)
	// ListMeta returns sessions without their messages, for the callers that
	// only need identity: the search filter, the prune list, anything that
	// counts or matches on a title.
	//
	// It exists because List() has to load the message blob, and a session's
	// messages are the bulk of its row - a long session is tens of kilobytes of
	// JSON. Matching a filter over a few hundred sessions therefore read the
	// entire conversation history to show a list of titles, and grew with the
	// length of the sessions rather than the number of them. The picker itself
	// still needs List, because selecting a session hands the whole transcript
	// to renderAll.
	ListMeta() ([]Session, error)
	Load(id string) (*Session, error)
	Delete(id string) error
	Backend() string
}

var backend Store = jsonStore{}

// InitDefault opens SQLite (with one-time JSON import) unless
// YCODE_SESSIONS=json forces the legacy backend. Never fails hard:
// on any error it stays on JSON.
func InitDefault() string {
	if os.Getenv("YCODE_SESSIONS") == "json" {
		backend = jsonStore{}
		return "json"
	}
	conn, err := db.Open(filepath.Join(config.Dir(), "ycode.db"))
	if err != nil {
		backend = jsonStore{}
		return "json"
	}
	sqlStore := &SQLStore{db: conn}
	if _, err := sqlStore.ImportJSON(); err != nil {
		_ = conn.Close()
		backend = jsonStore{}
		return "json"
	}
	backend = sqlStore
	return "sqlite"
}

// UseSQLite pins the backend (tests).
func UseSQLite(conn *sql.DB) { backend = &SQLStore{db: conn} }

// UseJSON pins the legacy backend (tests).
func UseJSON() { backend = jsonStore{} }

// Backend reports the active store ("json"|"sqlite").
func Backend() string { return backend.Backend() }
