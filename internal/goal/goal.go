// Package goal holds the in-memory state of a goal-mode run: the goal the
// user asked for, how many autonomous iterations it has used, and whether the
// agent declared it met or blocked.
//
// The model owns the "is it done?" decision, expressed with the verdict
// markers below, so the harness can stop the loop deterministically. Nothing
// here is persisted: a goal lives for the length of the session.
package goal

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Yash-K-Jagani/ycode/internal/textutil"
	"time"
)

type Status string

const (
	// Active means the run continues: the model neither confirmed nor
	// gave up yet.
	Active Status = "active"
	// Met means every acceptance criterion was satisfied and verified.
	Met Status = "met"
	// Blocked means the agent cannot proceed without user input.
	Blocked Status = "blocked"
	// Spent means the iteration budget ran out with the goal still open.
	Spent Status = "budget_exhausted"
	// Stalled means a turn ran with no tool calls and no verdict: the model
	// described the work instead of doing it. Retrying reproduces the same
	// turn, so the run stops and says so.
	Stalled Status = "stalled"
	// Cancelled means the user interrupted the run.
	Cancelled Status = "cancelled"
)

// Markers the model emits to end a run. GOAL_STATUS is the machine-readable
// form small models emit more reliably than a bare marker on its own line.
// There is deliberately no "continue" marker: a run continues by simply not
// producing a verdict, and offering an explicit end-of-turn token only taught
// the model to narrate instead of acting.
const (
	MarkerMet     = "GOAL MET"
	MarkerBlocked = "GOAL BLOCKED"
	statusPrefix  = "GOAL_STATUS:"
)

// Goal is one autonomous objective and its progress.
type Goal struct {
	Text      string
	Iter      int
	MaxIter   int
	Status    Status
	StartedAt time.Time
	// rejected records that the last GOAL MET claim was not credible, and why.
	// A rejected claim does not end the run — the work is still owed — but it
	// must not pass silently either.
	rejected bool
	reason   string
	// workDone and openSteps track what the run actually changed, so a run
	// that ends without a verdict is reported honestly. A model can finish
	// the work and then stop calling tools: that is not "nothing was done".
	workDone  int
	openSteps int
}

// New starts a goal with an iteration budget. maxIter <= 0 falls back to 12.
func New(text string, maxIter int) *Goal {
	if maxIter <= 0 {
		maxIter = 12
	}
	return &Goal{
		Text:      strings.TrimSpace(text),
		MaxIter:   maxIter,
		Status:    Active,
		StartedAt: time.Now(),
	}
}

// Running reports whether the harness should start another turn.
func (g *Goal) Running() bool {
	return g != nil && g.Status == Active && g.Iter < g.MaxIter
}

// Exceeded reports whether the iteration budget is used up.
func (g *Goal) Exceeded() bool {
	return g != nil && g.Iter >= g.MaxIter
}

// Next counts the iteration that just finished and reports whether another
// turn may start. Iter is always counted, so a run that ends on its last turn
// still reports how many turns it took. The status becomes Spent once the
// budget is used up.
func (g *Goal) Next() bool {
	if g == nil {
		return false
	}
	g.Iter++
	if g.Status != Active {
		return false
	}
	if g.Iter >= g.MaxIter {
		g.Status = Spent
		return false
	}
	return true
}

// Verdict reads the model's closing markers. Blocked wins over Met when both
// appear, since an agent that gives up has not finished the goal. An answer
// with neither marker leaves the status untouched (the run continues).
func (g *Goal) Verdict(answer string) Status {
	if g == nil {
		return Active
	}
	up := strings.ToUpper(answer)
	blocked := strings.Contains(up, MarkerBlocked)
	met := strings.Contains(up, MarkerMet)
	if v, ok := parseStatus(up); ok {
		switch v {
		case "MET":
			met, blocked = true, false
		case "BLOCKED":
			blocked, met = true, false
		}
	}
	switch {
	case blocked:
		g.Status = Blocked
	case met:
		g.Status = Met
	}
	return g.Status
}

// parseStatus reads an explicit GOAL_STATUS: met|blocked line.
func parseStatus(up string) (string, bool) {
	i := strings.Index(up, statusPrefix)
	if i < 0 {
		return "", false
	}
	rest := strings.TrimSpace(up[i+len(statusPrefix):])
	if k := strings.IndexAny(rest, " \t\r\n,.;:"); k >= 0 {
		rest = rest[:k]
	}
	switch strings.ToUpper(rest) {
	case "MET", "COMPLETE", "COMPLETED", "DONE", "ACHIEVED", "SUCCESS":
		return "MET", true
	case "BLOCKED", "BLOCK", "STUCK", "FAIL", "FAILED", "IMPOSSIBLE":
		return "BLOCKED", true
	}
	return "", false
}

// workTools are the tools whose success means something was actually done to
// the project. Bookkeeping (todo, memory, summary) and observation (read,
// grep, glob, tree, changes, security) succeed just as easily while the goal
// is completely untouched, so they are not evidence of anything.
//
// This distinction is not theoretical: a 3b model created a two-item task
// list, marked both items done without touching a file, and announced
// "GOAL MET". Counting its todo calls as progress accepted that.
var workTools = map[string]bool{
	"write": true, "create": true, "add": true, "edit": true, "remove": true,
	"delete": true, "patch": true, "run": true, "testgen": true, "bash": true,
	"git": true, "github": true, "db": true, "api": true, "notebook": true,
	"scaffold": true, "vscode": true, "models": true,
}

// IsWorkTool reports whether a tool mutates the project (or runs something).
func IsWorkTool(name string) bool { return workTools[strings.ToLower(name)] }

// TaskList is the subset of a todo entry the goal loop needs, so this package
// does not import the tools package (which would cycle: tools imports agent).
type TaskList struct {
	ID   int
	Text string
	Done bool
}

// OpenSteps counts unfinished steps in the goal's task list. In goal mode this
// is the harness's independent check on a claimed GOAL MET.
func OpenSteps(items []TaskList) int {
	n := 0
	for _, it := range items {
		if !it.Done {
			n++
		}
	}
	return n
}

// TaskBlock renders the task list for the prompt: completed steps stay visible
// so the model does not redo them, and the open ones come last.
func TaskBlock(items []TaskList) string {
	if len(items) == 0 {
		return ""
	}
	var open, done []string
	for _, it := range items {
		mark := " "
		if it.Done {
			mark = "x"
		}
		line := "[" + mark + "] " + strconv.Itoa(it.ID) + ". " + it.Text
		if it.Done {
			done = append(done, line)
		} else {
			open = append(open, line)
		}
	}
	return strings.Join(append(done, open...), "\n")
}

// Evidence is what the harness itself observed about a turn. It exists so a
// model's GOAL MET claim can be checked against something other than the
// model's own word.
type Evidence struct {
	// Calls is how many tool calls the turn parsed.
	Calls int
	// SucceededCalls is how many of them actually completed without error.
	SucceededCalls int
	// FailedCalls is how many errored. A turn that hit an error has not
	// verified its work, so it may not claim the goal is finished.
	FailedCalls int
	// WorkCalls is how many *mutating* calls succeeded — the only calls that
	// count as progress toward a goal. A model that only planned, listed,
	// read and ticked its own boxes has done nothing.
	WorkCalls int
	// OpenSteps is how many todo steps the goal's task list still has open.
	// In goal mode the task list *is* the acceptance criteria, so open steps
	// mean the goal is not finished.
	OpenSteps int
}

// Reconcile reads the model's closing markers, then checks them against what
// the harness observed. A verdict is a claim; the evidence decides.
//
// A met claim that the evidence does not support does not end the run: the
// status stays active and the rejection is recorded, because the work is still
// owed and the model usually has budget left to do it. Only a verified met
// claim, an explicit GOAL BLOCKED, a stall, or an exhausted budget ends it.
//
// Three failure modes this exists to stop, all observed with small local
// models: narrating a turn without calling anything, announcing "all criteria
// satisfied" while the files on disk are untouched, and ticking off a task
// list without touching a file.
func (g *Goal) Reconcile(answer string, ev Evidence) Status {
	if g == nil {
		return Active
	}
	g.Verdict(answer)
	g.rejected, g.reason = false, ""
	g.workDone += ev.WorkCalls
	g.openSteps = ev.OpenSteps
	switch g.Status {
	case Active:
		// No verdict and no tool call: the turn only talked about the work.
		if ev.Calls == 0 {
			g.Status = Stalled
		}
	case Met:
		if !g.Credible(ev) {
			g.rejected, g.reason = true, unmetReason(ev)
			g.Status = Active
		}
	}
	return g.Status
}

// Rejected reports whether the last GOAL MET claim was turned down.
func (g *Goal) Rejected() bool { return g != nil && g.rejected }

// RejectionNote explains the last rejected claim, or "" when there was none.
func (g *Goal) RejectionNote() string {
	if !g.Rejected() {
		return ""
	}
	return fmt.Sprintf("⊘ GOAL MET rejected: %s — the goal is not met, continuing", g.reason)
}

// unmetReason describes why a met claim is not credible.
func unmetReason(ev Evidence) string {
	var why []string
	switch {
	case ev.Calls == 0:
		why = append(why, "no tool was called at all")
	case ev.WorkCalls == 0:
		why = append(why, "nothing was changed or run (only planning/reading steps completed)")
	}
	if ev.FailedCalls > 0 {
		why = append(why, fmt.Sprintf("%d tool call(s) failed in the turn that claimed it", ev.FailedCalls))
	}
	if ev.OpenSteps > 0 {
		why = append(why, fmt.Sprintf("%d task-list step(s) still open", ev.OpenSteps))
	}
	return strings.Join(why, " and ")
}

// Credible reports whether a claimed GOAL MET is backed by observed work: at
// least one mutating call actually succeeded, nothing errored on the way, and
// the goal's own task list is closed. Anything less and the run continues —
// erring towards more work is the safe direction when a model is grading its
// own homework.
func (g *Goal) Credible(ev Evidence) bool {
	return ev.WorkCalls > 0 && ev.FailedCalls == 0 && ev.OpenSteps == 0
}

// Stop reports whether the run is over for any reason.
func (g *Goal) Stop() bool { return g != nil && g.Status != Active }

// Summary is the one-line status shown in the sidebar and /status.
func (g *Goal) Summary() string {
	if g == nil {
		return "no goal"
	}
	return fmt.Sprintf("%s · iter %d/%d · %s", oneLine(g.Text), g.Iter, g.MaxIter, g.Status)
}

// workState describes what the run actually changed. A model can complete the
// work and then stop calling tools, so a run that ends without a verdict must
// not claim "nothing was done" when files changed.
func (g *Goal) workState() string {
	switch {
	case g.workDone == 0:
		return "nothing was changed"
	case g.openSteps == 0:
		return fmt.Sprintf("%d change(s) were made and every task-list step is closed, so the goal may already be done — review the diff before rerunning", g.workDone)
	default:
		return fmt.Sprintf("%d change(s) were made, but %d task-list step(s) were still open", g.workDone, g.openSteps)
	}
}

// StopNote explains a finished run to the user, or "" while it is still going.
func (g *Goal) StopNote() string {
	if g == nil || g.Status == Active {
		return ""
	}
	note := func(format string, a ...any) string {
		s := fmt.Sprintf(format, a...)
		if g.rejected {
			s += "\n  last GOAL MET claim was rejected: " + g.reason
		}
		return s
	}
	switch g.Status {
	case Met:
		return note("✓ goal met after %d iteration(s): %s", g.Iter, oneLine(g.Text))
	case Blocked:
		return note("⊘ goal blocked after %d iteration(s): %s", g.Iter, oneLine(g.Text))
	case Spent:
		if g.openSteps == 0 && g.workDone > 0 {
			return note("…goal budget spent (%d iterations). %s: %s", g.Iter, g.workState(), oneLine(g.Text))
		}
		return note("…goal budget spent (%d iterations) with the goal still open: %s", g.Iter, oneLine(g.Text))
	case Stalled:
		if g.workDone > 0 {
			return note("⊘ goal run stopped after %d iteration(s): the model stopped calling tools. %s: %s", g.Iter, g.workState(), oneLine(g.Text))
		}
		return note("⊘ goal run stopped after %d iteration(s): the model described the work instead of calling tools, so nothing was done: %s", g.Iter, oneLine(g.Text))
	case Cancelled:
		return note("⊘ goal run cancelled after %d iteration(s): %s", g.Iter, oneLine(g.Text))
	}
	return ""
}

// PromptBlock is the ACTIVE GOAL section injected into the system prompt of
// every goal-mode turn. todos is the current task list rendered as text (may
// be empty); pendingPlan is an approved plan to carry over, if any.
func (g *Goal) PromptBlock(todos, pendingPlan string) string {
	if g == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\nACTIVE GOAL (iteration %d of %d — you are running unattended, nobody will reply to questions):\n- %s",
		g.Iter+1, g.MaxIter, oneLine(g.Text))
	if pendingPlan != "" {
		b.WriteString("\nAPPROVED PLAN for this goal (implement it step by step, in order):\n" + pendingPlan)
	}
	b.WriteString("\nYOUR TASK LIST (todo list, live — this is the goal's progress):")
	if strings.TrimSpace(todos) == "" {
		b.WriteString("\n(empty — create the steps with todo add now, then immediately do the first one with tools)")
	} else {
		b.WriteString("\n" + todos)
	}
	b.WriteString("\nBUDGET: " + budgetLine(g))
	return b.String()
}

// Continuation is the nudge that starts the next autonomous turn. It is
// injected as a system message so the transcript keeps only real user turns.
func (g *Goal) Continuation() string {
	return fmt.Sprintf("Keep working the active goal (iteration %d of %d). No user is watching — do not ask anything. "+
		"Re-read your task list, take the first unfinished step, DO it with a tagged tool call, verify it, close it, then take the next step in this same turn. "+
		"Emitting a <tool:NAME>{...}</tool:NAME> is the only way to make progress. Do not imitate a tool result: "+
		"lines like 'wrote <path> (N bytes)', 'added #N' or a ```diff block are fabrications unless you are reproducing one the harness actually sent you, "+
		"and typing one skips the work entirely. "+
		"Do not restate the goal, do not describe what you are about to do, and do not end the turn until you either finish every criterion "+
		"(end with GOAL MET and evidence) or cannot continue (end with GOAL BLOCKED: <reason>). A turn with no tool calls is a wasted turn.",
		g.Iter+1, g.MaxIter)
}

func budgetLine(g *Goal) string {
	if g.MaxIter <= 1 {
		return "this is your only turn — do the whole goal with tools, then report."
	}
	return "if you stop without a verdict the harness simply starts the next iteration, so use every one to make real progress."
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	return textutil.Truncate(s, 120)
}
