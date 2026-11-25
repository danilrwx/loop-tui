package loop

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/danilrwx/telega/tui"
	"github.com/danilrwx/telega/updater"
)

func TestUpdateWaitsForComposerAndReportsInstallationFailure(t *testing.T) {
	m := newApp(t.Context(), newDemoClient())
	m.width, m.height = 100, 24
	m.composing = true
	dialog := &tui.UpdateDialog{App: updater.App{Name: "loop-tui"}, Release: updater.Release{Version: "v0.2.0"}, Selected: 1}
	m.Update(updateAvailable{dialog})

	if m.updateDialog != nil || m.pendingUpdate != dialog {
		t.Fatal("update prompt interrupted the composer")
	}

	m.composing = false
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})

	if m.updateDialog != dialog || m.pendingUpdate != nil {
		t.Fatal("pending prompt did not open after composing")
	}

	cmd := m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil || !dialog.Installing {
		t.Fatal("confirmation did not start the update")
	}

	m.Update(updateInstalled{dialog, errors.New("permission denied")})

	if !strings.Contains(m.View(), "permission denied") {
		t.Fatal("installation error was hidden")
	}

	m.key(tea.KeyMsg{Type: tea.KeyEsc})

	if m.updateDialog != nil {
		t.Fatal("failed update cannot be dismissed")
	}
}
