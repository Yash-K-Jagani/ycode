package gemini

import (
	"github.com/Yash-K-Jagani/ycode/internal/providers/openaicompat"
)

func New(apiKey string) *openaicompat.Client {
	return openaicompat.New("gemini", "https://generativelanguage.googleapis.com/v1beta/openai", apiKey)
}

var DefaultModels = []string{"gemini-2.5-flash", "gemini-2.5-pro"}
