// Package notify implements the background Loop client without terminal dependencies.
package notify

import (
	"context"
	"errors"
	"log"
	"os"
	"os/exec"
	"time"
)

type command func() any

func batch(commands ...command) command {
	var pending []command

	for _, cmd := range commands {
		if cmd != nil {
			pending = append(pending, cmd)
		}
	}

	if len(pending) == 0 {
		return nil
	}

	return func() any { return pending }
}

type settings struct {
	MentionReminder reminderMode               `json:"mention_reminder,omitempty"`
	WatchedChannels map[string]map[string]bool `json:"watched_channels,omitempty"`
}
type daemon struct {
	ctx                    context.Context
	api                    *client
	background             bool
	notificationStateName  string
	notificationCursor     notificationCursor
	notificationsDirty     bool
	notificationFailure    error
	notificationExecutable string
	notificationBinary     os.FileInfo
	notifier               string
	notificationEvents     <-chan notificationEvent
	reminders              map[string]reminder
	remindersChecking      bool
	channels               []channel
	preferences            settings
}

func newDaemon(ctx context.Context, api *client) *daemon {
	return &daemon{ctx: ctx, api: api, reminders: make(map[string]reminder)}
}

func (m *daemon) channelByID(id string) channel {
	for _, ch := range m.channels {
		if ch.ID == id {
			return ch
		}
	}

	return channel{}
}

func (m *daemon) loadPreferences() error {
	var next settings
	if err := readSettings("loop-tui", &next); err != nil {
		return err
	}

	m.preferences = next
	for id, item := range m.reminders {
		if !item.mentioned && !item.followed && !m.watchesChannel(item.post.ChannelID) {
			delete(m.reminders, id)
			m.notificationsDirty = true
		}
	}

	return nil
}

func (m *daemon) persistAndDeliver(cmd command) error {
	if m.notificationsDirty && m.notificationStateName != "" {
		if err := m.saveNotifications(); err != nil {
			return err
		}
	}

	return m.execute(cmd)
}

func (m *daemon) execute(cmd command) error {
	if cmd == nil {
		return nil
	}

	switch result := cmd().(type) {
	case []command:
		for _, next := range result {
			if err := m.execute(next); err != nil {
				return err
			}
		}
	case notifyError:
		return result.err
	case remindersChecked:
		return m.persistAndDeliver(m.receiveReminderChecks(result, time.Now()))
	}

	return nil
}

func (m *daemon) process(msg any) error {
	var cmd command

	switch msg := msg.(type) {
	case notificationEvent:
		cmd = m.receiveNotification(msg, time.Now())
	case remindersChecked:
		cmd = m.receiveReminderChecks(msg, time.Now())
	case notifyError:
		m.notificationFailure = msg.err
		return msg.err
	}

	return m.persistAndDeliver(cmd)
}

// Run listens for notifications independently of the terminal client.
func Run(ctx context.Context, server, token string) error {
	return runClient(ctx, newClient(server, token))
}

func runClient(ctx context.Context, api *client) error {
	if err := api.connect(ctx); err != nil {
		return err
	}

	notifier, err := exec.LookPath("notify-send")
	if err != nil {
		return errors.New("background notifications require notify-send")
	}

	m := newDaemon(ctx, api)
	m.background = true

	m.notifier = notifier
	if err := m.loadPreferences(); err != nil {
		return err
	}

	lease, err := m.startNotifications()
	if err != nil {
		return err
	}

	defer func() { _ = lease.Close() }()

	m.notificationExecutable, _ = os.Executable()
	m.notificationBinary, _ = os.Stat(m.notificationExecutable)

	ticks := time.NewTicker(5 * time.Second)
	defer ticks.Stop()

	poll := time.NewTicker(time.Minute)
	defer poll.Stop()

	type snapshot struct {
		channels []channel
		err      error
	}

	results := make(chan any, 2)
	send := func(result any) {
		select {
		case results <- result:
		case <-ctx.Done():
		}
	}
	loading := false
	refresh := func() {
		if loading {
			return
		}

		loading = true

		go func() { channels, err := api.channels(ctx); send(snapshot{channels, err}) }()
	}
	refresh()

	for {
		select {
		case <-ctx.Done():
			return nil
		case event, ok := <-m.notificationEvents:
			if !ok {
				return ctx.Err()
			}

			if err := m.process(event); err != nil {
				return err
			}

			if event.kind == "connected" {
				refresh()
			}
		case <-poll.C:
			refresh()
		case now := <-ticks.C:
			if m.notifierBinaryChanged() {
				return nil
			}

			if err := m.loadPreferences(); err != nil {
				log.Printf("notification settings: %v", err)
			}

			if err := m.persistAndDeliver(nil); err != nil {
				return err
			}

			if cmd := m.checkReminders(now); cmd != nil {
				go func() { send(cmd()) }()
			}
		case result := <-results:
			switch result := result.(type) {
			case snapshot:
				loading = false

				if result.err != nil {
					if errors.Is(result.err, errUnauthorized) {
						return result.err
					}

					log.Printf("notification recovery: %v", result.err)

					continue
				}

				m.channels = result.channels
				if err := m.persistAndDeliver(m.recoverNotifications(time.Now())); err != nil {
					return err
				}
			default:
				if err := m.process(result); err != nil {
					return err
				}
			}
		}
	}
}
