package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/danilrwx/telega/tui"
)

func TestChatStatusCountsChannels(t *testing.T) {
	channels := []channel{
		{},
		{ID: "read"},
		{ID: "ordinary", Unread: 20},
		{ID: "thread-replies", Threads: []threadSignal{{unread: 5}}},
		{ID: "channel-mentions", Mentions: 3},
		{ID: "thread-mentions", Threads: []threadSignal{{unread: 2, mentions: 2}, {unread: 5, mentions: 1}}},
		{ID: "both", Unread: 10, Mentions: 4, Threads: []threadSignal{{unread: 1, mentions: 1}}},
		{ID: "duplicate", Unread: 2},
		{ID: "duplicate", Mentions: 1},
		{ID: "duplicate", Unread: 1},
	}
	unread, mentions := chatStatusCounts(channels)
	if unread != 6 || mentions != 4 {
		t.Fatalf("got (%d, %d), want (6, 4)", unread, mentions)
	}
}

func TestChatStatusSidebarRefreshWhileHidden(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v4/users/me/channels":
			fmt.Fprint(w, `[{"id":"chat","total_msg_count":20,"total_msg_count_root":7}]`)
		case "GET /api/v4/users/me/teams/team/channels/members":
			fmt.Fprint(w, `[{"channel_id":"chat","msg_count":18,"msg_count_root":7}]`)
		case "GET /api/v4/users/me/teams/team/channels/categories":
			fmt.Fprint(w, `{"categories":[]}`)
		case "GET /api/v4/users/me/teams/team/threads":
			fmt.Fprint(w, `{"threads":[{"post":{"id":"root","channel_id":"chat"},"unread_replies":0,"unread_mentions":2,"last_viewed_at":1000}]}`)
		case "GET /api/v4/channels/chat/posts", "GET /api/v4/posts/root/thread":
			fmt.Fprint(w, threadFixture)
		case "POST /api/v4/users/ids":
			fmt.Fprint(w, `[]`)
		default:
			t.Errorf("unexpected API request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer s.Close()
	path := filepath.Join(t.TempDir(), "loop.status")
	t.Setenv("TUI_CHAT_STATUS_FILE", path)
	api := newClient(s.URL, "fixture")
	api.teamIDs = []string{"team"}
	m := newApp(t.Context(), api)
	m.chatStatus = tui.OpenChatStatus()
	t.Cleanup(m.chatStatus.Clear)

	m.Update(channelsResult{err: errors.New("not loaded")})
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("status should not exist before the first successful sidebar load, stat: %v", err)
	}

	// At narrow widths, focusing the sidebar hides both open conversations.
	m.active, m.root = "chat", "root"
	m.Update(tea.WindowSizeMsg{Width: 30, Height: 12})
	if layout := m.layout(); layout.channel != 0 || layout.thread != 0 {
		t.Fatalf("the open conversations should be hidden: %+v", layout)
	}
	_, cmd := m.Update(tickMsg(time.Unix(15, 0)))
	batch := cmd().(tea.BatchMsg)
	if len(batch) != 4 {
		t.Fatalf("got %d commands, want timer, sidebar, timeline and thread", len(batch))
	}
	// Skip the next timer; run the three refresh commands against the fixture.
	for _, refresh := range batch[1:] {
		msg := refresh()
		if history, ok := msg.(historyResult); ok && (history.err != nil || history.marked || history.markErr != nil) {
			t.Fatalf("unexpected history refresh result: %+v", history)
		}
		m.Update(msg)
	}
	ch := m.activeChannel()
	if signal := ch.threadSignal("root"); ch.Unread != 0 || ch.Mentions != 0 || signal.unread != 2 || signal.mentions != 2 || signal.lastViewed != 1000 {
		t.Fatalf("did not preserve thread-only mention metadata: %+v", ch)
	}
	assertStatus := func(want string) {
		t.Helper()
		if got, err := os.ReadFile(path); err != nil || string(got) != want {
			t.Fatalf("status: got %q, %v; want %q", got, err, want)
		}
	}
	assertStatus("1 1\n")
	m.Update(channelsResult{err: errors.New("offline")})
	assertStatus("1 1\n")
	m.Update(channelsResult{})
	assertStatus("0 0\n")

	// Message-action menus pause history refresh, not the status heartbeat.
	m.deleteTarget = post{ID: "root"}
	_, cmd = m.Update(tickMsg(time.Unix(30, 0)))
	batch = cmd().(tea.BatchMsg)
	if len(batch) != 2 {
		t.Fatalf("got %d commands during message action, want timer and sidebar", len(batch))
	}
	m.Update(batch[1]())
	assertStatus("1 1\n")
}
