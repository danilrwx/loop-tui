package loop

import (
	"bytes"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/danilrwx/telega/tui"
)

func TestAttachmentAndCustomEmojiRenderInConversation(t *testing.T) {
	t.Setenv("TUI_KITTY_GRAPHICS", "1")
	t.Setenv("TMUX", "")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	var payload bytes.Buffer
	if err := png.Encode(&payload, image.NewRGBA(image.Rect(0, 0, 96, 64))); err != nil {
		t.Fatal(err)
	}

	requests := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++

		switch r.URL.Path {
		case "/api/v4/files/photo/thumbnail", "/api/v4/emoji/custom/image", "/api/v4/users/author/image":
			_, _ = w.Write(payload.Bytes())
		case "/api/v4/emoji/name/pupupu":
			_, _ = io.WriteString(w, `{"id":"custom"}`)
		default:
			t.Errorf("unexpected request: %s", r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	out, err := os.CreateTemp(t.TempDir(), "terminal")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = out.Close() }()

	m := newApp(t.Context(), newClient(server.URL, "fixture"))
	m.width, m.height = 100, 24
	m.active, m.focus = "channel", channelPane
	m.inlineImages = tui.NewInlineImages(out)
	p := post{ID: "one", UserID: "author", Author: "Test Author", Text: "## :space_invader: Image message :pupupu:", Files: []string{"photo"}}
	p.Metadata.Files = []fileInfo{{ID: "photo", Name: "photo.png"}}
	p.Metadata.Reactions = []reaction{{EmojiName: "pupupu"}, {EmojiName: "pupupu"}, {EmojiName: "eyes"}}
	m.timeline.posts = []post{p}

	cmd := m.loadVisibleImages()
	for range 3 {
		if cmd == nil {
			t.Fatal("missing inline image request")
		}

		_, cmd = m.Update(cmd())
	}

	if cmd != nil {
		t.Fatal("inline media should only be loaded once")
	}

	view := m.View()
	if !strings.Contains(view, "👾 Image message ") || !strings.Contains(view, " 2  👀 1") ||
		strings.Contains(view, ":pupupu:") || strings.Count(view, "\U0010EEEE") < 5 {
		t.Fatalf("expected the photo and custom emoji in the heading and reaction row: %q", view)
	}

	avatar := m.inlineImages.Get("avatar:author")
	if avatar == nil || len(avatar.Rows) != 2 || !strings.Contains(view, avatar.Rows[0]) ||
		!strings.Contains(view, avatar.Rows[1]) {
		t.Fatal("both avatar rows should appear in the conversation")
	}

	m.reader = p
	if reader := m.View(); !strings.Contains(reader, "👾 Image message ") || !strings.Contains(reader, "\U0010EEEE") {
		t.Fatalf("expected emoji in the expanded message: %q", reader)
	}

	m.reader = post{}
	if _, err := m.inlineImages.WriteString(view); err != nil {
		t.Fatal(err)
	}

	wire, err := os.ReadFile(out.Name())
	if err != nil {
		t.Fatal(err)
	}

	if strings.Count(string(wire), ",U=1") != 3 ||
		strings.Index(string(wire), ",U=1") > strings.Index(string(wire), "\U0010EEEE") {
		t.Fatal("Kitty placements should be transmitted before the conversation cells")
	}

	reopened := newClient(server.URL, "fixture")
	if reopened.loadInlineImage(t.Context(), "unknown-file:photo") == nil {
		t.Fatal("reopening an attachment without metadata should reuse its disk thumbnail")
	}

	full, err := cachedLoopEmoji(t.Context(), reopened, "pupupu", tui.DecodeTerminalImage)
	if err != nil {
		t.Fatal(err)
	}

	full.Release()

	if reopened.loadInlineImage(t.Context(), "avatar:author") == nil {
		t.Fatal("reopening an avatar should reuse it from disk")
	}

	if requests != 4 {
		t.Fatalf("reopening cached images sent extra requests: %d total", requests)
	}

	m.configEnabled = true
	m.settingsCursor = 5
	m.settingsKey(tea.KeyMsg{Type: tea.KeyEnter})

	if view := m.View(); strings.Contains(view, avatar.Rows[0]) || strings.Contains(view, avatar.Rows[1]) {
		t.Fatal("disabling avatars should redraw the message without profile photos")
	}

	fresh := newApp(t.Context(), reopened)
	fresh.loadPreferences()

	if !fresh.preferences.HideAvatars {
		t.Fatal("avatar preference should survive a restart")
	}

	m.settingsKey(tea.KeyMsg{Type: tea.KeyEnter})

	if !strings.Contains(m.View(), avatar.Rows[0]) {
		t.Fatal("reenabling avatars should reuse the existing decoded placement")
	}

	m.inlineImages = nil
	if plain := m.View(); !strings.Contains(plain, "👾 Image message :pupupu:") ||
		!strings.Contains(plain, ":pupupu: 2") || strings.Contains(plain, "\U0010EEEE") {
		t.Fatal("plain terminal should retain text reaction labels")
	}
}

func TestAvatarsRecoverInTheSameOutputAfterGraphicsEnable(t *testing.T) {
	t.Setenv("TUI_GRAPHICS", "off")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 32, 32))); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(data.Bytes()) }))

	defer server.Close()

	file, err := os.CreateTemp(t.TempDir(), "terminal")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()

	m := newApp(t.Context(), newClient(server.URL, "fixture"))
	m.width, m.height = 100, 24
	m.active, m.focus = "chat", channelPane
	m.timeline.posts = []post{{ID: "post", UserID: "author", Author: "Test Author", Text: "hello"}}
	m.inlineImages = tui.NewImageOutput(file)

	output := m.inlineImages
	if m.loadVisibleImages() != nil {
		t.Fatal("disabled output requested an avatar")
	}

	t.Setenv("TUI_GRAPHICS", "sixel")

	refresh := m.refreshInlineGraphics(false)
	if refresh == nil {
		t.Fatal("disabled graphics were never rechecked")
	}

	_, load := m.Update(refresh())
	if load == nil {
		t.Fatal("enabling graphics did not schedule the missing avatar")
	}

	m.Update(load())

	if m.inlineImages != output || m.inlineImages.Get("avatar:author") == nil {
		t.Fatal("avatar did not appear in the original output writer")
	}

	if _, err := output.WriteString(m.View()); err != nil {
		t.Fatal(err)
	}

	wire, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Contains(wire, []byte("\x1bP0;1q")) || bytes.Contains(wire, []byte("\U0010EEEE")) {
		t.Fatal("avatar was not sent as a Sixel image")
	}
}
