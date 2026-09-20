package registry

import (
	"github.com/Yash-K-Jagani/ycode/internal/providers/gemini"
	"github.com/Yash-K-Jagani/ycode/internal/providers/groq"
	"github.com/Yash-K-Jagani/ycode/internal/providers/openrouter"
	"github.com/Yash-K-Jagani/ycode/pkg/apitypes"
)

func Catalog() []apitypes.ModelInfo {
	var out []apitypes.ModelInfo
	for _, m := range gemini.DefaultModels {
		out = append(out, apitypes.ModelInfo{ID: m, Provider: "gemini", Status: "cloud"})
	}
	for _, m := range openrouter.DefaultModels {
		out = append(out, apitypes.ModelInfo{ID: m, Provider: "openrouter", Status: "cloud"})
	}
	for _, m := range groq.DefaultModels {
		out = append(out, apitypes.ModelInfo{ID: m, Provider: "groq", Status: "cloud"})
	}
	return out
}
