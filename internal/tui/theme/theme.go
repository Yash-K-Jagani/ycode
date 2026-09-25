package theme

import "github.com/charmbracelet/lipgloss"

type Theme struct {
	Accent   lipgloss.Color
	User     lipgloss.AdaptiveColor
	AIBorder lipgloss.Color
	Dim      lipgloss.AdaptiveColor
	CodeBG   lipgloss.AdaptiveColor
	CodeFG   lipgloss.AdaptiveColor
	ChipBG   lipgloss.AdaptiveColor
	ChipFG   lipgloss.AdaptiveColor
	SysBG    lipgloss.AdaptiveColor
	FileBG   lipgloss.AdaptiveColor
	FileFG   lipgloss.AdaptiveColor
	CmdBG    lipgloss.AdaptiveColor
	CmdFG    lipgloss.AdaptiveColor
	DelBG    lipgloss.AdaptiveColor
	DelFG    lipgloss.AdaptiveColor
	AddBG    lipgloss.AdaptiveColor
	AddFG    lipgloss.AdaptiveColor
	GutFG    lipgloss.AdaptiveColor
	Name     string
}

func Dark() Theme {
	return Theme{
		Accent:   lipgloss.Color("#7C5CFF"),
		User:     lipgloss.AdaptiveColor{Light: "#1a1a2e", Dark: "#1a1a2e"},
		AIBorder: lipgloss.Color("#2de1a7"),
		Dim:      lipgloss.AdaptiveColor{Light: "#666666", Dark: "#888888"},
		CodeBG:   lipgloss.AdaptiveColor{Light: "#E9E9F2", Dark: "#26263A"},
		CodeFG:   lipgloss.AdaptiveColor{Light: "#1A1A2E", Dark: "#E6E6FA"},
		ChipBG:   lipgloss.AdaptiveColor{Light: "#DFDFF2", Dark: "#2E2E4A"},
		ChipFG:   lipgloss.AdaptiveColor{Light: "#333355", Dark: "#CFCFEA"},
		SysBG:    lipgloss.AdaptiveColor{Light: "#EFEFEF", Dark: "#1B1B28"},
		FileBG:   lipgloss.AdaptiveColor{Light: "#E4DFF2", Dark: "#2C2340"},
		FileFG:   lipgloss.AdaptiveColor{Light: "#2A1F4D", Dark: "#D9CBFF"},
		CmdBG:    lipgloss.AdaptiveColor{Light: "#F3EAD6", Dark: "#38300F"},
		CmdFG:    lipgloss.AdaptiveColor{Light: "#5C4A1F", Dark: "#E8C86A"},
		DelBG:    lipgloss.AdaptiveColor{Light: "#F9E2E2", Dark: "#3D1A1A"},
		DelFG:    lipgloss.AdaptiveColor{Light: "#8A1F1F", Dark: "#FF7B72"},
		AddBG:    lipgloss.AdaptiveColor{Light: "#E1F3E1", Dark: "#1A3320"},
		AddFG:    lipgloss.AdaptiveColor{Light: "#1F6B2E", Dark: "#56D364"},
		GutFG:    lipgloss.AdaptiveColor{Light: "#999999", Dark: "#666666"},
		Name:     "dark",
	}
}

func Light() Theme {
	return Theme{
		Accent:   lipgloss.Color("#5B3CC4"),
		User:     lipgloss.AdaptiveColor{Light: "#EDE7FF", Dark: "#EDE7FF"},
		AIBorder: lipgloss.Color("#0E7A5A"),
		Dim:      lipgloss.AdaptiveColor{Light: "#555555", Dark: "#555555"},
		CodeBG:   lipgloss.AdaptiveColor{Light: "#F5F5FF", Dark: "#F5F5FF"},
		CodeFG:   lipgloss.AdaptiveColor{Light: "#1A1A2E", Dark: "#1A1A2E"},
		ChipBG:   lipgloss.AdaptiveColor{Light: "#E8E0FF", Dark: "#E8E0FF"},
		ChipFG:   lipgloss.AdaptiveColor{Light: "#2A1F4D", Dark: "#2A1F4D"},
		SysBG:    lipgloss.AdaptiveColor{Light: "#F0F0F0", Dark: "#F0F0F0"},
		FileBG:   lipgloss.AdaptiveColor{Light: "#EDE7FF", Dark: "#EDE7FF"},
		FileFG:   lipgloss.AdaptiveColor{Light: "#1A0F3A", Dark: "#1A0F3A"},
		CmdBG:    lipgloss.AdaptiveColor{Light: "#FFF4D6", Dark: "#FFF4D6"},
		CmdFG:    lipgloss.AdaptiveColor{Light: "#5C4A1F", Dark: "#5C4A1F"},
		DelBG:    lipgloss.AdaptiveColor{Light: "#FFECEC", Dark: "#FFECEC"},
		DelFG:    lipgloss.AdaptiveColor{Light: "#8A1F1F", Dark: "#8A1F1F"},
		AddBG:    lipgloss.AdaptiveColor{Light: "#E6F5E6", Dark: "#E6F5E6"},
		AddFG:    lipgloss.AdaptiveColor{Light: "#1F6B2E", Dark: "#1F6B2E"},
		GutFG:    lipgloss.AdaptiveColor{Light: "#777777", Dark: "#777777"},
		Name:     "light",
	}
}

func For(name string) Theme {
	if name == "light" {
		return Light()
	}
	return Dark()
}

func Splash(accent lipgloss.Color) string {
	word := lipgloss.NewStyle().Bold(true).Foreground(accent).Render("ycode")
	sub := lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#666666", Dark: "#888888"}).Render(
		"terminal AI coding harness  ·  Tab: modes  ·  /connect: providers  ·  /help: commands")
	return "\n" + word + "\n\n" + sub + "\n"
}
