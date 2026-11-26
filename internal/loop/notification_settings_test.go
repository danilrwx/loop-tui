package loop

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestNotificationSettingsPersist(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	c := newClient("https://example.org", "test")
	m := newApp(t.Context(), c)
	m.loadPreferences()
	m.channels = []channel{{ID: "duty", DisplayName: "Duty"}, {ID: "general", DisplayName: "General"}}
	m.channelCursor = "duty"
	m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("N")})
	m.settingsCursor = 3
	m.settingsKey(tea.KeyMsg{Type: tea.KeyEnter})

	reopened := newApp(t.Context(), c)
	reopened.loadPreferences()

	if !reopened.watchesChannel("duty") || reopened.watchesChannel("general") || reopened.preferences.MentionReminder != untilEyes {
		t.Fatalf("unexpected notification settings after restart: %+v", reopened.preferences)
	}
}
