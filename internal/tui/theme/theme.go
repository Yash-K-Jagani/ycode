package theme

import "github.com/charmbracelet/lipgloss"

type Theme struct {
	Accent   lipgloss.Color
	User     lipgloss.AdaptiveColor
	AIBorder lipgloss.Color
	Dim      lipgloss.AdaptiveColor
}

func Dark() Theme {
	return Theme{
		Accent:   lipgloss.Color("#7C5CFF"),
		User:     lipgloss.AdaptiveColor{Light: "#1a1a2e", Dark: "#1a1a2e"},
		AIBorder: lipgloss.Color("#2de1a7"),
		Dim:      lipgloss.AdaptiveColor{Light: "#666666", Dark: "#888888"},
	}
}

func Splash(accent lipgloss.Color) string {
	word := lipgloss.NewStyle().Bold(true).Foreground(accent).Render("ycode")
	sub := lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#666666", Dark: "#888888"}).Render(
		"terminal AI coding harness  ·  Tab: modes  ·  /connect: providers  ·  /help: commands")
	return "\n" + word + "\n\n" + sub + "\n"
}
