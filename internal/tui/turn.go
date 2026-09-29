package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Yash-K-Jagani/ycode/internal/agent"
	"github.com/Yash-K-Jagani/ycode/internal/audit"
	yctx "github.com/Yash-K-Jagani/ycode/internal/context"
	"github.com/Yash-K-Jagani/ycode/internal/goal"
	"github.com/Yash-K-Jagani/ycode/internal/hooks"
	"github.com/Yash-K-Jagani/ycode/internal/modes"
	"github.com/Yash-K-Jagani/ycode/internal/rag"
	"github.com/Yash-K-Jagani/ycode/internal/textutil"
	"github.com/Yash-K-Jagani/ycode/internal/tools"
	"github.com/Yash-K-Jagani/ycode/pkg/apitypes"
	tea "github.com/charmbracelet/bubbletea"
)

func (m *Model) submit() tea.Cmd {
	text := strings.TrimSpace(m.ta.Value())
	m.ta.Reset()
	if text == "" {
		return nil
	}
	if strings.HasPrefix(text, "/") || strings.HasPrefix(text, ":") {
		if strings.HasPrefix(text, ":") {
			text = "/" + text[1:]
		}
		name := strings.Fields(text)[0]
		args := strings.TrimSpace(strings.TrimPrefix(text, name))
		h, ok := slashRegistry()[name]
		if !ok {
			m.appendSys("unknown command: " + name + " (try /help)")
			return nil
		}
		var out string
		var cmd tea.Cmd
		func() {
			defer func() {
				if r := recover(); r != nil {
					if e, ok := r.(error); ok && e == ErrQuit {
						if m.prog != nil {
							m.prog.Quit()
						}
					}
				}
			}()
			out, cmd = h(context.Background(), m, args)
		}()
		if out != "" {
			m.appendSys(out)
		}
		return cmd
	}
	return m.startTurn(text, turnOpts{})
}

// startTurn appends the user message, builds the system prompt and runs the
// provider turn (streaming with or without the tool loop).
func (m *Model) startTurn(text string, o turnOpts) tea.Cmd {
	// The hard stop. A turn that would begin over the daily budget does not
	// begin, and says why with the number attached.
	//
	// It is checked here rather than after the model call because the cost of a
	// turn is not known until it has been paid for, and a check afterwards is
	// a report rather than a limit. plan.md called for hard stops; a soft
	// warning that then spends anyway is the worst of both.
	if m.budgetExceeded() {
		return nil
	}
	if !o.auto {
		m.sess.Messages = append(m.sess.Messages, apitypes.Message{Role: apitypes.RoleUser, Content: text})
	}
	expanded, missing := expandAttachments(m.workdir, text)
	for _, miss := range missing {
		m.appendSys("attachment skipped: " + miss)
	}
	if !o.auto {
		m.appendMsg(m.formatMsg(apitypes.RoleUser, text))
	}
	m.busy = true
	m.busySince = time.Now()
	m.stream.Reset()
	m.sr.reset()
	spinCmd := m.sp.Tick
	hist := append([]apitypes.Message(nil), m.sess.Messages...)
	userText := text
	if !o.auto {
		if expanded != text {
			userText = expanded
			hist[len(hist)-1].Content = expanded
		}
	} else if o.nudge != "" {
		hist = append(hist, apitypes.Message{Role: apitypes.RoleSystem, Content: o.nudge})
	}
	prog := m.prog
	mode := m.mode
	ag := m.agent
	reg := m.toolreg
	workdir := m.workdir
	tracker := m.tracker
	hookset := m.hookset
	skill := m.pendingSkill
	m.pendingSkill = ""
	mcpNames := append([]string(nil), m.mcpNames...)
	rounds := modes.Rounds(mode)
	// Snapshot the goal block on the UI thread: the run advances between
	// turns, so building it inside the turn goroutine would race it.
	goalBlock := ""
	if mode == modes.Goal && m.goal != nil {
		goalBlock = m.goal.PromptBlock(goalTodoBlock(m.workdir), m.pendingPlan)
	}
	hookset.Fire(context.Background(), hooks.OnRequest, map[string]string{"mode": string(mode), "workdir": workdir})
	turnCtx, turnCancel := context.WithCancel(context.Background())
	m.turnCancel = turnCancel
	m.cancelled = false
	turnStart := time.Now()
	turn := func() tea.Msg {
		ctx := turnCtx
		// Read-only is a property of the mode, not a hardcoded exception for
		// plan; a new read-only mode gets it for free.
		if modes.IsReadOnly(mode) {
			ctx = tools.WithReadOnly(ctx)
		}
		zdl := m.cfg.ZeroDataLeak
		if zdl {
			ctx = tools.WithZeroLeak(ctx)
		}
		toolCalls := 0
		toolOK := 0
		toolFail := 0
		workOK := 0
		var acted []fileAct
		written := 0
		toolBytes := 0
		// Coalesce the model's output: a fast local model can produce dozens of
		// writes a second, each of which would otherwise be a bubbletea message
		// and a full transcript re-render. One update per 40ms is far more than
		// a person can read, and it bounds the work per second however chatty
		// the transport is. Flush is called before the turn completes, and the
		// completed message is re-rendered in full anyway, so nothing is lost.
		w := newCoalescingWriter(func(s string) {
			if prog != nil {
				prog.Send(deltaMsg(s))
			}
		}, coalesceInterval)
		countWritten := func(n int) { written += n }
		w.onWrite = countWritten
		// Flush on every exit path. A deferred call runs after the returned
		// message is built but before the command's result reaches the program,
		// so the final delta is queued ahead of the completion message and the
		// transcript never briefly shows a truncated answer.
		defer w.Flush()
		onTool := func(name, args, result string, err error) {
			toolCalls++
			if err == nil {
				toolOK++
				if goal.IsWorkTool(name) {
					workOK++
				}
				switch name {
				case "write", "create", "add", "edit", "remove", "delete":
					if p := writeOpPath(args); p != "" {
						acted = append(acted, fileAct{op: name, path: p})
					}
				}
			} else {
				toolFail++
			}
			toolBytes += len(result)
			status := fmt.Sprintf("ok (%d bytes)", len(result))
			if err != nil {
				status = "error: " + err.Error()
			}
			audit.Log("tool", map[string]any{"tool": name, "args": truncateArgs(args), "status": status})
			if prog == nil {
				return
			}
			switch name {
			case "write", "edit", "create", "add", "remove", "read":
				prog.Send(fileOpMsg{op: name, path: writeOpPath(args), detail: status, diff: extractDiff(result), ok: err == nil})
				return
			}
			prog.Send(toolMsg(fmt.Sprintf("🔧 %s %s → %s", name, truncateArgs(args), status)))
		}
		p, model, err := m.router.Active()
		if err != nil {
			return errMsg{err}
		}
		// Retrieve RAG first: it feeds the system prompt, so it has to happen
		// before the prompt is assembled.
		ragNote, ragContext := "", ""
		if !m.ragOff {
			if idx, ok := rag.Load(workdir); ok {
				if qv, err := m.embedder.Embed(ctx, []string{userText}); err == nil && len(qv) > 0 {
					chunks, qerr := rag.Query(idx, qv[0], 4)
					if qerr != nil {
						// The index was built with a different embedding
						// model. Silently skipping it would leave the user
						// wondering why RAG stopped finding anything, so the
						// turn says so once.
						ragNote = "RAG skipped: " + qerr.Error()
					} else if len(chunks) > 0 {
						ragContext = rag.FormatContext(chunks)
						ragNote = fmt.Sprintf("RAG %d chunks", len(chunks))
					}
				} else if err != nil {
					m.ragOff = true
					if prog != nil {
						prog.Send(sysMsg("RAG disabled this session (embed failed: " + err.Error() + ")"))
					}
				}
			}
		}

		// The plan carries over only when the user actually asked for it.
		planForPrompt := m.pendingPlan
		carryPlan := mode == modes.Build && planForPrompt != "" && isBuildIt(userText)
		if carryPlan {
			m.pendingPlan = ""
			if prog != nil {
				prog.Send(sysMsg("Building the approved plan…"))
			}
		}
		sys := agent.BuildSystem(agent.SystemOptions{
			Mode:        mode,
			Registry:    reg,
			Workdir:     workdir,
			Model:       model,
			Agent:       ag.Name,
			Skill:       skill,
			GoalBlock:   goalBlock,
			PendingPlan: planForPrompt,
			CarryPlan:   carryPlan,
			RagContext:  ragContext,
			ZeroLeak:    zdl,
		})

		budget := yctx.BudgetFor(model)
		trimmed, dropped := yctx.Trim(hist, budget-1500)
		if dropped > 0 && prog != nil {
			prog.Send(sysMsg(fmt.Sprintf("…compacted %d older messages to fit context", dropped)))
		}
		msgs := append([]apitypes.Message{{Role: apitypes.RoleSystem, Content: sys}}, trimmed...)
		promptTok := yctx.Estimate(msgs)
		allowed := modes.AllowedTools(mode, append(mcpNames, m.pluginNames...)...)
		if zdl {
			allowed = filterNetworkTools(allowed)
		}
		notes := []string{fmt.Sprintf("~%d tokens", promptTok)}
		var answer string
		turnCalls := 0
		if len(allowed) == 0 {
			if mode == modes.Chat && fileTaskRe.MatchString(userText) && prog != nil {
				prog.Send(sysMsg("Tip: I have no file tools in chat mode — hit Tab or /build so I can read/edit files."))
			}
			if hit, ok := m.semCache.Lookup(ctx, userText, m.cfg.ActiveProvider, model); ok {
				est := yctx.Estimate([]apitypes.Message{{Role: apitypes.RoleUser, Content: userText}})
				return doneMsg{text: hit, note: "⚡ semantic cache hit (no model call)", ctx: est, ctxB: yctx.BudgetFor(model), mode: mode}
			}
			full, fbNote, err := m.router.StreamWithFallback(ctx, msgs, w)
			if err != nil {
				return errMsg{err}
			}
			answer = full
			m.semCache.Store(ctx, userText, full, m.cfg.ActiveProvider, model)
			if fbNote != "" && prog != nil {
				prog.Send(sysMsg(fbNote))
			}
		} else {
			chain := []agent.Candidate{{Provider: p, Model: model}}
			for _, fb := range m.router.Fallbacks() {
				if fp, err := m.router.Provider(fb.Provider); err == nil {
					chain = append(chain, agent.Candidate{
						Provider: fp, Model: fb.Model, Label: fb.Provider + "/" + fb.Model,
					})
				}
			}
			// One chain implementation, shared with the headless paths, so a
			// fallback means the same thing in a TUI turn and a CI run.
			res := agent.RunChain(ctx, chain, msgs, reg, allowed, hookset, w, onTool, rounds, string(mode),
				func(label string) {
					if prog != nil {
						prog.Send(resetStreamMsg{})
						prog.Send(sysMsg("↳ retrying turn on fallback " + label))
					}
				})
			turnCalls += res.Calls
			if res.Text == "" {
				return errMsg{res.Failure()}
			}
			answer = res.Text
			if res.Err != nil {
				answer += "\n\n(stopped early: " + res.Err.Error() + ")"
			}
			if res.FellBack() {
				notes = append(notes, "fell back to "+res.Used)
			}
			{
				// Deterministic conversion: bare shell command as the whole
				// answer (rm/touch) becomes the real tool call — no inference.
				if toolOK == 0 {
					if conv := shellToCalls(answer, userText); len(conv) > 0 {
						if prog != nil {
							prog.Send(sysMsg("↳ ran it as a tool instead…"))
						}
						results := agent.Exec(ctx, reg, allowed, hookset, conv, onTool)
						turnCalls += len(conv)
						okAll := true
						var parts []string
						for _, r := range results {
							if strings.HasPrefix(r, "ERROR:") {
								okAll = false
							}
							parts = append(parts, r)
						}
						if okAll {
							answer = strings.Join(parts, "\n")
							notes = append(notes, "executed as "+conv[0].Name)
						}
					}
				}
				// Self-correction: the model dodged acting (pasted content or
				// roleplayed a result tag instead of emitting the call).
				// One bounded retry round; the original answer stands unless
				// the retry actually calls tools (never swap visible content
				// for an equally empty answer).
				retryNudge := ""
				switch {
				case toolOK == 0 && looksLikeWriteTask(userText) && hasCodeFence(answer):
					retryNudge = "You pasted file content as text instead of using the write/edit tool. Redo this turn properly: emit ONLY a tool call shaped exactly like <tool:write>{\"path\": \"FILE\", \"content\": \"...\"}</tool:write> — no pasted content, no prose."
				case toolOK == 0 && fileTaskRe.MatchString(userText) && hasResultRoleplay(answer):
					retryNudge = "You wrote a <tool_result> without ever emitting the matching <tool:> call — nothing executed. Redo this turn properly: emit ONLY the <tool:> call(s); results come back to you, never write them yourself."
				case toolOK == 0 && looksLikeDeleteTask(userText):
					retryNudge = "You did not call any tool. Redo this turn properly: emit ONLY a call shaped exactly like <tool:delete>{\"path\": \"FILE\"}</tool:delete> (add \"recursive\": true for directories) — no prose claims."
				case toolOK == 0 && fileTaskRe.MatchString(userText) && claimsCompletion(answer):
					retryNudge = "You claimed completion without calling any tool — nothing executed. Redo this turn properly: emit ONLY the <tool:> call(s) that do the work, no claims, no prose."
				case toolOK == 0 && fileTaskRe.MatchString(userText) && delegatesToUser(answer):
					retryNudge = "You told the user to do the work themselves instead of acting. Redo this turn properly: YOU do it — emit ONLY the <tool:> call(s), no instructions to the user."
				}
				if retryNudge != "" {
					if prog != nil {
						prog.Send(sysMsg("↳ dodged acting — retrying with tools…"))
					}
					callsBefore := turnCalls
					retryMsgs := append(append([]apitypes.Message(nil), msgs...),
						apitypes.Message{Role: apitypes.RoleAssistant, Content: answer},
						apitypes.Message{Role: apitypes.RoleSystem, Content: retryNudge})
					res2, err2 := agent.RunWithRounds(ctx, res.Winner.Provider, res.Winner.Model, retryMsgs, reg, allowed, hookset, w, onTool, rounds, string(mode))
					turnCalls += res2.Calls
					if turnCalls > callsBefore && (err2 == nil || res2.Text != "") {
						answer = res2.Text
						notes = append(notes, "self-corrected to tools")
					}
				}
				if toolOK == 0 && fileTaskRe.MatchString(userText) {
					notes = append(notes, noSuccessNote())
				}
				// Outcome verification: check acted file ops against disk.
				// One bounded verify-retry with concrete facts, then report
				// honestly either way.
				if fails := verifyOps(workdir, acted, turnStart); len(fails) > 0 {
					if prog != nil {
						prog.Send(sysMsg("↳ verifying on disk… mismatch, one more try…"))
					}
					vCallsBefore := turnCalls
					vRFMsgs := append(append([]apitypes.Message(nil), msgs...),
						apitypes.Message{Role: apitypes.RoleAssistant, Content: answer},
						apitypes.Message{Role: apitypes.RoleSystem, Content: verifyFact(fails)})
					res3, err3 := agent.RunWithRounds(ctx, res.Winner.Provider, res.Winner.Model, vRFMsgs, reg, allowed, hookset, w, onTool, rounds, string(mode))
					turnCalls += res3.Calls
					if turnCalls > vCallsBefore && (err3 == nil || res3.Text != "") {
						answer = res3.Text
						notes = append(notes, "retried after verification")
					}
					if fails := verifyOps(workdir, acted, turnStart); len(fails) > 0 {
						notes = append(notes, "unverified: "+strings.Join(fails, "; "))
					} else {
						notes = append(notes, fmt.Sprintf("verified: %d file op(s)", len(acted)))
					}
				} else if len(acted) > 0 {
					notes = append(notes, fmt.Sprintf("verified: %d file op(s)", len(acted)))
				}
			}
		}
		complTok := written/4 + toolBytes/4
		turnUSD := tracker.Add(m.cfg.ActiveProvider, promptTok, complTok)
		_, _, usd := tracker.Today()
		notes = append(notes, fmt.Sprintf("$%.4f today", usd))
		if ragNote != "" {
			notes = append(notes, ragNote)
		}
		audit.Log("turn", map[string]any{"mode": string(mode), "provider": m.cfg.ActiveProvider, "model": model, "prompt": userText, "answer": answer})
		return doneMsg{text: answer, note: "↳ " + strings.Join(notes, " · "), ptok: promptTok, ctok: complTok, usd: turnUSD, ctx: promptTok, ctxB: budget, calls: turnCalls, oks: toolOK, fails: toolFail, work: workOK, agent: len(allowed) > 0, mode: mode}
	}
	return tea.Batch(spinCmd, turn)
}

// allProvidersFailed reports every attempt, because "all providers failed"
// names no provider, no model and no reason — the one moment the user most
// needs to know whether to fix a key, switch model, or check the network.
func allProvidersFailed(attempts []string) error {
	if len(attempts) == 0 {
		return fmt.Errorf("no provider was available for this turn — check /status and /connect")
	}
	msg := "every provider failed:\n" + strings.Join(attempts, "\n")
	if len(attempts) > 1 {
		msg += "\n(fallbacks were tried; see /status for latency and failures)"
	}
	return errors.New(msg)
}

func truncateArgs(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	return textutil.Truncate(s, 120)
}
