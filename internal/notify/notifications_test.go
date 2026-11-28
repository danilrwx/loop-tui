package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func TestNotificationSocketDecodesServerMentionsAndResumes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/websocket" || r.Header.Get("Authorization") != "Bearer test" {
			t.Error("unexpected websocket path or authorization")
			http.Error(w, "Unexpected request", http.StatusBadRequest)

			return
		}

		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = conn.CloseNow() }()

		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()

		query := r.URL.Query()
		id, seq := "first", 2

		if query.Get("connection_id") != "" {
			if query.Get("connection_id") != "connection" || query.Get("sequence_number") != "3" {
				t.Errorf("unexpected resume cursor: %s", query.Encode())
			}

			id, seq = "second", 3
		} else {
			if err := wsjson.Write(ctx, conn, map[string]any{
				"event": "hello", "seq": 0, "data": map[string]string{"connection_id": "connection"},
			}); err != nil {
				t.Error(err)
				return
			}

			if err := wsjson.Write(ctx, conn, map[string]any{
				"event": "ignored", "seq": 1, "data": map[string]int{"post": 10},
			}); err != nil {
				t.Error(err)
				return
			}
		}

		body, _ := json.Marshal(post{ID: id, UserID: "peer", ChannelID: "chat", Text: "hello", CreatedAt: 1})
		if err := wsjson.Write(ctx, conn, map[string]any{
			"event": "posted", "seq": seq, "data": map[string]string{
				"post": string(body), "mentions": `["me"]`, "channel_display_name": "Duty",
			},
		}); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()

	c := newClient(server.URL, "test")
	c.me.ID = "me"
	events := make(chan notificationEvent, 2)

	var cursor notificationCursor

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	for _, id := range []string{"first", "second"} {
		c.readNotificationEvents(ctx, events, &cursor)

		received := false

		for len(events) > 0 {
			event := <-events
			if event.kind == "connected" {
				continue
			}

			if event.post.ID != id || !event.mentioned || event.title != "Duty" {
				t.Fatalf("unexpected notification: %+v", event)
			}

			received = true
		}

		if !received {
			t.Fatalf("expected event %s", id)
		}
	}
}

func TestRemindersStopOnTheConfiguredAcknowledgement(t *testing.T) {
	reads := map[string]int64{"duty/me": 100, "duty/thread": 0, "general/me": 100}
	reactions := []reaction{{UserID: "peer", EmojiName: "eyes"}, {UserID: "me", EmojiName: "thumbsup"}}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/reactions") {
			_ = json.NewEncoder(w).Encode(reactions)
			return
		}

		for key, value := range reads {
			channel, scope, _ := strings.Cut(key, "/")
			if r.URL.Path == "/api/v4/channels/"+channel+"/members/"+scope ||
				scope == "thread" && r.URL.Path == "/api/v4/users/me/teams/team/threads/"+scope {
				fmt.Fprintf(w, `{"last_viewed_at":%d}`, value)
				return
			}
		}

		t.Errorf("unexpected endpoint: %s", r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	c := newClient(server.URL, "test")
	c.me.ID = "me"
	m := newDaemon(t.Context(), c)
	m.notifier = "/bin/true"
	m.preferences.MentionReminder = untilEyes
	m.toggleChannelNotifications("duty")

	now := time.Unix(1, 0)

	postEvent := func(id, ch, root string, mentioned bool) notificationEvent {
		return notificationEvent{
			kind: "posted", mentioned: mentioned, team: "team",
			post: post{ID: id, ChannelID: ch, RootID: root, CreatedAt: 50},
		}
	}
	if m.receiveNotification(postEvent("ignored", "general", "", false), now) != nil {
		t.Fatal("unexpected alert for an ordinary message")
	}

	for _, event := range []notificationEvent{
		postEvent("mention", "general", "", true),
		postEvent("duty-root", "duty", "", true),
		postEvent("duty-reply", "duty", "thread", false),
	} {
		if m.receiveNotification(event, now) == nil || m.receiveNotification(event, now) != nil {
			t.Fatalf("expected a single initial notification for %s", event.post.ID)
		}
	}

	if m.checkReminders(now.Add(reminderInterval-time.Second)) != nil {
		t.Fatal("reminded before one minute")
	}

	runCheck := func(at time.Time) {
		t.Helper()

		cmd := m.checkReminders(at)
		if cmd == nil {
			t.Fatal("no check scheduled")
		}

		if m.receiveReminderChecks(cmd().(remindersChecked), at) == nil {
			t.Fatal("expected a repeat notification")
		}
	}
	runCheck(now.Add(reminderInterval))

	if _, ok := m.reminders["duty-root"]; ok {
		t.Fatal("duty alert should stop when read, including a mention")
	}

	if len(m.reminders) != 2 {
		t.Fatalf("expected the mention and the unread reply, got %+v", m.reminders)
	}

	m.acknowledgeRead("general", "", 50)
	m.acknowledgeReaction(reaction{PostID: "mention", UserID: "me", EmojiName: "thumbsup"})

	if len(m.reminders) != 2 {
		t.Fatal("Eyes mode must wait for the user's eyes reaction")
	}

	m.acknowledgeReaction(reaction{PostID: "mention", UserID: "me", EmojiName: "eyes"})
	m.toggleChannelNotifications("duty")

	if len(m.reminders) != 0 {
		t.Fatalf("acknowledging the mention and disabling duty alerts should stop both, got %+v", m.reminders)
	}

	m.preferences.MentionReminder = untilReaction
	m.receiveNotification(postEvent("second-mention", "general", "", true), now)
	cmd := m.checkReminders(now.Add(reminderInterval))
	m.receiveReminderChecks(cmd().(remindersChecked), now.Add(reminderInterval))

	if len(m.reminders) != 0 {
		t.Fatal("a reaction from the same account in another client should stop the reminder")
	}
}

func TestNotificationsIncludeFollowedThreadsAndServerMentionRecipients(t *testing.T) {
	for _, scenario := range []struct {
		name, text, root            string
		mentions, followers         any
		mentioned, followed, notify bool
	}{
		{"personal mention", "@my-username", "", `["me"]`, nil, true, false, true},
		{"team mention", "@my-team", "", `["me"]`, nil, true, false, true},
		{"custom mention keyword", "virtualization-oncall", "", []string{"me"}, nil, true, false, true},
		{"followed thread", "new reply", "thread", nil, `["other","me"]`, false, true, true},
		{"followed and mentioned", "@me new reply", "thread", `["me"]`, `["me"]`, true, true, true},
		{"followed thread array", "new reply", "thread", nil, []string{"me"}, false, true, true},
		{"another user's thread", "new reply", "thread", nil, `["other"]`, false, false, false},
		{"not a reply", "new root post", "", nil, `["me"]`, false, false, false},
		{"unrecognized text mention", "@someone-else", "", nil, nil, false, false, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			postJSON, _ := json.Marshal(post{ID: "message", UserID: "peer", ChannelID: "chat", Text: scenario.text, RootID: scenario.root, CreatedAt: 50})
			data, _ := json.Marshal(map[string]any{"post": string(postJSON), "mentions": scenario.mentions, "followers": scenario.followers, "team_id": "team"})

			event, ok := (notificationWire{Event: "posted", Data: data}).notification("me")
			if !ok || event.mentioned != scenario.mentioned || event.followed != scenario.followed {
				t.Fatalf("wrong notification recipients: %+v, decoded %t", event, ok)
			}

			c := newClient("https://example.org", "test")
			c.me.ID = "me"
			m := newDaemon(t.Context(), c)
			m.notifier = "/bin/true"
			m.preferences.MentionReminder = untilEyes

			cmd := m.receiveNotification(event, time.Unix(1, 0))
			if (cmd != nil) != scenario.notify {
				t.Fatalf("notification scheduled=%t, want %t", cmd != nil, scenario.notify)
			}

			if scenario.followed && scenario.mentioned && m.modeFor(m.reminders["message"]) != untilEyes {
				t.Fatal("thread subscriptions must preserve explicit mention reminder settings")
			}

			if scenario.followed && !scenario.mentioned {
				item := m.reminders["message"]
				if m.modeFor(item) != untilRead {
					t.Fatal("followed thread reminders must stop when read")
				}

				m.acknowledgeRead("chat", "thread", 50)

				if len(m.reminders) != 0 {
					t.Fatal("reading the followed thread did not stop its reminder")
				}
			}
		})
	}
}

func TestDisablingChannelAlertsKeepsThreadSubscriptions(t *testing.T) {
	c := newClient("https://example.org", "test")
	m := newDaemon(t.Context(), c)
	m.notifier = "/bin/true"
	m.toggleChannelNotifications("chat")
	m.receiveNotification(notificationEvent{kind: "posted", followed: true, post: post{ID: "reply", ChannelID: "chat", RootID: "thread", CreatedAt: 1}}, time.Now())
	m.toggleChannelNotifications("chat")

	if _, ok := m.reminders["reply"]; !ok {
		t.Fatal("channel-wide alerts must not disable notifications for followed threads")
	}
}

func TestThreadRemindersRespectReadAndFollowState(t *testing.T) {
	for _, scenario := range []struct {
		name, state string
		status      int
		mentioned   bool
		watched     bool
		deleted     bool
		keep        bool
	}{
		{name: "unread followed thread", state: `{"last_viewed_at":0,"is_following":true}`, keep: true},
		{name: "read followed thread", state: `{"last_viewed_at":50,"is_following":true}`},
		{name: "unfollowed thread", state: `{"last_viewed_at":0,"is_following":false}`},
		{name: "missing unfollowed thread", status: http.StatusNotFound},
		{name: "missing mentioned thread", status: http.StatusNotFound, mentioned: true, keep: true},
		{name: "unfollowed mentioned thread", state: `{"last_viewed_at":0,"is_following":false}`, mentioned: true, keep: true},
		{name: "unfollowed watched channel", state: `{"last_viewed_at":0,"is_following":false}`, watched: true, keep: true},
		{name: "deleted reply", status: http.StatusNotFound, deleted: true},
		{name: "server failure", status: http.StatusInternalServerError, keep: true},
		{name: "omitted follow state", state: `{"last_viewed_at":0}`, keep: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v4/users/me/teams/team/threads/thread":
					if scenario.status != 0 {
						w.WriteHeader(scenario.status)
						return
					}

					_, _ = fmt.Fprint(w, scenario.state)
				case "/api/v4/posts/reply":
					if scenario.deleted {
						w.WriteHeader(http.StatusNotFound)
						return
					}

					_, _ = fmt.Fprint(w, `{"id":"reply"}`)
				default:
					t.Errorf("unexpected endpoint: %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()

			c := newClient(server.URL, "test")
			c.me.ID = "me"
			m := newDaemon(t.Context(), c)

			m.notifier = "/bin/true"
			if scenario.watched {
				m.toggleChannelNotifications("chat")
			}

			now := time.Now()
			m.receiveNotification(notificationEvent{
				kind: "posted", followed: true, mentioned: scenario.mentioned, team: "team",
				post: post{ID: "reply", ChannelID: "chat", RootID: "thread", CreatedAt: 50},
			}, now)
			at := now.Add(reminderInterval)
			checks := m.checkReminders(at)().(remindersChecked)

			repeat := m.receiveReminderChecks(checks, at)
			if _, kept := m.reminders["reply"]; kept != scenario.keep {
				t.Fatalf("reminder retained: %v, want %v", kept, scenario.keep)
			}

			if repeat != nil && (!scenario.keep || scenario.status == http.StatusInternalServerError) {
				t.Fatal("unexpected repeat notification")
			}
		})
	}
}

func (m *daemon) toggleChannelNotifications(id string) {
	if m.preferences.WatchedChannels == nil {
		m.preferences.WatchedChannels = make(map[string]map[string]bool)
	}

	watched := m.preferences.WatchedChannels[m.api.baseURL]
	if watched == nil {
		watched = make(map[string]bool)
		m.preferences.WatchedChannels[m.api.baseURL] = watched
	}

	if watched[id] {
		delete(watched, id)

		for key, item := range m.reminders {
			if item.post.ChannelID == id && !item.followed && !item.mentioned {
				delete(m.reminders, key)
			}
		}
	} else {
		watched[id] = true
	}
}

func TestQueuedNotificationKeepsOnlyShortExcerpts(t *testing.T) {
	text := strings.Repeat("я", 10000)
	postJSON, _ := json.Marshal(post{ID: "message", UserID: "peer", ChannelID: "chat", Text: text, CreatedAt: 1})
	data, _ := json.Marshal(map[string]string{"post": string(postJSON), "mentions": `["me"]`})

	event, ok := (notificationWire{Event: "posted", Data: data}).notification("me")
	if !ok || len([]rune(event.body)) != 180 || len([]rune(event.post.Text)) != 180 {
		t.Fatal("queued notification retained a complete message")
	}
}
