package loop

import (
	"bytes"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestPhotoOpensInternallyBeforeExternalViewer(t *testing.T) {
	t.Setenv("TUI_KITTY_GRAPHICS", "1")
	tmp := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", tmp)
	t.Setenv("PATH", tmp+string(os.PathListSeparator)+os.Getenv("PATH"))
	opened := filepath.Join(tmp, "opened")
	t.Setenv("LOOP_TEST_OPENED", opened)

	if err := os.WriteFile(filepath.Join(tmp, "xdg-open"), []byte("#!/bin/sh\nprintf '%s' \"$1\" > \"$LOOP_TEST_OPENED\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	var img bytes.Buffer
	if err := png.Encode(&img, image.NewRGBA(image.Rect(0, 0, 96, 64))); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v4/files/photo/info":
			_, _ = io.WriteString(w, `{"id":"photo","name":"photo.png","mime_type":"image/png"}`)
		case "/api/v4/files/photo":
			_, _ = w.Write(img.Bytes())
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	m := newApp(t.Context(), newClient(server.URL, "fixture"))
	m.active, m.focus = "chat", channelPane
	p := post{ID: "post", Text: "https://example.org", Files: []string{"photo"}}
	p.Metadata.Files = []fileInfo{{ID: "photo", Name: "photo.png"}}
	m.timeline.posts = []post{p}

	cmd := m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("o")})
	if cmd == nil || len(m.resources) != 0 {
		t.Fatal("opening the photo should skip the resource list")
	}

	result, ok := cmd().(imagePreviewResult)
	if !ok || result.image == nil || filepath.Base(result.path) != "photo.png" {
		t.Fatalf("expected internal preview, got %+v", result)
	}

	if _, err := os.Stat(opened); !os.IsNotExist(err) {
		t.Fatal("first key must not launch the external viewer")
	}

	preview := imagePreview{image: result.image, path: result.path, title: result.title}
	if err := preview.readKeys(strings.NewReader("щй")); err != nil {
		t.Fatal(err)
	}

	if got, err := os.ReadFile(opened); err != nil || string(got) != result.path {
		t.Fatalf("second key should open the cached original: %q, %v", got, err)
	}

	m.resourceOpening = false
	m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("O")})

	if len(m.resources) != 2 {
		t.Fatalf("expected both the image and the link in the resource list: %+v", m.resources)
	}
}
