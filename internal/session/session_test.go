package session

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBrowserSessionIsScopedToConfiguredServer(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	t.Setenv("LOOP_SERVER", "")
	t.Setenv("LOOP_TOKEN", "")

	if err := os.MkdirAll(filepath.Join(config, "matterhorn"), 0o700); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(config, "matterhorn", "config.ini"), []byte("[mattermost]\nhost = chat.example.org\ntokencmd = printf local-session\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	server, err := Server("")
	if err != nil || server != "https://chat.example.org" {
		t.Fatalf("existing server not loaded: %s %v", server, err)
	}

	token, err := Token(t.Context(), server, false)
	if err != nil || token != "local-session" {
		t.Fatal("existing SSO login was not reused")
	}

	if err := os.WriteFile(filepath.Join(config, "matterhorn", "config.ini"), []byte("[mattermost]\nhost: chat.example.org\ntokencmd: printf local-session\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if configured, _ := Server(""); configured != server {
		t.Fatal("INI colon syntax was not supported")
	}

	if token, err := Token(t.Context(), server, false); err != nil || token != "local-session" {
		t.Fatal("INI colon syntax broke SSO")
	}

	for _, scenario := range []struct {
		server   string
		explicit bool
	}{{"https://another.example.org", false}, {server, true}} {
		token, err := Token(t.Context(), scenario.server, scenario.explicit)
		if err != nil || token != "" {
			t.Fatal("configured session leaked into an explicit or different server")
		}
	}

	t.Setenv("LOOP_TOKEN", "explicit-session")

	token, err = Token(t.Context(), server, true)
	if err != nil || token != "explicit-session" {
		t.Fatal("environment token was ignored")
	}

	if _, err := Server("http://unsafe.example.org"); err == nil {
		t.Fatal("unencrypted server URL was accepted")
	}
}
