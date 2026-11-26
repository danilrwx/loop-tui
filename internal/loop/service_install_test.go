package loop

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danilrwx/loop-tui/contrib"
)

func mockUserSystemd(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "calls")

	script := `#!/bin/sh
printf '%s\n' "$*" >> "$SYSTEMD_TEST_LOG"
case "$2" in
 show-environment) test "$SYSTEMD_TEST_AVAILABLE" = yes ;;
 is-active) test "$SYSTEMD_TEST_ACTIVE" = yes ;;
 daemon-reload) test "$SYSTEMD_TEST_FAIL" != reload ;;
 restart) test "$SYSTEMD_TEST_FAIL" != restart ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "systemctl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SYSTEMD_TEST_LOG", log)
	t.Setenv("SYSTEMD_TEST_AVAILABLE", "yes")
	t.Setenv("SYSTEMD_TEST_ACTIVE", "yes")
	t.Setenv("SYSTEMD_TEST_FAIL", "")

	return log
}

func TestNotificationServiceInstallAndUpdatePreserveLauncherAndState(t *testing.T) {
	log := mockUserSystemd(t)
	path := filepath.Join(t.TempDir(), "systemd", "user", notificationUnit)
	launcher := `/custom dir/100%/$user/"loop\tui`

	var out bytes.Buffer
	if err := configureNotificationService(t.Context(), &out, path, launcher, contrib.NotificationService); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	expected := `ExecStart="/custom dir/100%%/$$user/\"loop\\tui"`
	if !strings.Contains(string(data), expected) {
		t.Fatalf("invalid launcher escaping: %s", data)
	}

	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o644 {
		t.Fatal("invalid unit permissions")
	}

	calls, _ := os.ReadFile(log)
	if !strings.Contains(string(calls), "enable --now "+notificationUnit) {
		t.Fatal("installation did not enable the service")
	}

	for _, active := range []string{"yes", "no"} {
		t.Setenv("SYSTEMD_TEST_ACTIVE", active)

		if err := os.WriteFile(log, nil, 0o600); err != nil {
			t.Fatal(err)
		}

		updated := strings.Replace(contrib.NotificationService, "RestartSec=5", "RestartSec=9", 1)
		if err := configureNotificationService(t.Context(), &out, path, "", updated); err != nil {
			t.Fatal(err)
		}

		data, _ = os.ReadFile(path)
		if !strings.Contains(string(data), "RestartSec=9") || !strings.Contains(string(data), expected) {
			t.Fatal("update lost the template or custom launcher")
		}

		calls, _ = os.ReadFile(log)
		if strings.Contains(string(calls), "enable") || strings.Contains(string(calls), "--user start ") || !strings.Contains(string(calls), "daemon-reload") {
			t.Fatalf("update changed enabled state: %s", calls)
		}

		if strings.Contains(string(calls), "restart ") != (active == "yes") {
			t.Fatalf("update changed running state: %s", calls)
		}
	}
}

func TestNotificationServiceUnavailableAndFailure(t *testing.T) {
	log := mockUserSystemd(t)
	path := filepath.Join(t.TempDir(), notificationUnit)

	var out bytes.Buffer
	if err := configureNotificationService(t.Context(), &out, path, "", contrib.NotificationService); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatal("update configured a service that was never installed")
	}

	t.Setenv("SYSTEMD_TEST_AVAILABLE", "no")

	if err := configureNotificationService(t.Context(), &out, path, "/bin/loop-tui", contrib.NotificationService); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out.String(), "enable --now") {
		t.Fatal("offline installation did not explain activation")
	}

	if err := configureNotificationService(t.Context(), &out, path, "", contrib.NotificationService); err == nil {
		t.Fatal("update hid an unapplied service change")
	}

	t.Setenv("SYSTEMD_TEST_AVAILABLE", "yes")
	t.Setenv("SYSTEMD_TEST_FAIL", "restart")

	if err := configureNotificationService(t.Context(), &out, path, "", contrib.NotificationService); err == nil {
		t.Fatal("service restart failure was hidden")
	}

	t.Setenv("SYSTEMD_TEST_FAIL", "")

	if err := configureNotificationService(t.Context(), &out, path, "", contrib.NotificationService); err != nil {
		t.Fatal("retry did not apply the saved unit:", err)
	}
}

func TestServiceSyncRunsTheInstalledBinary(t *testing.T) {
	target := filepath.Join(t.TempDir(), "new-client")
	marker := filepath.Join(t.TempDir(), "synced")
	t.Setenv("SERVICE_SYNC_MARKER", marker)

	script := "#!/bin/sh\ntest \"$1\" = --sync-notification-service || exit 2\nprintf '%s' new-template > \"$SERVICE_SYNC_MARKER\"\n"
	if err := os.WriteFile(target, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := syncUpdatedService(t.Context(), target); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(marker)
	if string(data) != "new-template" {
		t.Fatal("update did not execute the new client's service installer")
	}

	if err := os.WriteFile(target, []byte("#!/bin/sh\necho failed >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := syncUpdatedService(t.Context(), target); err == nil || !strings.Contains(err.Error(), "client installed") {
		t.Fatal("partial update failure was hidden")
	}
}

func TestServiceUpdateMigratesTheCombinedClientToLoopd(t *testing.T) {
	mockUserSystemd(t)
	path := filepath.Join(t.TempDir(), notificationUnit)

	previous := strings.Replace(contrib.NotificationService, "ExecStart=%h/.local/bin/loopd", "ExecStart=\"/custom location/loop-tui\" --notify", 1)
	if err := os.WriteFile(path, []byte(previous), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := configureNotificationService(t.Context(), &out, path, "", contrib.NotificationService); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "ExecStart=\"/custom location/loopd\"") || strings.Contains(string(data), "--notify") {
		t.Fatalf("old service was not migrated: %s", data)
	}
}
