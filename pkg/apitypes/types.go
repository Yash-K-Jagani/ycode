package apitypes

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type Message struct {
	Role    Role   `json:"role"`
	Content string `json:"content"`
}

type StreamChunk struct {
	Delta     string `json:"delta"`
	Done      bool   `json:"done"`
	PromptTok int    `json:"prompt_tokens,omitempty"`
	ComplTok  int    `json:"completion_tokens,omitempty"`
}

type ModelInfo struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	Size     string `json:"size,omitempty"`
	Quant    string `json:"quant,omitempty"`
	Status   string `json:"status,omitempty"`
}
