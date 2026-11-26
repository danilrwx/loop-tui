package loop

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/danilrwx/telega/tui"
	"github.com/danilrwx/telega/updater"
	"golang.org/x/term"
)

// Options configure the client at startup.
type Options struct {
	Notifications bool
	Version       string
	Server        string
	Login         bool
	Demo          bool
}

// Run starts Loop TUI and waits for it to exit.
func Run(opts Options) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()

	if opts.Demo {
		if opts.Notifications {
			return errors.New("background notifications are not available in the offline demo")
		}

		return runClient(ctx, newDemoClient(), true, opts.Version, false)
	}

	rawServer := strings.TrimSpace(opts.Server)
	if rawServer == "" {
		rawServer = strings.TrimSpace(os.Getenv("LOOP_SERVER"))
	}

	if rawServer == "" {
		return errors.New("set LOOP_SERVER or pass --server with your server URL")
	}

	address, err := url.Parse(strings.TrimRight(rawServer, "/"))
	if err != nil || address.Scheme != "https" || address.Hostname() == "" || address.User != nil ||
		address.RawQuery != "" || address.Fragment != "" || address.ForceQuery {
		return errors.New("use an HTTPS server URL without credentials, query or fragment")
	}

	if opts.Login {
		return launchResource(address.String())
	}

	token, err := readToken(address.Host)
	if err != nil {
		return err
	}

	return runClient(ctx, newClient(address.String(), token), false, opts.Version, opts.Notifications)
}

func runClient(ctx context.Context, api *client, demo bool, version string, background bool) error {
	if err := api.connect(ctx); err != nil {
		return err
	}

	model := newApp(ctx, api)

	model.background = background
	if !demo {
		if !background {
			model.updateApp = updater.App{Name: "loop-tui", Repository: "danilrwx/loop-tui", Version: version}
		}

		if !background {
			model.discussionCachePath = api.discussionCachePath()
			model.followed = readDiscussionCache(model.discussionCachePath)
			model.discussionsComplete = len(model.followed) > 40

			model.chatStatus = tui.OpenChatStatus()
			defer model.chatStatus.Clear()
		}

		model.loadPreferences()

		if notifier, err := exec.LookPath("notify-send"); err == nil {
			model.notifier = notifier

			lease, err := model.startNotifications()
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}

				return err
			}

			if lease != nil {
				defer func() { _ = lease.Close() }()
			}
		} else if background {
			return errors.New("background notifications require notify-send")
		}

		if !background {
			var err error

			model.draftStore, err = tui.OpenDraftStore("loop-tui")
			if err != nil {
				model.status = "Drafts: " + err.Error()
			}
		}
	}

	if background {
		model.notificationExecutable, _ = os.Executable()
		model.notificationBinary, _ = os.Stat(model.notificationExecutable)

		_, err := tea.NewProgram(model, tea.WithInput(nil), tea.WithOutput(io.Discard), tea.WithoutRenderer(), tea.WithoutSignalHandler(), tea.WithContext(ctx)).Run()
		if ctx.Err() != nil {
			return nil
		}

		if model.notificationFailure != nil {
			return model.notificationFailure
		}

		return err
	}

	options := []tea.ProgramOption{tea.WithAltScreen(), tea.WithContext(ctx)}

	model.inlineImages = tui.NewInlineImages(os.Stdout)
	if model.inlineImages != nil {
		defer model.inlineImages.Clear()

		options = append(options, tea.WithOutput(model.inlineImages))
	}

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
