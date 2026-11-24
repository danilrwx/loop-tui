package loop

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
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

		select {
		case event := <-events:
			if event.post.ID != id || !event.mentioned || event.title != "Duty" {
				t.Fatalf("unexpected notification: %+v", event)
			}
		default:
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
	m := newApp(t.Context(), c)
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

func TestNotificationSettingsPersist(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	c := newClient("https://example.org", "test")
	m := newApp(t.Context(), c)
	m.loadPreferences()
	m.channels = []channel{{ID: "duty", DisplayName: "Duty"}, {ID: "general", DisplayName: "General"}}
	m.channelCursor = "duty"
	m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("N")})
	m.settingsCursor = 3
	m.settingsKey(tea.KeyMsg{Type: tea.KeyEnter})

	reopened := newApp(t.Context(), c)
	reopened.loadPreferences()

	if !reopened.watchesChannel("duty") || reopened.watchesChannel("general") || reopened.preferences.MentionReminder != untilEyes {
		t.Fatalf("unexpected notification settings after restart: %+v", reopened.preferences)
	}
}
