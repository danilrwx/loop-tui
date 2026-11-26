package loop

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"

	"github.com/danilrwx/loop-tui/contrib"
)

const notificationUnit = "loop-tui-notifications.service"

// ConfigureNotificationService installs the embedded unit or updates an existing one.
// A nonempty launcher explicitly installs and enables the service; updates preserve its state.
func ConfigureNotificationService(ctx context.Context, out io.Writer, launcher string) error {
	if runtime.GOOS != "linux" {
		return nil
	}

	config, err := os.UserConfigDir()
	if err != nil {
		return err
	}

	return configureNotificationService(ctx, out, filepath.Join(config, "systemd", "user", notificationUnit), launcher, contrib.NotificationService)
}

func configureNotificationService(ctx context.Context, out io.Writer, path, launcher, template string) error {
	previous, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	install := launcher != ""
	if !install && errors.Is(err, os.ErrNotExist) {
		return nil
	}

	var command string

	if install {
		if !filepath.IsAbs(launcher) || strings.ContainsFunc(launcher, unicode.IsControl) {
			return errors.New("notification launcher must be an absolute path without control characters")
		}

		escaped := strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "%", "%%", "$", "$$").Replace(launcher)
		command = "ExecStart=\"" + escaped + "\""
	} else {
		section := ""

		for _, line := range strings.Split(string(previous), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "[") {
				section = line
			}

			if section == "[Service]" && strings.HasPrefix(line, "ExecStart=") && line != "ExecStart=" {
				if command != "" || strings.HasSuffix(line, "\\") {
					return errors.New("cannot preserve a complex service ExecStart; use a systemd drop-in for overrides")
				}

				command = strings.NewReplacer(
					"/loop-tui\" --notify", "/loopd\"",
					"/loop-tui --notify", "/loopd",
					"/loop\" --notify", "/loopd\"",
					"/loop --notify", "/loopd",
				).Replace(line)
			}
		}

		if command == "" {
			return errors.New("installed notification service has no ExecStart")
		}
	}

	lines := strings.Split(template, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "ExecStart=") {
			lines[i] = command
		}
	}

	data := []byte(strings.Join(lines, "\n"))

	if !bytes.Equal(previous, data) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}

		staged, err := os.CreateTemp(filepath.Dir(path), ".loop-tui-service-*")
		if err != nil {
			return err
		}

		defer func() { _ = staged.Close(); _ = os.Remove(staged.Name()) }()

		if _, err = staged.Write(data); err != nil {
			return err
		}

		if err := errors.Join(staged.Chmod(0o644), staged.Sync(), staged.Close()); err != nil {
			return err
		}

		if err := os.Rename(staged.Name(), path); err != nil {
			return err
		}
	}

	if err := exec.CommandContext(ctx, "systemctl", "--user", "show-environment").Run(); err != nil {
		fmt.Fprintln(out, "Notification service saved. User systemd is unavailable; after logging in, run:")
		fmt.Fprintln(out, "  systemctl --user daemon-reload")

		if install {
			fmt.Fprintln(out, "  systemctl --user enable --now "+notificationUnit)
			return nil
		}

		return errors.New("notification service saved, but user systemd is unavailable; run daemon-reload and restart the service when available")
	}

	active := exec.CommandContext(ctx, "systemctl", "--user", "is-active", "--quiet", notificationUnit).Run() == nil

	run := func(args ...string) error {
		output, err := exec.CommandContext(ctx, "systemctl", append([]string{"--user"}, args...)...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("notification service %s: %w: %s", args[0], err, strings.TrimSpace(string(output)))
		}

		return nil
	}
	if err := run("daemon-reload"); err != nil {
		return err
	}

	if install {
		if err := run("enable", "--now", notificationUnit); err != nil {
			return err
		}
	}

	if active {
		if err := run("restart", notificationUnit); err != nil {
			return err
		}
	}

	fmt.Fprintln(out, "Notification service configured: "+path)

	return nil
}
