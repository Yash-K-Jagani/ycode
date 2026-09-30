package router

import "github.com/Yash-K-Jagani/ycode/pkg/apitypes"

// chunkUsage pulls the usage out of a final stream chunk.
//
// Returns a zero Usage with Reported false when the provider sent nothing, which
// is what happens with Ollama's older endpoints and with several OpenAI-compatible
// servers behind a proxy. Callers fall back to an estimate and say so; a zero
// presented as a measurement would read as "this turn was free".
func chunkUsage(c apitypes.StreamChunk) Usage {
	return Usage{
		PromptTok: c.PromptTok,
		ComplTok:  c.ComplTok,
		Reported:  c.UsageReported(),
	}
}

// Usage is provider-reported token usage for one turn.
//
// The distinction between this and an estimate is the whole point of the type,
// and it is why it is not two bare ints. A provider that sends usage has
// counted the tokens it actually processed; an estimate guesses at four
// characters per token. Presenting the second as the first is how a cost figure
// becomes wrong by 30% without anyone noticing.
type Usage struct {
	// Provider and Model are which provider actually served the turn. On a
	// fallback this is not the active one, and pricing depends on it, so a
	// usage count without it cannot be costed.
	Provider string
	Model    string

	PromptTok int
	ComplTok  int

	// Reported is true when the provider sent real counts.
	//
	// False means every number in this struct is zero because nothing was
	// reported, not because the turn was free. Callers that fall back to an
	// estimate must say so rather than reporting a confident zero.
	Reported bool

	// Calls is how many provider round trips contributed. A turn with a tool
	// loop makes several, so the counts are a sum, not a single request.
	Calls int
}

// Add folds one stream chunk's usage in.
//
// A chunk that reports nothing is counted as a call but contributes no tokens,
// and Reported stays false for the turn unless some chunk actually said
// something. Mixing estimated and reported tokens in one total is how a number
// stops meaning anything, so the flag is all-or-nothing.
func (u *Usage) Add(provider, model string, chunkUsage Usage) {
	if u.Provider == "" {
		u.Provider, u.Model = provider, model
	}
	u.Calls++
	if !chunkUsage.Reported {
		return
	}
	if !u.Reported {
		// First reported chunk resets rather than adds, so tokens counted
		// before any provider reported anything do not leak into a total that
		// claims to be measured.
		u.PromptTok, u.ComplTok = 0, 0
		u.Provider, u.Model = provider, model
		u.Reported = true
	}
	u.PromptTok += chunkUsage.PromptTok
	u.ComplTok += chunkUsage.ComplTok
}

// Total is prompt plus completion, for the places that only need one number.
func (u Usage) Total() int { return u.PromptTok + u.ComplTok }

// Begin clears the ledger so the next turn starts from zero.
//
// The Router is created once for the whole session, so without this a turn's
// usage would accumulate into the next one's and the second turn's cost would
// be roughly double the first's.
func (r *Router) Begin() {
	r.usageMu.Lock()
	defer r.usageMu.Unlock()
	r.usage = Usage{}
}

// Usage returns what has been recorded since the last Begin, and resets it.
//
// Taking rather than getting is deliberate: the caller wants this turn's usage,
// not the session's, and a getter would invite reading it twice.
func (r *Router) TakeUsage() Usage {
	r.usageMu.Lock()
	defer r.usageMu.Unlock()
	u := r.usage
	r.usage = Usage{}
	return u
}

// record folds one chunk in. Internal: providers are called through here.
func (r *Router) record(provider, model string, c Usage) {
	r.usageMu.Lock()
	defer r.usageMu.Unlock()
	r.usage.Add(provider, model, c)
}
