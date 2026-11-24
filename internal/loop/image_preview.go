package loop

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/term"
	"github.com/danilrwx/telega/tui"
)

type imagePreviewResult struct {
	image        *tui.TerminalImage
	title        string
	path         string
	reactionPost string
	err          error
}

type imagePreview struct {
	image *tui.TerminalImage
	title string
	path  string
	in    io.Reader
	out   io.Writer
}

func (p *imagePreview) SetStdin(in io.Reader)   { p.in = in }
func (p *imagePreview) SetStdout(out io.Writer) { p.out = out }
func (*imagePreview) SetStderr(io.Writer)       {}

func (p *imagePreview) Run() error {
	defer p.image.Release()

	tty, ok := p.in.(*os.File)
	if !ok || !term.IsTerminal(tty.Fd()) {
		return errors.New("image preview requires a terminal")
	}

	state, err := term.MakeRaw(tty.Fd())
	if err != nil {
		return err
	}
	defer func() { _ = term.Restore(tty.Fd(), state) }()

	cols, rows, err := term.GetSize(tty.Fd())
	if err != nil {
		return err
	}

	if _, err := fmt.Fprint(p.out, "\x1b[?1049h\x1b[?25l"); err != nil {
		return err
	}

	defer func() { _, _ = fmt.Fprint(p.out, p.image.Delete(), "\x1b[?25h\x1b[?1049l") }()

	if tui.KittyGraphics() && tui.KittyPassthrough() {
		// tmux can discard passthrough while it redraws a switched screen.
		time.Sleep(50 * time.Millisecond)
	}

	if err := p.image.WritePreview(p.out, p.title, cols, rows, p.path != ""); err != nil {
		return err
	}

	return p.readKeys(tty)
}

func (p *imagePreview) readKeys(input io.Reader) error {
	reader := bufio.NewReader(input)
	for {
		key, _, err := reader.ReadRune()
		if err != nil {
			return err
		}

		switch tui.RussianCommandRune(key) {
		case 'o':
			if p.path != "" {
				if err := launchResource(p.path); err != nil {
					return err
				}
			}
		case 3, 27, '\r', '\n', 'q':
			return nil
		}
	}
}

func (m *app) previewEmoji() tea.Cmd {
	name := m.selectedEmoji()
	if name == "" || m.emojiPreviewLoading {
		return nil
	}

	if !tui.ImageGraphics() {
		m.status = "Image preview needs Kitty or Sixel graphics"
		return nil
	}

	m.status = "Loading emoji image…"
	m.emojiPreviewLoading = true
	postID := m.reactionTarget.ID

	return func() tea.Msg {
		result := imagePreviewResult{title: ":" + name + ":", reactionPost: postID}
		result.image, result.err = cachedLoopEmoji(m.ctx, m.api, name, tui.DecodeTerminalImage)

		return result
	}
}
