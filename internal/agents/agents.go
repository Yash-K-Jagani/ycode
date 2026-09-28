package agents

import "strings"

type Agent struct {
	Name        string
	Description string
	Prompt      string
}

func All() []Agent {
	return []Agent{
		{Name: "builder", Description: "Default: implements changes, runs tests", Prompt: "Act as a senior engineer. Make minimal, correct edits with safety in mind."},
		{Name: "planner", Description: "Analyzes and plans, avoids writes", Prompt: "Act as a software architect. Explore first, then propose a precise plan with file paths and risks."},
		{Name: "reviewer", Description: "Reviews diffs, finds bugs", Prompt: "Act as a strict code reviewer. List bugs, risks and concrete fixes with file:line references."},
	}
}

// Get looks an agent up by name, tolerating case and surrounding whitespace:
// /agent offers a picker, but the HTTP API takes whatever string the caller
// sends.
func Get(name string) (Agent, bool) {
	name = strings.TrimSpace(name)
	for _, a := range All() {
		if strings.EqualFold(a.Name, name) {
			return a, true
		}
	}
	return Agent{}, false
}
