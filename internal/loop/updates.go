package loop

import (
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/danilrwx/telega/tui"
)

type (
	updateAvailable struct{ dialog *tui.UpdateDialog }
	updateInstalled struct {
		dialog *tui.UpdateDialog
		err    error
	}
)

func (m *app) checkUpdate() tea.Cmd {
	return func() tea.Msg {
		dialog, err := tui.CheckForUpdate(m.ctx, m.updateApp)
		if err != nil || dialog == nil {
			return nil
		}

		return updateAvailable{dialog}
	}
}

func (m *app) updateKey(key tea.KeyMsg) tea.Cmd {
	dialog := m.updateDialog
	switch dialog.Key(commandKeyName(key)) {
	case "close":
		m.updateDialog = nil
	case "install":
		return func() tea.Msg {
			target, err := os.Executable()
			if err == nil {
				err = dialog.App.Install(m.ctx, dialog.Release, target)
			}

			return updateInstalled{dialog, err}
		}
	}

	return nil
}
