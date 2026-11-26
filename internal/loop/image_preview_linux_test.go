package loop

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"io"
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
