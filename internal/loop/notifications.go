package loop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/coder/websocket"
)

const reminderInterval = time.Minute

type reminderMode string

const (
	untilRead     reminderMode = ""
	untilEyes     reminderMode = "eyes"
	untilReaction reminderMode = "reaction"
)

type notificationEvent struct {
	kind        string
	post        post
	reaction    reaction
	team, title string
	body        string
	mentioned   bool
	followed    bool
}

type notificationCursor struct {
	connection string
	sequence   int
}

type reminder struct {
	notificationEvent
	due  time.Time
	read bool
}

type reminderCheck struct {
	id   string
	mode reminderMode
	done bool
	err  error
}

type (
	remindersChecked []reminderCheck
	notifyError      struct{ err error }
)

func (c *client) notificationEvents(ctx context.Context) <-chan notificationEvent {
	events := make(chan notificationEvent, 64)
	go func() {
		defer close(events)

		var cursor notificationCursor
		for ctx.Err() == nil {
			c.readNotificationEvents(ctx, events, &cursor)

			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
		}
	}()

	return events
}

func (c *client) readNotificationEvents(ctx context.Context, events chan<- notificationEvent, cursor *notificationCursor) {
	address := c.baseURL + "/api/v4/websocket"

	if cursor.connection != "" {
		query := url.Values{"connection_id": {cursor.connection}, "sequence_number": {strconv.Itoa(cursor.sequence)}}
		address += "?" + query.Encode()
	}

	header := http.Header{"Authorization": {"Bearer " + c.token}}
	dialCtx, cancel := context.WithTimeout(ctx, c.http.Timeout)

	//nolint:bodyclose // Dial owns the handshake body, and Conn owns the upgraded stream.
	conn, _, err := websocket.Dial(dialCtx, address, &websocket.DialOptions{
		HTTPClient: c.http, HTTPHeader: header,
	})

	cancel()

	if err != nil {
		return
	}

	defer func() { _ = conn.CloseNow() }()

	conn.SetReadLimit(1 << 20)

	ctx, stop := context.WithCancel(ctx)
	defer stop()

	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
				err := conn.Ping(pingCtx)

				cancel()

				if err != nil {
					stop()
					return
				}
			}
		}
	}()

	for {
		_, payload, err := conn.Read(ctx)
		if err != nil {
			return
		}

		var wire notificationWire
		if json.Unmarshal(payload, &wire) != nil || wire.Event == "" {
			continue
		}

		if wire.Event == "hello" {
			var hello struct {
				Connection string `json:"connection_id"`
			}

			_ = json.Unmarshal(wire.Data, &hello)
			cursor.connection = hello.Connection
		} else if cursor.connection != "" && wire.Sequence != cursor.sequence {
			return
		}

		cursor.sequence = wire.Sequence + 1
		if event, ok := wire.notification(c.me.ID); ok {
			select {
			case events <- event:
			case <-ctx.Done():
				return
			}
		}
	}
}

type notificationWire struct {
	Event    string
	Sequence int `json:"seq"`
	Data     json.RawMessage
}

func (wire notificationWire) notification(userID string) (notificationEvent, bool) {
	switch wire.Event {
	case "posted", "post_deleted", "reaction_added":
	default:
		return notificationEvent{}, false
	}

	var data struct {
		Post      string
		Reaction  string
		Team      string `json:"team_id"`
		Channel   string `json:"channel_display_name"`
		Sender    string `json:"sender_name"`
		Mentions  json.RawMessage
		Followers json.RawMessage
	}
	if json.Unmarshal(wire.Data, &data) != nil {
		return notificationEvent{}, false
	}

	event := notificationEvent{kind: wire.Event, team: data.Team, title: data.Channel}
	switch wire.Event {
	case "posted", "post_deleted":
		if json.Unmarshal([]byte(data.Post), &event.post) != nil || event.post.ID == "" {
			return notificationEvent{}, false
		}

		if wire.Event == "posted" {
			p := event.post
			if p.UserID == "" || p.UserID == userID || p.ChannelID == "" || p.CreatedAt <= 0 || strings.HasPrefix(p.Type, "system_") {
				return notificationEvent{}, false
			}

			event.mentioned = notificationRecipient(data.Mentions, userID)
			event.followed = p.RootID != "" && notificationRecipient(data.Followers, userID)

			event.body = strings.Join(strings.Fields(p.Text), " ")
			if event.body == "" {
				event.body = "New message"
			}

			if data.Sender != "" {
				event.body = data.Sender + ": " + event.body
			}
		}
	case "reaction_added":
		if json.Unmarshal([]byte(data.Reaction), &event.reaction) != nil || event.reaction.UserID != userID {
			return notificationEvent{}, false
		}
	}

	return event, true
}

// Mattermost encodes recipients as a JSON array inside a JSON string.
func notificationRecipient(raw json.RawMessage, userID string) bool {
	if len(raw) > 0 && raw[0] == '"' {
		var encoded string
		if json.Unmarshal(raw, &encoded) != nil {
			return false
		}

		raw = json.RawMessage(encoded)
	}

	var users []string

	return json.Unmarshal(raw, &users) == nil && slices.Contains(users, userID)
}

func (m *app) waitNotificationEvent() tea.Cmd {
	if m.notificationEvents == nil {
		return nil
	}

	return func() tea.Msg {
		event, ok := <-m.notificationEvents
		if !ok {
			return nil
		}

		return event
	}
}

func (m *app) receiveNotification(event notificationEvent, now time.Time) tea.Cmd {
	switch event.kind {
	case "post_deleted":
		delete(m.reminders, event.post.ID)

		for id, item := range m.reminders {
			if item.post.RootID == event.post.ID {
				delete(m.reminders, id)
			}
		}
	case "reaction_added":
		m.acknowledgeReaction(event.reaction)
	case "posted":
		p := event.post
		if !event.mentioned && !event.followed && !m.watchesChannel(p.ChannelID) || m.reminders[p.ID].post.ID != "" {
			return nil
		}

		if event.title == "" {
			event.title = m.channelByID(p.ChannelID).DisplayName
		}

		if event.title == "" {
			event.title = "New message"
		}

		if event.team == "" {
			event.team = m.channelByID(p.ChannelID).TeamID
		}

		if event.team == "" && len(m.api.teamIDs) > 0 {
			event.team = m.api.teamIDs[0]
		}

		m.reminders[p.ID] = reminder{notificationEvent: event, due: now.Add(reminderInterval)}

		return m.desktopNotice(event, false)
	}

	return nil
}

func (m *app) desktopNotice(event notificationEvent, repeat bool) tea.Cmd {
	if m.notifier == "" {
		return nil
	}

	title, body := "Loop · "+event.title, event.body
	if repeat {
		title = "Loop reminder · " + event.title
	}

	runes := []rune(body)
	if len(runes) > 180 {
		body = string(runes[:180]) + "…"
	}

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
		defer cancel()

		err := exec.CommandContext(ctx, m.notifier, "--app-name=Loop TUI", "--", html.EscapeString(title), html.EscapeString(body)).Run()
		if err != nil {
			return notifyError{err}
		}

		return nil
	}
}

func (m *app) modeFor(item reminder) reminderMode {
	if m.watchesChannel(item.post.ChannelID) || item.followed && !item.mentioned {
		return untilRead
	}

	return m.preferences.MentionReminder
}

func (m *app) acknowledgeReaction(react reaction) {
	if item, ok := m.reminders[react.PostID]; ok && react.UserID == m.api.me.ID {
		mode := m.modeFor(item)
		if mode == untilReaction || mode == untilEyes && react.EmojiName == "eyes" {
			delete(m.reminders, react.PostID)
		}
	}
}

func (m *app) acknowledgeRead(channelID, rootID string, through int64) {
	for id, item := range m.reminders {
		if item.post.ChannelID == channelID && item.post.RootID == rootID && item.post.CreatedAt <= through {
			if m.modeFor(item) == untilRead {
				delete(m.reminders, id)
			} else {
				item.read = true
				m.reminders[id] = item
			}
		}
	}
}

func (m *app) checkReminders(now time.Time) tea.Cmd {
	if m.remindersChecking {
		return nil
	}

	var (
		due    []reminder
		checks remindersChecked
	)

	for _, item := range m.reminders {
		if !now.Before(item.due) {
			due = append(due, item)
			checks = append(checks, reminderCheck{id: item.post.ID, mode: m.modeFor(item)})
		}
	}

	if len(due) == 0 {
		return nil
	}

	m.remindersChecking = true

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 12*time.Second)
		defer cancel()

		type readScope struct{ channel, root string }

		type viewState struct {
			lastViewed int64
			err        error
		}

		viewed := make(map[readScope]viewState)

		for i, item := range due {
			check := &checks[i]
			if check.mode != untilRead {
				check.done, check.err = m.api.reminderReacted(ctx, item.post.ID, check.mode)
				continue
			}

			if item.read {
				check.done = true
				continue
			}

			scope := readScope{item.post.ChannelID, item.post.RootID}

			state, ok := viewed[scope]
			if !ok {
				state.lastViewed, state.err = m.api.reminderViewed(ctx, item)
				viewed[scope] = state
			}

			check.done, check.err = state.lastViewed >= item.post.CreatedAt, state.err
		}

		return checks
	}
}

func (c *client) reminderViewed(ctx context.Context, item reminder) (int64, error) {
	p := item.post
	user := url.PathEscape(c.me.ID)

	path := "/channels/" + url.PathEscape(p.ChannelID) + "/members/" + user
	if p.RootID != "" && item.team != "" {
		path = "/users/" + user + "/teams/" + url.PathEscape(item.team) + "/threads/" + url.PathEscape(p.RootID)
	}

	var state struct {
		LastViewed int64 `json:"last_viewed_at"`
	}

	err := c.request(ctx, http.MethodGet, path, nil, &state)
	if errors.Is(err, errNotFound) && p.RootID != "" {
		return 0, nil // An unfollowed thread can have no server read state.
	}

	return state.LastViewed, err
}

func (c *client) reminderReacted(ctx context.Context, id string, mode reminderMode) (bool, error) {
	var reactions []reaction

	path := "/posts/" + url.PathEscape(id) + "/reactions"

	err := c.request(ctx, http.MethodGet, path, nil, &reactions)
	if errors.Is(err, errNotFound) {
		return true, nil
	}

	return slices.ContainsFunc(reactions, func(react reaction) bool {
		return react.UserID == c.me.ID && (mode == untilReaction || react.EmojiName == "eyes")
	}), err
}

func (m *app) receiveReminderChecks(checks remindersChecked, now time.Time) tea.Cmd {
	m.remindersChecking = false

	var notifications []tea.Cmd

	for _, check := range checks {
		item, ok := m.reminders[check.id]
		if !ok || m.modeFor(item) != check.mode {
			continue
		}

		if check.done {
			delete(m.reminders, check.id)
			continue
		}

		if check.err == nil {
			item.due = item.due.Add(reminderInterval)
			if !item.due.After(now) {
				item.due = now.Add(reminderInterval)
			}

			notifications = append(notifications, m.desktopNotice(item.notificationEvent, true))
		} else {
			item.due = now.Add(5 * time.Second)
		}

		m.reminders[check.id] = item
	}

	return tea.Batch(notifications...)
}

func (m *app) watchesChannel(id string) bool {
	return m.api != nil && m.preferences.WatchedChannels[m.api.baseURL][id]
}

func (m *app) toggleChannelNotifications(id string) {
	if id == "" || m.api == nil {
		return
	}

	if m.preferences.WatchedChannels == nil {
		m.preferences.WatchedChannels = make(map[string]map[string]bool)
	}

	server := m.api.baseURL

	watched := m.preferences.WatchedChannels[server]
	if watched == nil {
		watched = make(map[string]bool)
		m.preferences.WatchedChannels[server] = watched
	}

	state := "on"

	if watched[id] {
		delete(watched, id)

		state = "off"

		for postID, item := range m.reminders {
			if item.post.ChannelID == id && !item.mentioned && !item.followed {
				delete(m.reminders, postID)
			}
		}
	} else {
		watched[id] = true
	}

	m.status = fmt.Sprintf("All-message alerts %s · %s", state, m.channelByID(id).DisplayName)
	m.savePreferences()
}
