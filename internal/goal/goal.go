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
	"strings"
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
	// UnearnedMet means the model claimed GOAL MET without the work to back
	// it up: no successful tool call, or its own task list still open.
	// Small models emit the marker whether or not anything happened, so the
	// harness checks the claim instead of believing it.
	UnearnedMet Status = "unearned_met"
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
	// unmet explains a rejected GOAL MET (why the claim was not credible).
	unmet string
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
// Two failure modes this exists to stop, both observed with small local
// models: narrating a turn without calling anything, and announcing "all
// criteria satisfied" while the files on disk are untouched.
func (g *Goal) Reconcile(answer string, ev Evidence) Status {
	if g == nil {
		return Active
	}
	g.Verdict(answer)
	switch g.Status {
	case Active:
		// No verdict and no tool call: the turn only talked about the work.
		if ev.Calls == 0 {
			g.Status = Stalled
		}
	case Met:
		// A goal run that changed nothing cannot have finished anything, and
		// a task list with open steps is the goal's own unfinished work.
		if !g.Credible(ev) {
			var why []string
			if ev.WorkCalls == 0 {
				if ev.Calls == 0 {
					why = append(why, "no tool was called at all")
				} else {
					why = append(why, "nothing was changed or run (only planning/reading steps completed)")
				}
			}
			if ev.FailedCalls > 0 {
				why = append(why, fmt.Sprintf("%d tool call(s) failed in the turn that claimed it", ev.FailedCalls))
			}
			if ev.OpenSteps > 0 {
				why = append(why, fmt.Sprintf("%d task-list step(s) still open", ev.OpenSteps))
			}
			g.unmet = strings.Join(why, " and ")
			g.Status = UnearnedMet
		}
	}
	return g.Status
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

// StopNote explains a finished run to the user, or "" while it is still going.
func (g *Goal) StopNote() string {
	if g == nil || g.Status == Active {
		return ""
	}
	switch g.Status {
	case Met:
		return fmt.Sprintf("✓ goal met after %d iteration(s): %s", g.Iter, oneLine(g.Text))
	case Blocked:
		return fmt.Sprintf("⊘ goal blocked after %d iteration(s): %s", g.Iter, oneLine(g.Text))
	case Spent:
		return fmt.Sprintf("…goal budget spent (%d iterations) with the goal still open: %s", g.Iter, oneLine(g.Text))
	case Stalled:
		return fmt.Sprintf("⊘ goal run stopped after %d iteration(s): the model described the work instead of calling tools, so nothing was done: %s", g.Iter, oneLine(g.Text))
	case UnearnedMet:
		why := g.unmet
		if why == "" {
			why = "the work does not back the claim"
		}
		return fmt.Sprintf("⊘ goal NOT met after %d iteration(s): the model claimed GOAL MET but %s: %s", g.Iter, why, oneLine(g.Text))
	case Cancelled:
		return fmt.Sprintf("⊘ goal run cancelled after %d iteration(s): %s", g.Iter, oneLine(g.Text))
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
	if len(s) > 120 {
		return s[:117] + "…"
	}
	return s
}
