package loop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/danilrwx/loop-tui/internal/session"
	"github.com/danilrwx/telega/tui"
	"github.com/danilrwx/telega/updater"
	"golang.org/x/term"
)

// Options configure the client at startup.
type Options struct {
	Version string
	Server  string
	Login   bool
	Demo    bool
}

// Run starts Loop TUI and waits for it to exit.
func Run(opts Options) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()

	if opts.Demo {
		return runClient(ctx, newDemoClient(), true, opts.Version)
	}

	server, err := session.Server(opts.Server)
	if err != nil {
		return err
	}

	if opts.Login {
		return launchResource(server)
	}

	token, err := session.Token(ctx, server, opts.Server != "")
	if err != nil {
		return err
	}

	if token == "" {
		token, err = readToken(server)
	}

	if err != nil {
		return err
	}

	return runClient(ctx, newClient(server, token), false, opts.Version)
}

func runClient(ctx context.Context, api *client, demo bool, version string) error {
	if err := api.connect(ctx); err != nil {
		return err
	}

	model := newApp(ctx, api)

	if !demo {
		model.updateApp = updater.App{Name: "loop-tui", Repository: "danilrwx/loop-tui", Version: version}
		model.discussionCachePath = api.discussionCachePath()
		model.followed = readDiscussionCache(model.discussionCachePath)
		model.discussionsComplete = len(model.followed) > 40

		model.chatStatus = tui.OpenChatStatus()
		defer model.chatStatus.Clear()

		model.loadPreferences()

		var err error

		model.draftStore, err = tui.OpenDraftStore("loop-tui")
		if err != nil {
			model.status = "Drafts: " + err.Error()
		}
	}

	options := []tea.ProgramOption{tea.WithAltScreen(), tea.WithReportFocus(), tea.WithContext(ctx)}

	model.inlineImages = tui.NewImageOutput(os.Stdout)
	defer model.inlineImages.Clear()

	options = append(options, tea.WithOutput(&terminalOutput{InlineImages: model.inlineImages}))

	_, err := tea.NewProgram(model, options...).Run()

	if ctx.Err() != nil {
		return nil
	}

	return err
}

func readToken(host string) (string, error) {
	if token := strings.TrimSpace(os.Getenv("LOOP_TOKEN")); token != "" {
		return token, nil
	}

	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", errors.New("set LOOP_TOKEN to an access token for this server")
	}

	fmt.Fprintf(os.Stderr, "Access token for %s: ", host)

	value, err := term.ReadPassword(fd)

	fmt.Fprintln(os.Stderr)

	if err != nil {
		return "", fmt.Errorf("read access token: %w", err)
	}

	if token := strings.TrimSpace(string(value)); token != "" {
		return token, nil
	}

	return "", errors.New("access token is empty")
}
