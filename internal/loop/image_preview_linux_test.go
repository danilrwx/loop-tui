package loop

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/danilrwx/telega/tui"
	"golang.org/x/sys/unix"
)

type previewTestModel struct {
	*app
	image *tui.TerminalImage
}

func (m *previewTestModel) Init() tea.Cmd {
	return func() tea.Msg { return imagePreviewResult{image: m.image, title: "preview-test"} }
}

func TestImagePreviewInRealTerminal(t *testing.T) {
	if os.Getenv("LOOP_INLINE_VISUAL_HELPER") == "1" {
		runInlineVisualHelper(t)
		return
	}

	if os.Getenv("LOOP_PREVIEW_HELPER") == "1" {
		var encoded bytes.Buffer
		if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 96, 64))); err != nil {
			t.Fatal(err)
		}

		im, err := tui.DecodeTerminalImage(&encoded)
		if err != nil {
			t.Fatal(err)
		}

		model := &previewTestModel{app: newApp(t.Context(), newClient("https://example.org", "fixture")), image: im}
		model.inlineImages = tui.NewInlineImages(os.Stdout)

		program := tea.NewProgram(model, tea.WithInput(os.Stdin), tea.WithOutput(model.inlineImages), tea.WithAltScreen())
		if _, err := program.Run(); err != nil {
			t.Fatal(err)
		}

		return
	}

	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = master.Close() }()

	if err := unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		t.Fatal(err)
	}

	number, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPTN)
	if err != nil {
		t.Fatal(err)
	}

	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = slave.Close() }()

	if err := unix.IoctlSetWinsize(int(slave.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 32, Col: 120, Xpixel: 960, Ypixel: 512}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()

	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestImagePreviewInRealTerminal$")

	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "TUI_GRAPHICS=") && !strings.HasPrefix(entry, "TMUX=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}

	cmd.Env = append(cmd.Env, "LOOP_PREVIEW_HELPER=1", "TUI_GRAPHICS=sixel", "TMUX=")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave

	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { cancel(); _ = cmd.Wait() })

	chunks := make(chan []byte, 4)
	go func() {
		defer close(chunks)

		for {
			buffer := make([]byte, 8192)

			n, err := master.Read(buffer)
			if n > 0 {
				select {
				case chunks <- buffer[:n]:
				case <-ctx.Done():
					return
				}
			}

			if err != nil {
				return
			}
		}
	}()

	var output []byte

	previewShown := false

	for {
		select {
		case data, ok := <-chunks:
			if !ok {
				t.Fatalf("preview process ended early: %q", output)
			}

			output = append(output, data...)
			if !previewShown && bytes.Contains(output, []byte("\x1bP0;1q")) {
				previewShown = true
				output = nil

				if _, err := io.WriteString(master, "q"); err != nil {
					t.Fatal(err)
				}
			} else if previewShown && bytes.Contains(output, []byte("Chats")) {
				if _, err := io.WriteString(master, "\x03"); err != nil {
					t.Fatal(err)
				}

				return
			}
		case <-ctx.Done():
			t.Fatalf("image preview did not render or restore the UI (shown=%t): %q", previewShown, output)
		}
	}
}

type inlineVisualModel struct {
	*app
	enable  string
	enabled bool
}

func (m *inlineVisualModel) Init() tea.Cmd { return nextTick() }
func (m *inlineVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if !m.enabled {
		if _, err := os.Stat(m.enable); err == nil {
			_ = os.Setenv("TUI_GRAPHICS", "sixel")
			m.enabled = true
		}
	}

	_, cmd := m.app.Update(msg)
	if _, ok := msg.(tea.WindowSizeMsg); ok {
		_ = os.WriteFile(m.enable+".ready", []byte("ready"), 0o600)
	}

	return m, cmd
}

func runInlineVisualHelper(t *testing.T) {
	t.Helper()
	t.Setenv("TUI_GRAPHICS", "off")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	src := image.NewRGBA(image.Rect(0, 0, 32, 32))

	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			src.SetRGBA(x, y, color.RGBA{R: 255, A: 255})
		}
	}

	var data bytes.Buffer
	if err := png.Encode(&data, src); err != nil {
		t.Fatal(err)
	}

	posts := []post{
		{ID: "one", UserID: "author", Author: "Visual first", Text: "First avatar should be red.", ChannelID: "chat", CreatedAt: time.Now().UnixMilli()},
		{ID: "two", UserID: "author-two", Author: "Visual second", Text: "Second avatar should be red.", ChannelID: "chat", CreatedAt: time.Now().Add(-time.Minute).UnixMilli()},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/image"):
			_, _ = w.Write(data.Bytes())
		case r.URL.Path == "/api/v4/channels/chat/posts":
			_ = json.NewEncoder(w).Encode(postList{Order: []string{"one", "two"}, Posts: map[string]post{"one": posts[0], "two": posts[1]}})
		case r.URL.Path == "/api/v4/users/me/channels":
			_, _ = io.WriteString(w, `[{"id":"chat","display_name":"Avatar visual check"}]`)
		default:
			_, _ = io.WriteString(w, "[]")
		}
	}))
	defer server.Close()

	m := newApp(t.Context(), newClient(server.URL, "fixture"))
	m.preferences.HideChatListWhenOpen = true
	m.preferences.ManualRead = true
	m.active, m.focus = "chat", channelPane
	m.sequence = 1
	m.channels = []channel{{ID: "chat", DisplayName: "Avatar visual check"}}
	m.timeline.posts = posts

	m.inlineImages = tui.NewImageOutput(os.Stdout)
	defer m.inlineImages.Clear()

	model := &inlineVisualModel{app: m, enable: os.Getenv("LOOP_INLINE_ENABLE_FILE")}
	if _, err := tea.NewProgram(model, tea.WithInput(os.Stdin), tea.WithOutput(m.inlineImages), tea.WithAltScreen()).Run(); err != nil {
		t.Fatal(err)
	}
}
