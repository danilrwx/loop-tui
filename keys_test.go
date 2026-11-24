package main

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestRussianShortcutsPreserveInput(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	m := newApp(t.Context(), nil)
	m.width, m.height = 100, 25
	m.active, m.focus = "chat", channelPane
	p := post{ID: "post", Text: "Message"}
	m.timeline.posts = []post{p}
	press := func(r rune) { m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}) }

	press('А')
	if !m.fullPane {
		t.Fatal("Shift+F on the Russian layout should expand the conversation")
	}
	press('м')
	if m.reader.ID != p.ID {
		t.Fatal("the Russian equivalent of v should open the message reader")
	}
	press('й')
	if m.reader.ID != "" {
		t.Fatal("the Russian equivalent of q should close the reader")
	}
	press('ш')
	press('о')
	m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("ш"), Paste: true})
	if !m.composing || m.draft.Value() != "ош" {
		t.Fatalf("composer should keep the original letters, got %q", m.draft.Value())
	}
	m.key(tea.KeyMsg{Type: tea.KeyEsc})
	press('.')
	press('л')
	if !m.filtering || m.query.Value() != "л" {
		t.Fatalf("filter should keep the original letters, got %q", m.query.Value())
	}
	m.key(tea.KeyMsg{Type: tea.KeyEsc})
	m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("ш"), Paste: true})
	if m.composing {
		t.Fatal("pasted letters must not activate shortcuts")
	}
}

func TestSidebarResetsAfterOpeningUsernameMatch(t *testing.T) {
	m := newApp(t.Context(), newClient("https://fixture", "fixture"))
	m.channels = []channel{
		{ID: "one", DisplayName: "General"},
		{ID: "two", DisplayName: "Мира Чен", Username: "mira_chen", Direct: true},
	}
	m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m.key(tea.KeyMsg{Type: tea.KeyRunes, Paste: true, Runes: []rune("@MIRA_")})
	if m.channelCursor != "two" {
		t.Fatalf("expected username match, got %q", m.channelCursor)
	}
	m.key(tea.KeyMsg{Type: tea.KeyEnter})
	if m.active != "two" || m.query.Value() != "" || len(m.filteredChannels()) != 2+len(feedChannels) {
		t.Fatal("opening the matched chat must restore the sidebar")
	}
}

func TestReactionShortcutClosesQuickPicker(t *testing.T) {
	m := newApp(t.Context(), nil)
	m.focus = channelPane
	m.timeline.posts = []post{{ID: "post"}}
	m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("z")})
	if m.reactionTarget.ID == "" {
		t.Fatal("z did not open the reaction picker")
	}
	m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("я")})
	if m.reactionTarget.ID != "" {
		t.Fatal("the Russian equivalent of z should close quick reactions")
	}
}
