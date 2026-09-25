package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// fileAct records a file operation the model successfully called.
type fileAct struct {
	op   string // write|create|add|edit|remove|delete
	path string // as given in tool args (may be relative)
}

// verifyOps checks acted file ops against disk. Returns failure descriptions.
// write/create/add/edit: file must exist and be newer than since (2s grace).
// remove/delete: path must be gone.
func verifyOps(workdir string, acts []fileAct, since time.Time) []string {
	var fails []string
	for _, a := range acts {
		p := a.path
		if !filepath.IsAbs(p) {
			p = filepath.Join(workdir, filepath.FromSlash(p))
		}
		fi, err := os.Stat(p)
		switch a.op {
		case "remove", "delete":
			if err == nil {
				fails = append(fails, fmt.Sprintf("%s still exists", a.path))
			}
		default:
			if err != nil {
				fails = append(fails, fmt.Sprintf("%s missing: %v", a.path, err))
			} else if fi.ModTime().Before(since.Add(-2 * time.Second)) {
				fails = append(fails, fmt.Sprintf("%s untouched (mtime %s)", a.path, fi.ModTime().Format(time.TimeOnly)))
			}
		}
	}
	return fails
}

// verifyFact builds the concrete message for a verify-retry round.
func verifyFact(fails []string) string {
	return "Verification failed — disk disagrees with your claims:\n- " +
		joinLines(fails) +
		"\nRedo this turn properly: emit ONLY the <tool:> call(s) that make it true."
}

func joinLines(ss []string) string {
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += "\n- "
		}
		out += s
	}
	return out
}
