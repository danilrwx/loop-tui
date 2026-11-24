package loop

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestClipboardPasteAddsImageAndKeepsText(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	t.Setenv("WAYLAND_DISPLAY", "fixture")
	t.Setenv("DISPLAY", "")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	var imageData bytes.Buffer
	if err := png.Encode(&imageData, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}

	fixture := filepath.Join(dir, "fixture.png")
	t.Setenv("TEST_CLIPBOARD_PNG", fixture)

	if err := os.WriteFile(fixture, imageData.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	script := `#!/bin/sh
case "$3" in
  image/png) cat "$TEST_CLIPBOARD_PNG" ;;
  text) printf 'first\nsecond' ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "wl-paste"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	m := newApp(t.Context(), nil)
	m.active, m.focus = "chat", channelPane
	m.compose()
	m.setDraft("Caption")

	paste := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("PNG\r\n\x1a\nIHDR"), Paste: true}

	cmd := m.key(paste)
	if cmd == nil {
		t.Fatal("PNG bytes received from the terminal should trigger an attachment")
	}

	m.Update(cmd())

	files := m.restoreDraftFiles(m.draftKey())
	if len(files) != 1 || filepath.Base(files[0].Path) != "clipboard.png" || !files[0].Managed {
		t.Fatalf("expected a managed clipboard attachment, got %+v", files)
	}

	if data, err := os.ReadFile(files[0].Path); err != nil || !bytes.Equal(data, imageData.Bytes()) {
		t.Fatalf("attachment differs from the clipboard image, read error %v", err)
	}

	if m.draft.Value() != "Caption" {
		t.Fatalf("image paste changed the caption: %q", m.draft.Value())
	}

	if err := os.Remove(fixture); err != nil {
		t.Fatal(err)
	}

	m.setDraft("")
	cmd = m.key(tea.KeyMsg{Type: tea.KeyCtrlV})
	m.Update(cmd())

	if m.draft.Value() != "first\nsecond" || len(m.restoreDraftFiles(m.draftKey())) != 1 {
		t.Fatalf("plain clipboard paste changed attachments or lost text: %q", m.draft.Value())
	}
}
