package cost

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Yash-K-Jagani/ycode/internal/config"
	"github.com/Yash-K-Jagani/ycode/internal/db"
	"github.com/Yash-K-Jagani/ycode/internal/providers"
)

// Free reports providers with no metered spend and no caps: local compute
// is unlimited by design — the tracker records volumes only.
func Free(provider string) bool { return providers.Free(provider) }

type dayEntry struct {
	PromptTok int     `json:"prompt_tokens"`
	ComplTok  int     `json:"completion_tokens"`
	USD       float64 `json:"usd"`
}

type Tracker struct {
	mu   sync.Mutex
	file string
	conn *sql.DB
	days map[string]*dayEntry
}

// NewEphemeral returns a Tracker that keeps day totals in memory only.
//
// It exists for callers that must not read or write the recorded spend - tests
// chiefly, since New seeds itself from the shared database and a test asserting
// a fraction of a budget would otherwise be measuring whatever the developer
// spent this morning. Production code should use New.
func NewEphemeral() *Tracker {
	return &Tracker{file: filepath.Join(os.TempDir(), "ycode-cost-ephemeral.json"), days: map[string]*dayEntry{}}
}

func New() *Tracker {
	t := &Tracker{file: filepath.Join(config.Dir(), "cost.json"), days: map[string]*dayEntry{}}
	t.conn = db.Shared()
	if t.conn != nil {
		if raw, ok := db.KVGet(t.conn, "cost/days"); ok {
			_ = json.Unmarshal([]byte(raw), &t.days)
		} else if data, err := os.ReadFile(t.file); err == nil {
			// one-time import from legacy JSON
			_ = json.Unmarshal(data, &t.days)
			t.persist()
		}
		if t.days == nil {
			t.days = map[string]*dayEntry{}
		}
		return t
	}
	data, _ := os.ReadFile(t.file)
	_ = json.Unmarshal(data, &t.days)
	if t.days == nil {
		t.days = map[string]*dayEntry{}
	}
	return t
}

func (t *Tracker) Add(provider string, promptTok, complTok int) float64 {
	promptUSD, complUSD := providers.Pricing(provider)
	usd := float64(promptTok)/1000*promptUSD + float64(complTok)/1000*complUSD
	t.mu.Lock()
	defer t.mu.Unlock()
	d := time.Now().Format("2006-01-02")
	e := t.days[d]
	if e == nil {
		e = &dayEntry{}
		t.days[d] = e
	}
	e.PromptTok += promptTok
	e.ComplTok += complTok
	e.USD += usd
	t.persist()
	return usd
}

func (t *Tracker) persist() {
	data, _ := json.Marshal(t.days)
	if t.conn != nil {
		if err := db.KVSet(t.conn, "cost/days", string(data)); err == nil {
			return
		}
	}
	_ = os.MkdirAll(config.Dir(), 0o755)
	_ = os.WriteFile(t.file, data, 0o644)
}

func (t *Tracker) Today() (prompt, compl int, usd float64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e := t.days[time.Now().Format("2006-01-02")]
	if e == nil {
		return 0, 0, 0
	}
	return e.PromptTok, e.ComplTok, e.USD
}

// Budget.
//
// plan.md has promised "budget limits with hard stops" since the project
// started. The tracker recorded spend and the sidebar displayed it, and nothing
// ever acted on it - which for a tool that drives an agent loop unattended is
// the difference between a limit and a number to look at.
//
// A hard stop is deliberately the only behaviour. A soft limit that warns and
// then spends anyway is the worst of both: the user has been told they are
// under control when they are not. So a turn that would begin over budget does
// not begin, and a goal run that crosses the line stops at the next iteration
// boundary rather than mid-token.
//
// A budget of zero means unlimited, which is the default and the only sensible
// value for a local provider.

// Budget is a daily spend ceiling over a Tracker.
type Budget struct {
	tracker *Tracker
	// limit is in USD. Zero or negative means no limit.
	limit float64
}

// NewBudget pairs a tracker with a daily limit in USD. A limit of zero or less
// means unlimited.
func NewBudget(t *Tracker, limitUSD float64) *Budget {
	return &Budget{tracker: t, limit: limitUSD}
}

// Unlimited reports whether there is no ceiling in force.
func (b *Budget) Unlimited() bool { return b == nil || b.limit <= 0 }

// Limit is the configured daily ceiling, or zero when unlimited.
func (b *Budget) Limit() float64 {
	if b == nil {
		return 0
	}
	return b.limit
}

// Spent is today's recorded cost in USD.
func (b *Budget) Spent() float64 {
	if b == nil {
		return 0
	}
	_, _, usd := b.tracker.Today()
	return usd
}

// Remaining is how much of the day's budget is left, or -1 when unlimited.
func (b *Budget) Remaining() float64 {
	if b.Unlimited() {
		return -1
	}
	r := b.limit - b.Spent()
	if r < 0 {
		return 0
	}
	return r
}

// Exceeded reports whether today's spend has reached the limit.
//
// >= rather than >, so setting a budget of exactly what you intend to spend
// stops the next turn rather than allowing one more that takes you past it.
func (b *Budget) Exceeded() bool {
	if b.Unlimited() {
		return false
	}
	return b.Spent() >= b.limit
}

// Fraction is how much of the budget is used, for a meter. Zero when unlimited.
func (b *Budget) Fraction() float64 {
	if b.Unlimited() || b.limit <= 0 {
		return 0
	}
	f := b.Spent() / b.limit
	if f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	return f
}

// BlockedMessage is what the user sees when a turn is refused. It names the
// number, because "over budget" with no figure is not actionable.
func (b *Budget) BlockedMessage() string {
	if b.Unlimited() {
		return ""
	}
	return fmt.Sprintf(
		"daily budget reached: $%.4f spent of $%.4f. "+
			"Raise it with `ycode config set daily_budget_usd <n>`, set it to 0 for no limit, "+
			"or switch to a local provider.", b.Spent(), b.limit)
}

// WillExceedAfter reports whether spending one more estimated turn would cross
// the line, so a turn can be allowed but its result flagged.
//
// The estimate is deliberately crude - it cannot know the answer length - and
// that is stated rather than dressed up. A warning that is sometimes wrong is
// still worth having; a stop that fires on an estimate would be worse than no
// stop at all, which is why the hard stop uses recorded spend only.
func (b *Budget) WillExceedAfter(usd float64) bool {
	if b.Unlimited() {
		return false
	}
	return b.Spent()+usd > b.limit
}
