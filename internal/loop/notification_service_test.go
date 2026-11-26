package loop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func TestNotificationOwnerIsExclusiveAndReleased(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	c := newClient("https://example.org", "token")
	c.me.ID = "me"

	lease, err := c.notificationLease()
	if err != nil || lease == nil {
		t.Fatalf("cannot acquire owner: %v", err)
	}

	duplicate, err := c.notificationLease()
	if err != nil || duplicate != nil {
		t.Fatalf("two notification owners: %v", err)
	}

	if err = lease.Close(); err != nil {
		t.Fatal(err)
	}

	next, err := c.notificationLease()
	if err != nil || next == nil {
		t.Fatalf("owner was not released: %v", err)
	}

	_ = next.Close()
	c.me.ID = "other-user"

	other, err := c.notificationLease()
	if err != nil || other == nil {
		t.Fatal("accounts share an ownership lock")
	}

	_ = other.Close()
}

func TestNotificationQueueAndCursorSurviveRestart(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)

	c := newClient("https://example.org", "secret-token")
	c.me.ID = "me"
	m := newApp(t.Context(), c)
	m.notificationStateName = c.notificationStateName()
	m.notifier = "/bin/true"
	m.Update(notificationEvent{kind: "posted", mentioned: true, post: post{ID: "post", ChannelID: "chat", UserID: "peer", Text: "hello", CreatedAt: 10}, cursor: notificationCursor{Connection: "socket", Sequence: 9}})

	path := filepath.Join(config, c.notificationStateName(), "settings.json")

	raw, err := os.ReadFile(path)
	if err != nil || strings.Contains(string(raw), "secret-token") {
		t.Fatalf("queue was not safely saved: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal("notification queue is not private")
	}

	m.background = true
	m.Update(notifyError{errors.New("desktop bus unavailable")})

	if m.notificationFailure == nil {
		t.Fatal("background delivery failure was hidden")
	}

	reopened := newApp(t.Context(), c)

	reopened.notificationStateName = c.notificationStateName()
	if err = reopened.restoreNotifications(); err != nil {
		t.Fatal(err)
	}

	if reopened.notificationCursor.Connection != "socket" || reopened.notificationCursor.Sequence != 9 || reopened.reminders["post"].post.Text != "hello" {
		t.Fatal("restart lost the cursor or pending message")
	}

	reopened.acknowledgeRead("chat", "", 10)

	if err = reopened.saveNotifications(); err != nil {
		t.Fatal(err)
	}

	final := newApp(t.Context(), c)

	final.notificationStateName = c.notificationStateName()
	if err = final.restoreNotifications(); err != nil || len(final.reminders) != 0 {
		t.Fatal("acknowledged message was restored")
	}
}

func TestRecoveryUsesUnreadCountsIncludingTeamMentionsAndThreads(t *testing.T) {
	c := newClient("https://example.org", "token")
	c.me.ID = "me"
	m := newApp(t.Context(), c)
	m.notificationStateName = "test"
	m.notifier = "/bin/true"
	m.channels = []channel{{ID: "chat", DisplayName: "Chat", Mentions: 1, LastViewed: 100, Threads: []threadSignal{{root: "thread", team: "team", unread: 2, lastViewed: 100}}}, {ID: "ordinary", Unread: 99}}

	now := time.Now()
	if m.recoverNotifications(now) == nil {
		t.Fatal("missed server activity did not produce notifications")
	}

	if len(m.reminders) != 2 || m.reminders["unread:channel:chat"].summary != 1 || m.reminders["unread:thread:thread"].summary != 2 {
		t.Fatalf("wrong recovered notifications: %+v", m.reminders)
	}

	if m.recoverNotifications(now) != nil {
		t.Fatal("unchanged unread state was notified twice")
	}

	m.channels = nil
	m.recoverNotifications(now)

	if len(m.reminders) != 0 {
		t.Fatal("read server scopes retained pending notifications")
	}
	// An older unreacted mention must not hide a newer unread mention.
	m.reminders["old"] = reminder{notificationEvent: notificationEvent{post: post{ID: "old", ChannelID: "chat", CreatedAt: 50}, mentioned: true}}

	m.channels = []channel{{ID: "chat", Mentions: 1, LastViewed: 100}}
	if m.recoverNotifications(now) == nil {
		t.Fatal("an old pending message hid a newer unread mention")
	}
}

func TestExpiredNotificationCursorReconnectsFresh(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("connection_id") != "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}

		defer func() { _ = conn.CloseNow() }()

		_ = wsjson.Write(t.Context(), conn, map[string]any{"event": "hello", "seq": 0, "data": map[string]string{"connection_id": "fresh"}})
	}))
	defer server.Close()

	c := newClient(server.URL, "token")
	cursor := notificationCursor{Connection: "expired", Sequence: 42}
	events := make(chan notificationEvent, 2)
	c.readNotificationEvents(t.Context(), events, &cursor)

	if cursor.Connection != "" {
		t.Fatal("expired connection cursor was retained")
	}

	c.readNotificationEvents(t.Context(), events, &cursor)

	if cursor.Connection != "fresh" || len(events) != 1 {
		t.Fatal("fresh notification connection was not established")
	}
}

func TestBackgroundNotificationsContinueWithoutUI(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	ctx, cancel := context.WithCancel(t.Context())
	later := make(chan struct{})

	var c *client

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v4/users/me":
			_, _ = fmt.Fprint(w, `{"id":"me","username":"me"}`)
		case "/api/v4/users/me/teams", "/api/v4/users/me/channels":
			_, _ = fmt.Fprint(w, `[]`)
		case "/api/v4/websocket":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Error(err)
				return
			}
			defer func() { _ = conn.CloseNow() }()

			_ = wsjson.Write(ctx, conn, map[string]any{"event": "hello", "seq": 0, "data": map[string]string{"connection_id": "socket"}})
			send := func(id string, sequence int) {
				body, _ := json.Marshal(post{ID: id, ChannelID: "chat", UserID: "peer", RootID: "root", CreatedAt: int64(sequence)})
				_ = wsjson.Write(ctx, conn, map[string]any{"event": "posted", "seq": sequence, "data": map[string]string{"post": string(body), "followers": `["me"]`}})
			}
			send("first", 1)

			select {
			case <-later:
				send("second", 2)
			case <-ctx.Done():
				return
			}

			_, _, _ = conn.Read(ctx)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	t.Cleanup(cancel)

	c = newClient(server.URL, "token")
	c.me.ID = "me"
	statePath := filepath.Join(config, c.notificationStateName(), "settings.json")
	trace := filepath.Join(t.TempDir(), "delivered")
	bin := t.TempDir()
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }

	script := fmt.Sprintf("#!/bin/sh\ntest -f %s || exit 5\nprintf 'sent\\n' >> %s\n", quote(statePath), quote(trace))
	if err := os.WriteFile(filepath.Join(bin, "notify-send"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	done := make(chan error, 1)
	go func() { done <- runClient(ctx, c, false, "dev", true) }()

	waitFor := func(count int) {
		t.Helper()

		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			data, _ := os.ReadFile(trace)
			if strings.Count(string(data), "sent\n") >= count {
				return
			}

			time.Sleep(10 * time.Millisecond)
		}

		t.Fatalf("background notification %d was not delivered", count)
	}
	waitFor(1)

	ui := newApp(ctx, c)
	ui.notifier = "/bin/true"

	lease, err := ui.startNotifications()
	if err != nil || lease != nil || ui.notificationEvents != nil {
		t.Fatalf("UI duplicated the background notification owner: %v", err)
	}
	// Discarding the UI does not own or cancel the notification service.
	close(later)
	waitFor(2)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("background client did not stop on cancellation")
	}
}
