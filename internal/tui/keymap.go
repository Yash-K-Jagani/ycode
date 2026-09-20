package tui

import "github.com/charmbracelet/bubbles/key"

type KeyMap struct {
	Quit      key.Binding
	Interrupt key.Binding
	New       key.Binding
	Models    key.Binding
	Palette   key.Binding
	History   key.Binding
}

func DefaultKeyMap() KeyMap {
	return KeyMap{
		Quit:      key.NewBinding(key.WithKeys("ctrl+d"), key.WithHelp("ctrl+d", "exit")),
		Interrupt: key.NewBinding(key.WithKeys("ctrl+c", "esc"), key.WithHelp("ctrl+c", "cancel")),
		New:       key.NewBinding(key.WithKeys("ctrl+n"), key.WithHelp("ctrl+n", "new session")),
		Models:    key.NewBinding(key.WithKeys("ctrl+o"), key.WithHelp("ctrl+o", "models")),
		Palette:   key.NewBinding(key.WithKeys("ctrl+p"), key.WithHelp("ctrl+p", "commands")),
		History:   key.NewBinding(key.WithKeys("ctrl+r"), key.WithHelp("ctrl+r", "sessions")),
	}
}
