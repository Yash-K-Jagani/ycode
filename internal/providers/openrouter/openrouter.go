package openrouter

import (
	"github.com/Yash-K-Jagani/ycode/internal/providers/openaicompat"
)

func New(apiKey string) *openaicompat.Client {
	c := openaicompat.New("openrouter", "https://openrouter.ai/api/v1", apiKey)
	c.ExtraHeaders = map[string]string{"HTTP-Referer": "https://github.com/Yash-K-Jagani/ycode", "X-Title": "ycode"}
	return c
}

var DefaultModels = []string{"meta-llama/llama-3.3-70b-instruct:free", "google/gemma-3-27b-it:free", "qwen/qwen3-32b:free"}
