package loop

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestManualUnreadNavigationListAndExplicitAcknowledgement(t *testing.T) {
	var (
		marks    atomic.Int32
		failRead atomic.Bool
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/read/") {
			if failRead.Swap(false) {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}

			marks.Add(1)
			w.WriteHeader(http.StatusOK)

			return
		}

		if strings.HasSuffix(r.URL.Path, "/thread") {
			parts := strings.Split(r.URL.Path, "/")
			root := parts[len(parts)-2]

			response := postList{Order: []string{root, "reply"}, Posts: map[string]post{root: {ID: root, ChannelID: "chat", CreatedAt: 10}, "reply": {ID: "reply", ChannelID: "chat", RootID: root, CreatedAt: 20}}}
			if err := json.NewEncoder(w).Encode(response); err != nil {
				t.Error(err)
			}

			return
		}

		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	m := newApp(t.Context(), newClient(server.URL, "fixture"))
	m.width, m.height = 110, 30
	m.active, m.focus = "chat", channelPane
	m.preferences.ManualRead = true
	m.channels = []channel{{ID: "chat", DisplayName: "Chat", TeamID: "team", Threads: []threadSignal{{root: "first", team: "team", preview: "First discussion", unread: 1, lastViewed: 10}, {root: "second", team: "team", preview: "Second discussion", unread: 2, lastViewed: 10}}}, {ID: "other", Threads: []threadSignal{{root: "third", unread: 1}}}}

	key := func(name string) tea.Cmd { return m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(name)}) }
	for _, root := range []string{"first", "second", "first"} {
		cmd := key("n")
		if cmd == nil || m.root != root {
			t.Fatalf("manual n did not cycle to %s", root)
		}

		m.Update(cmd())

		if marks.Load() != 0 || len(m.channels[0].Threads) != 2 {
			t.Fatal("opening a thread marked it read in manual mode")
		}
	}

	key("u")

	if len(m.unreadChoices) != 2 || !strings.Contains(m.View(), "Unread threads") || !strings.Contains(m.View(), "Second discussion") {
		t.Fatal("unread picker omitted a thread or its preview")
	}

	m.key(tea.KeyMsg{Type: tea.KeyEsc})

	if m.unreadChoices != nil || marks.Load() != 0 {
		t.Fatal("dismissing the list changed read state")
	}

	key("u")
	key("j")
	m.Update(m.key(tea.KeyMsg{Type: tea.KeyEnter})())

	if m.root != "second" || marks.Load() != 0 {
		t.Fatal("opening from the list did not preserve unread state")
	}

	before := m.discussion.readThrough

	failRead.Store(true)
	m.Update(key("m")())

	if len(m.channels[0].Threads) != 2 || m.discussion.readThrough != before {
		t.Fatal("failed m optimistically marked the thread read")
	}

	m.Update(key("m")())

	if marks.Load() != 1 || len(m.channels[0].Threads) != 1 || m.channels[0].Threads[0].root != "first" || m.discussion.readThrough != 20 {
		t.Fatal("explicit m did not acknowledge only the selected thread")
	}
	// A sidebar request started before m must not put the read thread back.
	_, refresh := m.Update(channelsResult{revision: 0, items: []channel{{ID: "chat", Threads: []threadSignal{{root: "second", unread: 2}}}}})
	if refresh == nil || len(m.channels[0].Threads) != 1 || m.channels[0].Threads[0].root != "first" {
		t.Fatal("stale sidebar snapshot restored the acknowledged thread")
	}
}

func TestManualReadSettingPersistsWithoutChangingExistingDefaults(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m := newApp(t.Context(), newClient("https://example.org", "fixture"))
	m.loadPreferences()

	if m.preferences.ManualRead {
		t.Fatal("existing users' automatic read mode changed")
	}

	m.settingsCursor = 7
	m.settingsKey(tea.KeyMsg{Type: tea.KeyEnter})
	reopened := newApp(t.Context(), m.api)
	reopened.loadPreferences()

	if !reopened.preferences.ManualRead {
		t.Fatal("manual read setting did not survive restart")
	}

	reopened.settingsCursor = 7
	reopened.settingsKey(tea.KeyMsg{Type: tea.KeyEnter})
	m.loadPreferences()

	if m.preferences.ManualRead {
		t.Fatal("automatic read mode could not be restored")
	}
}

func TestManualOpenKeepsChannelUnreadUntilM(t *testing.T) {
	var marks atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v4/channels/chat/posts":
			if err := json.NewEncoder(w).Encode(postList{Order: []string{"root"}, Posts: map[string]post{"root": {ID: "root", ChannelID: "chat", CreatedAt: 10}}}); err != nil {
				t.Error(err)
			}
		case "POST /api/v4/channels/members/me/view":
			marks.Add(1)
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	api := newClient(server.URL, "fixture")
	api.me.ID = "me"
	m := newApp(t.Context(), api)
	m.preferences.ManualRead = true
	m.channelCursor = "chat"
	m.channels = []channel{{ID: "chat", Unread: 1}}
	m.Update(m.openChannel()())

	if marks.Load() != 0 || m.channels[0].Unread != 1 {
		t.Fatal("manual channel opening sent an automatic read acknowledgement")
	}

	m.Update(m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("m")})())

	if marks.Load() != 1 || m.channels[0].Unread != 0 {
		t.Fatal("explicit m did not mark the channel read")
	}
}

func TestNextUnreadContinuesAfterMarkingTheMiddleThread(t *testing.T) {
	m := newApp(t.Context(), newClient("https://example.org", "fixture"))
	m.active, m.focus = "chat", channelPane
	m.preferences.ManualRead = true
	m.channels = []channel{{ID: "chat", Threads: []threadSignal{{root: "first", unread: 1}, {root: "second", unread: 1}, {root: "third", unread: 1}}}}
	m.openUnreadChoice(m.unreadThreadChoices()[1])
	m.Update(readResult{channelID: "chat", rootID: "second", through: 10})

	if m.openUnreadThread() == nil || m.root != "third" {
		t.Fatal("marking the middle thread restarted navigation at the first unread thread")
	}
}
