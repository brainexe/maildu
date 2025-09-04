package ui

import (
	"github.com/charmbracelet/bubbles/key"
)

type keyMap struct {
	Up, Down, Enter, Back, Delete, Strip, Refresh, Quit key.Binding
	ToggleSort, ListMode                                key.Binding
}

func newKeyMap() keyMap {
	return keyMap{
		Up:      key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
		Down:    key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
		Enter:   key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "open")),
		Back:    key.NewBinding(key.WithKeys("h", "backspace"), key.WithHelp("h/⌫", "back")),
		Delete:  key.NewBinding(key.WithKeys("x", "delete"), key.WithHelp("x", "delete msg")),
		Strip:   key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "strip attach")),
		Refresh: key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
		Quit:    key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
		ToggleSort: key.NewBinding(
			key.WithKeys("t"),
			key.WithHelp("t", "toggle sort"),
		),
		ListMode: key.NewBinding(
			key.WithKeys("l"),
			key.WithHelp("l", "list mode"),
		),
	}
}

func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Up, k.Down, k.Enter, k.Back, k.Delete, k.Strip, k.Refresh, k.Quit, k.ToggleSort, k.ListMode}
}

func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.Enter, k.Back},
		{k.Delete, k.Strip, k.Refresh, k.Quit, k.ToggleSort, k.ListMode},
	}
}
