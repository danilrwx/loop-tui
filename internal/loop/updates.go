package loop

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/danilrwx/loop-tui/internal/bundle"
	"github.com/danilrwx/telega/tui"
	"github.com/danilrwx/telega/updater"
)

// RunUpdateCLI also refreshes an installed notification service after updating the binary.
func RunUpdateCLI(ctx context.Context, out io.Writer, app updater.App, install bool) error {
	target, err := os.Executable()
	if err != nil {
		return err
	}

	release, err := app.Check(ctx)
	if err != nil {
		return err
	}

	if !app.Newer(release) {
		fmt.Fprintf(out, "loop %s is up to date.\n", app.Version)
		return nil
	}

	if !install {
		fmt.Fprintf(out, "loop %s is available. Run loop --update to install it.\n", release.Version)
		return nil
	}

	if err := bundle.Install(ctx, release, target); err != nil {
		return err
	}

	if err := syncUpdatedService(ctx, target); err != nil {
		return err
	}

	fmt.Fprintf(out, "Installed loop and loopd %s. Restart the interface to use it.\n", release.Version)

	return nil
}

// Execute the verified new binary so service updates use its embedded template.
func syncUpdatedService(ctx context.Context, target string) error {
	output, err := exec.CommandContext(ctx, target, "--sync-notification-service").CombinedOutput()
	if err != nil {
		return fmt.Errorf("client installed, but notification service configuration failed: %w: %s", err, output)
	}

	return nil
}

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
				err = bundle.Install(m.ctx, dialog.Release, target)
			}

			if err == nil {
				err = syncUpdatedService(m.ctx, target)
			}

			return updateInstalled{dialog, err}
		}
	}

	return nil
}
