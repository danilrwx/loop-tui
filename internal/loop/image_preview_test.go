package loop

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
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
	"github.com/danilrwx/telega/tui"
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

func TestImagePreviewRecoversAfterTmuxCapabilitiesChange(t *testing.T) {
	t.Setenv("TUI_GRAPHICS", "")
	t.Setenv("TUI_KITTY_GRAPHICS", "")
	t.Setenv("TERM", "tmux-256color")
	t.Setenv("TMUX", "test")
	t.Setenv("TMUX_PANE", "%test")
	bin := t.TempDir()
	state := filepath.Join(bin, "features")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("LOOP_TEST_TMUX_FEATURES", state)

	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte("#!/bin/sh\ncat \"$LOOP_TEST_TMUX_FEATURES\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(state, []byte("linux|off|1|bpaste|1"), 0o600); err != nil {
		t.Fatal(err)
	}

	tui.RefreshTerminalGraphics()

	if tui.ImageGraphics() {
		t.Fatal("initial terminal unexpectedly supports graphics")
	}

	if err := os.WriteFile(state, []byte("foot|on|1|RGB,sixel|1"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 32, 32))); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/info") {
			_, _ = io.WriteString(w, `{"id":"photo","name":"photo.png","mime_type":"image/png"}`)
		} else {
			_, _ = w.Write(data.Bytes())
		}
	}))
	defer server.Close()

	m := newApp(t.Context(), newClient(server.URL, "fixture"))
	p := post{Files: []string{"photo"}}
	p.Metadata.Files = []fileInfo{{ID: "photo", Name: "photo.png"}}

	cmd := m.openPostResource(p)
	if cmd == nil {
		t.Fatal("cached graphics failure prevented the retry")
	}

	result, ok := cmd().(imagePreviewResult)
	if !ok || result.err != nil || result.image == nil {
		t.Fatalf("preview did not recover in the existing window: %+v", result)
	}

	result.image.Release()
}

func TestPhotoOutsideTerminalPreviewLimitsOpensExternally(t *testing.T) {
	t.Setenv("TUI_GRAPHICS", "sixel")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	bin := t.TempDir()
	opened := filepath.Join(bin, "opened")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("LOOP_TEST_OPENED", opened)

	if err := os.WriteFile(filepath.Join(bin, "xdg-open"), []byte("#!/bin/sh\nprintf '%s' \"$1\" > \"$LOOP_TEST_OPENED\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 32, 32))); err != nil {
		t.Fatal(err)
	}
	// A valid oversized PNG header exercises the limit without allocating its raster.
	header := data.Bytes()
	binary.BigEndian.PutUint32(header[16:20], 5000)
	binary.BigEndian.PutUint32(header[20:24], 4000)
	binary.BigEndian.PutUint32(header[29:33], crc32.ChecksumIEEE(header[12:29]))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/info") {
			_, _ = io.WriteString(w, `{"id":"photo","name":"photo.png","mime_type":"image/png"}`)
		} else {
			_, _ = w.Write(header)
		}
	}))
	defer server.Close()

	m := newApp(t.Context(), newClient(server.URL, "fixture"))

	result, ok := m.openResource(resource{fileID: "photo", label: "photo.png"})().(resourceResult)
	if !ok || result.err != nil {
		t.Fatalf("preview limit blocked external opening: %+v", result)
	}

	if path, err := os.ReadFile(opened); err != nil || filepath.Base(string(path)) != "photo.png" {
		t.Fatal("large image was not passed to the external viewer")
	}
}
