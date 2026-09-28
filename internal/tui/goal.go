package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Yash-K-Jagani/ycode/internal/goal"
	"github.com/Yash-K-Jagani/ycode/internal/modes"
	"github.com/Yash-K-Jagani/ycode/internal/tools"
)

// goalTodoBlock renders the workdir task list for the goal prompt: done steps
// stay visible (so the model doesn't redo them) but the open ones come last.
func goalTodoBlock(workdir string) string {
	return goal.TaskBlock(taskList(workdir))
}

// taskList reads the workdir task list into the goal package's own type, so the
// TUI and headless run paths cannot disagree about what the task list means.
func taskList(workdir string) []goal.TaskList {
	items := tools.ReadTodos(workdir)
	out := make([]goal.TaskList, 0, len(items))
	for _, it := range items {
		out = append(out, goal.TaskList{ID: it.ID, Text: it.Text, Done: it.Done})
	}
	return out
}

// openSteps counts unfinished task-list entries. In goal mode this is the
// harness's independent check on a claimed GOAL MET.
func openSteps(workdir string) int {
	return goal.OpenSteps(taskList(workdir))
}

// advanceGoal decides what a finished goal-mode turn does next. It returns a
// tea.Cmd that starts the next autonomous iteration, or nil when the run is
// over (goal met, blocked, budget spent, or the mode was left mid-run).
func (m *Model) advanceGoal(msg doneMsg) tea.Cmd {
	g := m.goal
	if g == nil || msg.mode != modes.Goal {
		return nil
	}
	g.Reconcile(msg.text, goal.Evidence{
		Calls:          msg.calls,
		SucceededCalls: msg.oks,
		FailedCalls:    msg.fails,
		WorkCalls:      msg.work,
		OpenSteps:      openSteps(m.workdir),
	})
	if g.Rejected() {
		// Do not pass a false completion claim off as progress: say it was
		// turned down, then let the run carry on with the work still owed.
		m.appendSys(g.RejectionNote())
	}
	// Next counts this iteration and reports whether the run may continue.
	if !g.Next() {
		m.goalStop()
		return nil
	}
	if m.mode != modes.Goal {
		// The user switched away mid-run: stop rather than keep spending
		// tokens on a mode they are no longer looking at.
		g.Status = goal.Cancelled
		m.appendSys("goal run stopped: left goal mode. " + g.Summary())
		return nil
	}
	m.appendSys(fmt.Sprintf("↳ goal iteration %d/%d — continuing unattended (Esc to stop)", g.Iter+1, g.MaxIter))
	return m.startTurn(g.Text, turnOpts{auto: true, nudge: g.Continuation()})
}

// stopGoalRun marks an interrupted run as cancelled.
func (m *Model) stopGoalRun() {
	if m.goal != nil && m.goal.Status == goal.Active {
		m.goal.Status = goal.Cancelled
	}
}

// goalStop marks a budget-exhausted run and reports what was achieved.
func (m *Model) goalStop() {
	m.noteGoalStop()
}

// noteGoalStop prints the run's outcome, if there is one to print. Guarded so
// a non-goal turn never appends an empty block to the transcript.
func (m *Model) noteGoalStop() {
	if note := m.goalStopNote(); note != "" {
		m.appendSys(note)
	}
}

func (m *Model) goalStopNote() string {
	if m.goal == nil {
		return ""
	}
	return m.goal.StopNote()
}

// setGoal starts (or replaces) the active goal and switches to goal mode.
func (m *Model) setGoal(text string) string {
	if text == "" {
		if m.goal == nil {
			return "No active goal. Usage: /goal <what you want accomplished>"
		}
		var b strings.Builder
		b.WriteString("Goal: " + m.goal.Summary())
		if block := goalTodoBlock(m.workdir); block != "" {
			b.WriteString("\nTask list:\n" + block)
		}
		if m.goal.Running() {
			b.WriteString("\nSend another message to work on it, or /goal <new goal> to replace it.")
		}
		return b.String()
	}
	m.goal = goal.New(text, modes.DefaultGoalIters)
	m.setMode(modes.Goal)
	// A goal owns the task list: stale steps from earlier work would send the
	// run down the wrong path on its first turn.
	cleared := 0
	if items := tools.ReadTodos(m.workdir); len(items) > 0 {
		cleared = len(items)
		if err := tools.ClearTodos(m.workdir); err != nil {
			m.appendSys("goal: could not clear the old task list: " + err.Error())
		}
	}
	m.addToast("goal started: " + m.goal.Text)
	if cleared > 0 {
		m.appendSys(fmt.Sprintf("goal: cleared %d task(s) from the previous goal.", cleared))
	}
	return ""
}
