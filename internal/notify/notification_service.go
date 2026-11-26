package notify

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

type savedNotification struct {
	Post                      post
	Team, Title, Body         string
	Mentioned, Followed, Read bool
	Summary                   int
}
type notificationState struct {
	Cursor  notificationCursor
	Pending map[string]savedNotification
}

func (c *client) notificationStateName() string {
	key := sha256.Sum256([]byte(c.baseURL + "\x00" + c.me.ID))
	return fmt.Sprintf("loop-tui/notifications/%x", key)
}

// Only one notification daemon can own the queue for an account.
func (c *client) notificationLease() (*os.File, error) {
	config, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}

	directory := filepath.Join(config, c.notificationStateName())
	if err = os.MkdirAll(directory, 0o700); err != nil {
		return nil, err
	}

	file, err := os.OpenFile(filepath.Join(directory, "owner.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}

	if err = unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()

		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, nil
		}

		return nil, err
	}

	return file, nil
}

func (m *daemon) startNotifications() (*os.File, error) {
	for {
		lease, err := m.api.notificationLease()
		if err != nil {
			return nil, err
		}

		if lease != nil {
			m.notificationStateName = m.api.notificationStateName()
			if err = m.restoreNotifications(); err != nil {
				_ = lease.Close()
				return nil, err
			}

			m.notificationEvents = m.api.notificationEvents(m.ctx, m.notificationCursor)

			return lease, nil
		}

		if !m.background {
			return nil, nil
		}

		select {
		case <-m.ctx.Done():
			return nil, m.ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func (m *daemon) restoreNotifications() error {
	var state notificationState
	if err := readSettings(m.notificationStateName, &state); err != nil {
		return fmt.Errorf("load pending notifications: %w", err)
	}

	m.notificationCursor = state.Cursor
	for id, saved := range state.Pending {
		if id == "" || saved.Post.ID != id {
			continue
		}

		m.reminders[id] = reminder{
			notificationEvent: notificationEvent{kind: "posted", post: saved.Post, team: saved.Team, title: saved.Title, body: saved.Body, mentioned: saved.Mentioned, followed: saved.Followed},
			due:               time.Now(), read: saved.Read, summary: saved.Summary,
		}
	}

	return nil
}

func (m *daemon) saveNotifications() error {
	state := notificationState{Cursor: m.notificationCursor, Pending: make(map[string]savedNotification, len(m.reminders))}
	for id, item := range m.reminders {
		p := item.post
		state.Pending[id] = savedNotification{
			Post: post{ID: p.ID, ChannelID: p.ChannelID, RootID: p.RootID, UserID: p.UserID, Text: p.Text, CreatedAt: p.CreatedAt},
			Team: item.team, Title: item.title, Body: item.body, Mentioned: item.mentioned, Followed: item.followed,
			Read: item.read, Summary: item.summary,
		}
	}

	if err := writeSettings(m.notificationStateName, state); err != nil {
		return fmt.Errorf("save pending notifications: %w", err)
	}

	m.notificationsDirty = false

	return nil
}

// A server unread count recovers attention even when WebSocket replay has expired.
func (m *daemon) recoverNotifications(now time.Time) command {
	if m.notificationStateName == "" {
		return nil
	}

	active := make(map[string]bool)

	var commands []command

	recoverScope := func(id string, event notificationEvent, count int, viewed int64) {
		known := 0

		for _, pending := range m.reminders {
			if pending.summary == 0 && !pending.read && pending.post.CreatedAt > viewed && pending.post.ChannelID == event.post.ChannelID && pending.post.RootID == event.post.RootID && (!event.mentioned || pending.mentioned) {
				known++
			}
		}

		count -= known
		if count <= 0 {
			return
		}

		active[id] = true
		if old, ok := m.reminders[id]; ok && old.summary == count {
			return
		}

		event.post.ID = id
		event.body = fmt.Sprintf("%d unread %s", count, event.body)
		m.reminders[id] = reminder{notificationEvent: event, due: now.Add(reminderInterval), summary: count}
		m.notificationsDirty = true
		commands = append(commands, m.desktopNotice(event, false))
	}
	for _, ch := range m.channels {
		count := ch.Mentions
		body := "mentions"

		if m.watchesChannel(ch.ID) {
			count = ch.Unread
			body = "messages"
		}

		if count > 0 {
			recoverScope("unread:channel:"+ch.ID, notificationEvent{kind: "posted", post: post{ChannelID: ch.ID}, team: ch.TeamID, title: ch.DisplayName, body: body, mentioned: ch.Mentions > 0}, count, ch.LastViewed)
		}

		for _, thread := range ch.Threads {
			recoverScope("unread:thread:"+thread.root, notificationEvent{kind: "posted", post: post{ChannelID: ch.ID, RootID: thread.root}, team: thread.team, title: ch.DisplayName, body: "replies in a followed thread", followed: true}, thread.unread, thread.lastViewed)
		}
	}

	for id, item := range m.reminders {
		if item.summary != 0 && !active[id] {
			delete(m.reminders, id)
			m.notificationsDirty = true
		}
	}

	return batch(commands...)
}

func (m *daemon) notifierBinaryChanged() bool {
	if m.notificationExecutable == "" || m.notificationBinary == nil {
		return false
	}

	current, err := os.Stat(m.notificationExecutable)

	return err == nil && !os.SameFile(m.notificationBinary, current)
}
