package main

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/danilrwx/telega/tui"
)

func (m *app) pickAttachment() tea.Cmd {
	m.attachmentPicker = tui.NewFilePicker()
	m.status = ""
	m.resetMention()
	return nil
}

func attachmentInputKey(key tea.KeyMsg) (name, text string) {
	if key.Paste || (key.Type == tea.KeyRunes || key.Type == tea.KeySpace) && !key.Alt {
		return "", string(key.Runes)
	}
	return key.String(), ""
}

func (m *app) attachmentPathKey(key tea.KeyMsg) tea.Cmd {
	name, text := attachmentInputKey(key)
	switch action, path := m.attachmentPicker.Key(name, text); action {
	case "close":
		m.attachmentPicker = nil
		m.status = ""
		if m.composing {
			return m.draft.Focus()
		}
	case "text":
		return m.nameTextAttachment()
	case "attach":
		m.status = ""
		if !m.addAttachment(draftAttachment{Path: path}) {
			m.attachmentPicker.Status = m.status
			return nil
		}
		m.attachmentPicker = nil
		return m.focusAttachmentDraft()
	}
	return nil
}
