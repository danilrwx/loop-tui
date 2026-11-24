package loop

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/danilrwx/telega/tui"
)

func TestPagingMovesIntoFetchedHistory(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/channels/chat/posts" || r.URL.Query().Get("before") != "oldest" {
			t.Errorf("wrong history request: %s", r.URL)
			w.WriteHeader(http.StatusNotFound)

			return
		}

		page := postList{Posts: make(map[string]post)}

		for i := range 6 {
			id := fmt.Sprintf("older-%d", i)
			page.Order = append(page.Order, id)
			page.Posts[id] = post{ID: id, CreatedAt: 100 - int64(i)}
		}

		if err := json.NewEncoder(w).Encode(page); err != nil {
			t.Error(err)
		}
	}))
	defer backend.Close()

	model := newApp(t.Context(), newClient(backend.URL, "fixture"))
	model.height, model.active, model.focus = 29, "chat", channelPane
	model.timeline = conversation{
		posts:    []post{{ID: "newest", CreatedAt: 200}, {ID: "oldest", CreatedAt: 150}},
		selected: 1, before: "oldest",
	}

	load := model.key(tea.KeyMsg{Type: tea.KeyCtrlU})
	if load == nil {
		t.Fatal("Ctrl+U did not request the older page")
	}

	model.Update(load())

	if got := model.timeline.selectedPost().ID; got != "older-3" {
		t.Fatalf("Ctrl+U stayed on the previous page: %s", got)
	}

	model.key(tea.KeyMsg{Type: tea.KeyCtrlD})

	if got := model.timeline.selectedPost().ID; got != "oldest" {
		t.Fatalf("Ctrl+D did not return to the newer page: %s", got)
	}
}

func TestSidebarShowsUnreadBelow(t *testing.T) {
	model := newApp(t.Context(), nil)
	model.channels = []channel{
		{ID: "fav", DisplayName: "Favorite", Favorite: true},
		{ID: "dm", DisplayName: "Read direct", Direct: true},
		{ID: "unread-dm", DisplayName: "Unread direct", Direct: true, Unread: 1},
		{ID: "channel", DisplayName: "Read channel"},
		{ID: "unread-channel", DisplayName: "Unread channel", Unread: 2},
	}
	model.ensureChannelCursor()

	if got := model.sidebar(32, 5); !strings.Contains(got, "↓ 2 unread chats") {
		t.Fatalf("offscreen unread chats were not shown: %q", got)
	}

	model.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})

	if got := model.sidebar(32, 12); !strings.Contains(got, "Read direct") ||
		strings.Contains(got, "╵ Read channel ") || !strings.Contains(got, "Unread channel") {
		t.Fatalf("read-channel filter should keep direct chats: %q", got)
	}
}

func TestSidebarSettingsAreIndependentAndPersistent(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	model := newApp(t.Context(), nil)
	model.loadPreferences()
	model.width, model.height, model.active = 100, 24, "channel"

	model.channels = []channel{
		{ID: "channel", DisplayName: "Read channel"},
		{ID: "dm", DisplayName: "Read direct", Direct: true},
		{ID: "unread", DisplayName: "Unread direct", Direct: true, Unread: 1},
	}
	if got := model.sidebar(35, 10); strings.Contains(got, "Read direct") || !strings.Contains(got, "Unread direct") {
		t.Fatalf("direct messages should default to unread only: %q", got)
	}

	model.query.SetValue("Read direct")

	if got := model.sidebar(35, 10); !strings.Contains(got, "Read direct") {
		t.Fatal("search should find read direct messages")
	}

	model.query.Reset()
	model.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("S")})
	model.key(tea.KeyMsg{Type: tea.KeyRight})
	model.key(tea.KeyMsg{Type: tea.KeyDown})
	model.key(tea.KeyMsg{Type: tea.KeyEnter})
	model.key(tea.KeyMsg{Type: tea.KeyDown})
	model.key(tea.KeyMsg{Type: tea.KeyEnter})
	model.key(tea.KeyMsg{Type: tea.KeyEsc})

	if got := model.layout().sidebar; got != 31 {
		t.Fatalf("settings did not resize the visible sidebar: %d", got)
	}

	model.focus = channelPane
	model.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("S")})

	for range settingCount - 1 {
		model.key(tea.KeyMsg{Type: tea.KeyDown})
	}

	model.key(tea.KeyMsg{Type: tea.KeyEnter})
	model.key(tea.KeyMsg{Type: tea.KeyEsc})

	if got := model.layout(); got != (paneLayout{channel: model.width}) {
		t.Fatalf("open chat should fill the terminal: %+v", got)
	}

	model.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("h")})

	if got := model.layout(); got != (paneLayout{sidebar: model.width}) {
		t.Fatalf("returning to the chat list should show it at full width: %+v", got)
	}

	model.key(tea.KeyMsg{Type: tea.KeyEnter})

	if got := model.layout(); got != (paneLayout{channel: model.width}) {
		t.Fatalf("reopening the chat should hide the list again: %+v", got)
	}

	reopened := newApp(t.Context(), nil)
	reopened.loadPreferences()

	if reopened.layoutSettings.SidebarWidth != 31 ||
		!reopened.preferences.HideReadChannels || reopened.preferences.HideReadDirect || !reopened.preferences.HideChatListWhenOpen {
		t.Fatalf("settings did not survive reopening: %+v, %+v", reopened.layoutSettings, reopened.preferences)
	}
}

func TestMentionAndDraftInChatPane(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if r.URL.Path != "/api/v4/users/autocomplete" ||
			query.Get("name") != "ми" || query.Get("in_channel") != "chat" || query.Get("in_team") != "team" {
			t.Errorf("wrong mention request: %s", r.URL)
			w.WriteHeader(http.StatusNotFound)

			return
		}

		_, _ = fmt.Fprint(w, `{"users":[{"id":"person","username":"mira","first_name":"Мира"}]}`)
	}))
	defer backend.Close()

	t.Setenv("XDG_STATE_HOME", t.TempDir())

	store, err := tui.OpenDraftStore("loop-tui")
	if err != nil {
		t.Fatal(err)
	}

	api := newClient(backend.URL, "fixture")
	api.me.ID = "me"
	makeModel := func() *app {
		m := newApp(t.Context(), api)
		m.draftStore = store
		m.active, m.focus = "chat", channelPane
		m.channels = []channel{{ID: "chat", DisplayName: "Fixture chat", TeamID: "team"}}
		m.Update(tea.WindowSizeMsg{Width: 100, Height: 22})

		return m
	}

	model := makeModel()
	if got := model.View(); !strings.Contains(got, "─ Message ") {
		t.Fatalf("the idle composer is missing: %q", got)
	}

	model.compose()
	model.draft.SetValue("first\nПривет @миxxx, остальное")
	model.draft.SetCursor(len([]rune("Привет @ми")))

	if model.refreshMention() == nil {
		t.Fatal("no autocomplete was scheduled")
	}

	model.Update(model.queryMention(model.mentionID)())

	if got := model.View(); !strings.Contains(got, "@mira · Мира") {
		t.Fatalf("no mention choices in composer: %q", got)
	}

	model.key(tea.KeyMsg{Type: tea.KeyTab})

	want := "first\nПривет @mira, остальное"
	if model.draft.Value() != want {
		t.Fatalf("mention insertion replaced unrelated text: %q", model.draft.Value())
	}

	model.key(tea.KeyMsg{Type: tea.KeyEsc})

	if model.composing || !strings.Contains(model.View(), "first") {
		t.Fatal("paused draft is not visible in the conversation")
	}

	reopened := makeModel()
	reopened.compose()

	if reopened.draft.Value() != want || reopened.restoreDraft(draftKey{channel: "chat", root: "thread"}) != "" {
		t.Fatal("draft should survive restart and stay scoped to its chat")
	}

	reopened.Update(sendResult{})

	afterSend := makeModel()
	if afterSend.restoreDraft(afterSend.draftKey()) != "" {
		t.Fatal("completed draft should be removed")
	}
}

func TestComposerGrowsToHalfTheWindow(t *testing.T) {
	model := newApp(t.Context(), nil)
	model.active, model.focus = "chat", channelPane
	model.Update(tea.WindowSizeMsg{Width: 32, Height: 20})
	model.compose()

	if got := model.draft.Height(); got != 1 {
		t.Fatalf("empty composer has %d rows", got)
	}

	model.setDraft("first\nsecond")

	if got := model.draft.Height(); got != 2 {
		t.Fatalf("two lines gave %d rows", got)
	}

	model.setDraft(strings.Repeat("wrap words ", 45) + "last marker")

	if got := model.draft.Height(); got != tui.ComposerLineLimit(20) {
		t.Fatalf("long draft gave %d rows", got)
	}

	if got := model.draft.View(); !strings.Contains(got, "last marker") {
		t.Fatalf("the caret end of the long draft is outside the viewport: %q", got)
	}

	model.setDraft("short")

	if got := model.draft.Height(); got != 1 {
		t.Fatalf("composer did not shrink: %d rows", got)
	}
}

func TestSidebarGroupModesCycleIndependently(t *testing.T) {
	m := newApp(t.Context(), nil)

	m.channels = []channel{
		{ID: "fav", DisplayName: "Pinned channel", Favorite: true},
		{ID: "fav-dm", DisplayName: "Pinned DM", Favorite: true, Direct: true},
		{ID: "read", DisplayName: "Quiet channel"},
		{ID: "unread", DisplayName: "Busy channel", Unread: 1},
		{ID: "read-dm", DisplayName: "Quiet DM", Direct: true},
		{ID: "unread-dm", DisplayName: "Busy DM", Direct: true, Unread: 1},
	}
	for _, step := range []struct {
		key                       rune
		unreadChannels, unreadDMs bool
	}{
		{'c', true, false},
		{'d', true, true},
		{'c', false, true},
		{'d', false, false},
	} {
		m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{step.key}})

		got := m.sidebar(32, 12)
		if !strings.Contains(got, "Pinned channel") || !strings.Contains(got, "Pinned DM") ||
			strings.Contains(got, "Quiet channel") == step.unreadChannels ||
			strings.Contains(got, "Quiet DM") == step.unreadDMs ||
			!strings.Contains(got, "Busy channel") || !strings.Contains(got, "Busy DM") ||
			!strings.Contains(got, "▾ Channels") || !strings.Contains(got, "▾ Direct messages") ||
			strings.Contains(got, "· Unread") {
			t.Fatalf("after %c with unread channels %t and unread DMs %t:\n%s", step.key, step.unreadChannels, step.unreadDMs, got)
		}
	}

	m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})

	m.channels[3].Unread = 0
	if got := m.sidebar(32, 12); !strings.Contains(got, "▾ Channels") || strings.Contains(got, "Quiet channel") {
		t.Fatalf("empty unread group did not keep its header:\n%s", got)
	}

	m.query.SetValue("Quiet channel")

	if got := m.filteredChannels(); len(got) != 1 || got[0].ID != "read" {
		t.Fatalf("search did not reveal a read channel: %+v", got)
	}
}

func TestSidebarLoadsOldCollapsedGroupAsUnread(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if err := tui.WriteSettings("loop-tui", map[string]bool{
		"collapse_channels": true, "hide_read_channels": false, "hide_read_direct_messages": false,
	}); err != nil {
		t.Fatal(err)
	}

	m := newApp(t.Context(), nil)
	m.loadPreferences()

	if !m.preferences.HideReadChannels || m.preferences.HideReadDirect {
		t.Fatalf("unexpected modes after loading collapsed settings: %+v", m.preferences)
	}
}

func TestUnreadSidebarRetainsOpenChannelAndDirectMessage(t *testing.T) {
	m := newApp(t.Context(), nil)
	m.preferences.HideReadChannels, m.preferences.HideReadDirect = true, true
	m.channels = []channel{
		{ID: "channel", DisplayName: "Open channel", Unread: 1},
		{ID: "dm", DisplayName: "Open direct", Direct: true, Unread: 1},
		{ID: "read", DisplayName: "Already read"},
	}
	m.channelCursor = "channel"
	m.openChannel()
	m.setRead("channel")

	if got := m.sidebar(40, 12); !strings.Contains(got, "› Open channel") || strings.Contains(got, "Already read") {
		t.Fatalf("the open channel should stay visible after being marked read:\n%s", got)
	}

	m.channelCursor = "dm"
	m.openChannel()
	m.setRead("dm")

	if got := m.sidebar(40, 12); !strings.Contains(got, "› Open direct") || strings.Contains(got, "Open channel") {
		t.Fatalf("only the newly opened conversation should stay visible when both are read:\n%s", got)
	}
}
