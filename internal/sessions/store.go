package sessions

import (
	"database/sql"
	"os"

	"github.com/Yash-K-Jagani/ycode/internal/config"
	"github.com/Yash-K-Jagani/ycode/internal/db"
)

// Store abstracts session persistence (JSON files or SQLite).
type Store interface {
	Save(*Session) error
	List() ([]Session, error)
	Load(id string) (*Session, error)
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
	conn, err := db.Open(config.Dir() + "/ycode.db")
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
