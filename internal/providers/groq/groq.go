package groq

import (
	"github.com/Yash-K-Jagani/ycode/internal/providers/openaicompat"
)

func New(apiKey string) *openaicompat.Client {
	return openaicompat.New("groq", "https://api.groq.com/openai/v1", apiKey)
}

var DefaultModels = []string{"llama-3.3-70b-versatile", "llama-3.1-8b-instant"}
