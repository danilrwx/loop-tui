package loop

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestDiscussionSubscriptionsAndParticipation(t *testing.T) {
	var changes []string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v4/users/me/teams/team/threads":
			if r.URL.Query().Get("unread") != "" {
				t.Error("discussions should include already-read threads")
			}

			_, _ = fmt.Fprint(w, `{"threads":[{"post":{"id":"other","channel_id":"chat","user_id":"other","message":"Followed only"},"last_reply_at":100},{"post":{"id":"mine","channel_id":"chat","user_id":"other","message":"My discussion"},"participants":[{"id":"me"}],"unread_replies":3},{"post":{"id":"tag","channel_id":"chat","user_id":"other","message":"Mention-only discussion"},"unread_mentions":1}]}`)
		case "POST /api/v4/users/ids":
			_, _ = fmt.Fprint(w, `[{"id":"other","username":"colleague"}]`)
		case "PUT /api/v4/users/me/teams/team/threads/mine/following", "DELETE /api/v4/users/me/teams/team/threads/mine/following":
			changes = append(changes, r.Method)

			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	c := newClient(server.URL, "fixture")
	c.me.ID, c.teamIDs = "me", []string{"team"}
	m := newApp(t.Context(), c)
	m.width, m.height = 100, 24
	m.channels = []channel{{ID: "chat", TeamID: "team", DisplayName: "Chat"}}
	m.active, m.focus = "chat", channelPane
	m.timeline.posts = []post{{ID: "mine"}}
	key := func(r string) tea.Cmd { return m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(r)}) }
	m.Update(key("w")())
	key("h")

	m.channelCursor = discussionsID
	m.Update(m.key(tea.KeyMsg{Type: tea.KeyEnter})())

	if len(m.timeline.posts) != 3 {
		t.Fatal("expected every followed discussion")
	}

	key("f")

	if len(m.timeline.posts) != 2 || !strings.Contains(m.View(), "Discussions · Unread") || !strings.Contains(m.View(), "Mention-only discussion") {
		t.Fatal("unread discussions should include both replies and mentions")
	}

	key("f")

	if len(m.timeline.posts) != 1 || m.timeline.posts[0].ID != "mine" || !strings.Contains(m.View(), "↳ 3 new") {
		t.Fatal("participation should filter to the current user's own threads and replies")
	}

	m.Update(key("W")())

	if len(changes) != 2 || changes[0] != "PUT" || changes[1] != "DELETE" || len(m.timeline.posts) != 0 {
		t.Fatalf("unexpected subscription state: %v", changes)
	}
}

func TestDiscussionsSidebarOpensThreadInSourceChannel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v4/users/me/teams/team/threads":
			_, _ = fmt.Fprint(w, `{"threads":[{"post":{"id":"root","channel_id":"thread-chat","message":"Discussion root","create_at":10},"unread_replies":1}]}`)
		case "GET /api/v4/posts/root/thread":
			_, _ = fmt.Fprint(w, `{"order":["root","reply"],"posts":{"root":{"id":"root","channel_id":"thread-chat","message":"Discussion root","create_at":10},"reply":{"id":"reply","root_id":"root","channel_id":"thread-chat","message":"Reply in the discussion","create_at":20}}}`)
		case "PUT /api/v4/users/me/teams/team/threads/root/read/20":
			w.WriteHeader(http.StatusOK)
		case "POST /api/v4/posts":
			var sent struct {
				Channel string `json:"channel_id"`
				Root    string `json:"root_id"`
				Text    string `json:"message"`
			}
			if err := json.NewDecoder(r.Body).Decode(&sent); err != nil ||
				sent.Channel != "thread-chat" || sent.Root != "root" || sent.Text != "test reply" {
				t.Errorf("reply should be sent to the source channel and thread: %+v (%v)", sent, err)
			}

			w.WriteHeader(http.StatusCreated)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	c := newClient(server.URL, "fixture")
	c.me.ID, c.teamIDs = "me", []string{"team"}
	m := newApp(t.Context(), c)
	m.width, m.height = 120, 24
	m.channels = []channel{{ID: "home", DisplayName: "Home chat"}, {ID: "thread-chat", DisplayName: "Thread chat"}}
	m.channelCursor = discussionsID
	enter := tea.KeyMsg{Type: tea.KeyEnter}
	m.Update(m.key(enter)())

	if screen := m.View(); !strings.Contains(screen, "Home chat") || !strings.Contains(screen, "│ Discussion root") ||
		strings.Contains(screen, "[T]") || strings.Contains(screen, "i write") {
		t.Fatal("the discussions feed should use the ordinary sidebar and timeline")
	}

	m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	m.Update(m.key(enter)())

	if screen := m.View(); !strings.Contains(screen, "Thread · Thread chat") ||
		!strings.Contains(screen, "Reply in the discussion") || m.active != discussionsID ||
		m.draftKey() != (draftKey{channel: "thread-chat", root: "root"}) {
		t.Fatal("the discussion should use its source channel while the feed stays open")
	}

	m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	m.draft.SetValue("test reply")
	m.Update(m.key(enter)())
	m.key(tea.KeyMsg{Type: tea.KeyEsc})

	if m.active != discussionsID || m.focus != channelPane || m.root != "" || len(m.timeline.posts) != 0 {
		t.Fatal("closing the thread should return to the unread feed")
	}
}

func TestDiscussionPreviewMatchesChannelPost(t *testing.T) {
	m := newApp(t.Context(), newClient("", ""))
	p := post{
		ID: "root", ChannelID: "chat", UserID: "author", Author: "Alice", Replies: 2,
		CreatedAt: 1_700_000_000_000, Text: "## Build report\n\n**Ready** for review",
	}
	ch := channel{ID: "chat", DisplayName: "Channel", Threads: []threadSignal{{root: p.ID, unread: 2}}}
	m.channels = []channel{ch}
	m.active, m.focus = discussionsID, channelPane
	m.followed = []followedThread{{Post: p, Unread: 2}}
	m.applyDiscussionFilter()

	const width, height = 48, 12

	list := m.conversationPane(false, width, height)

	timeline := m.transcript("Channel", &conversation{posts: []post{p}}, width, height, true, true, ch)
	for _, line := range []string{"│ Build report", "│ Ready for review", "↳ 2 new  [t]"} {
		if !strings.Contains(list, line) || !strings.Contains(timeline, line) {
			t.Errorf("expected %q in both the discussion preview and the channel", line)
		}
	}

	if !strings.Contains(list, "Alice · Channel") {
		t.Error("discussion preview should show its source channel")
	}
}

func TestDiscussionCacheShowsEarlyPageAndSurvivesRefreshError(t *testing.T) {
	failed := false

	var limits []string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v4/users/me/teams/team/threads":
			if failed {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}

			limit := r.URL.Query().Get("per_page")
			limits = append(limits, limit)

			count := 41
			if limit == "40" {
				count = 40
			}

			items := make([]followedThread, count)
			for i := range items {
				items[i].Post = post{ID: fmt.Sprintf("thread-%d", i), UserID: "colleague", Text: "Test discussion"}
			}

			if err := json.NewEncoder(w).Encode(struct{ Threads []followedThread }{items}); err != nil {
				t.Error(err)
			}
		case "/api/v4/users/ids":
			_, _ = fmt.Fprint(w, `[{"id":"colleague","username":"Alice"}]`)
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	c := newClient(server.URL, "fixture")
	c.me.ID, c.teamIDs = "me", []string{"team"}

	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	cold := newApp(t.Context(), c)
	cold.discussionCachePath = c.discussionCachePath()
	cold.channelCursor = discussionsID

	_, more := cold.Update(cold.openChannel()())
	if len(cold.timeline.posts) != 40 || more == nil {
		t.Fatal("the first page should be usable while older discussions load")
	}

	cold.Update(more())

	if len(cold.timeline.posts) != 41 || cold.timeline.loading {
		t.Fatal("the remainder should replace the first-page snapshot")
	}

	warm := newApp(t.Context(), c)
	warm.discussionCachePath = cold.discussionCachePath
	warm.followed = readDiscussionCache(warm.discussionCachePath)
	warm.discussionsComplete = true
	warm.channelCursor = discussionsID

	refresh := warm.openChannel()
	if len(warm.timeline.posts) != 41 || warm.timeline.selectedPost().Author != "Alice" ||
		warm.followed[0].TeamID != "team" || refresh == nil {
		t.Fatal("opening the feed should display the cached discussions before the server responds")
	}

	_, continuation := warm.Update(refresh())
	if len(warm.timeline.posts) != 41 || continuation != nil || strings.Join(limits, ",") != "40,100,40" {
		t.Fatalf("warm refresh should keep older history and request just one page: %v", limits)
	}

	failed = true

	warm.Update(warm.loadFeed(false)())

	if len(warm.timeline.posts) != 41 || warm.timeline.loading {
		t.Fatal("a failed refresh should preserve the cache")
	}
}

func TestUnreadDiscussionBadgeAndAutomaticRefresh(t *testing.T) {
	m := newApp(t.Context(), newClient("https://example.org", "fixture"))
	m.width, m.height = 100, 24
	m.channels = []channel{{ID: "chat", Threads: []threadSignal{{root: "old", unread: 3}, {root: "other", unread: 1}}}}

	rows := m.sidebarItems()
	if rows[0].ID != discussionsID || rows[0].Unread != 2 || !strings.Contains(m.sidebar(40, 20), "Discussions") {
		t.Fatal("discussions must count unread threads even before opening the feed")
	}

	m.active, m.focus = discussionsID, channelPane
	m.Update(tickMsg(time.Unix(15, 0)))

	if !m.discussionsLoading {
		t.Fatal("the open discussions feed did not refresh automatically")
	}

	m.receiveDiscussions(discussionsResult{items: []followedThread{{Post: post{ID: "old", ChannelID: "chat"}, Unread: 3}}})
	m.feedFilter = 1
	m.applyDiscussionFilter()

	if len(m.timeline.posts) != 1 {
		t.Fatal("new replies missing from unread discussions")
	}

	m.clearThread("old")

	if m.sidebarItems()[0].Unread != 1 {
		t.Fatal("reading a thread did not reduce the discussion badge")
	}

	m.clearThread("other")
	m.syncDiscussionUnread()

	if len(m.timeline.posts) != 0 {
		t.Fatal("a server read snapshot left a cached discussion in the unread filter")
	}

	if m.sidebarItems()[0].hasUnread() {
		t.Fatal("read discussions retained the badge")
	}
}

func TestUnreadThreadShortcutFromEveryPane(t *testing.T) {
	for _, focus := range []focus{channelsPane, channelPane, threadPane} {
		t.Run(fmt.Sprint(focus), func(t *testing.T) {
			m := newApp(t.Context(), newClient("https://example.org", "fixture"))
			m.channels = []channel{{ID: "chat", TeamID: "team", Threads: []threadSignal{{root: "old-root", unread: 2, lastViewed: 10}}}}
			m.active, m.channelCursor, m.focus = "chat", "chat", focus

			m.timeline.posts = []post{{ID: "recent", ChannelID: "chat"}}
			if focus == threadPane {
				m.root, m.threadChannelID = "read-root", "chat"
			}

			cmd := m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
			if cmd == nil || m.root != "old-root" || m.threadChannelID != "chat" || m.focus != threadPane || m.discussion.readThrough != 10 {
				t.Fatal("n must open the unread thread even when its root is outside the loaded channel history")
			}
		})
	}
}

func TestUnreadShortcutFromDiscussions(t *testing.T) {
	for _, pane := range []focus{channelsPane, channelPane} {
		m := newApp(t.Context(), newClient("https://example.org", "fixture"))
		m.channels = []channel{{ID: "chat", Threads: []threadSignal{{root: "root", unread: 1}}}}

		m.active, m.channelCursor, m.focus = discussionsID, discussionsID, pane
		if m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")}) == nil || m.root != "root" || m.threadChannelID != "chat" {
			t.Fatal("n on Discussions must open an unread thread in its source channel")
		}
	}
}

func TestNextUnreadThreadSequenceAndReadFailure(t *testing.T) {
	for _, scope := range []string{"chat", discussionsID} {
		t.Run(scope, func(t *testing.T) {
			var failRead atomic.Bool
			failRead.Store(true)

			var marks atomic.Int32

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

				if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/thread") {
					parts := strings.Split(r.URL.Path, "/")
					root := parts[len(parts)-2]

					channel := "chat"
					if root == "third" {
						channel = "other"
					}

					response := postList{Order: []string{root, "reply"}, Posts: map[string]post{
						root:    {ID: root, ChannelID: channel, CreatedAt: 10},
						"reply": {ID: "reply", RootID: root, ChannelID: channel, CreatedAt: 20},
					}}
					if err := json.NewEncoder(w).Encode(response); err != nil {
						t.Error(err)
					}

					return
				}

				t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				w.WriteHeader(http.StatusNotFound)
			}))
			defer server.Close()

			api := newClient(server.URL, "fixture")
			api.me.ID = "me"
			m := newApp(t.Context(), api)
			m.active, m.focus = scope, channelPane
			m.channels = []channel{
				{ID: "chat", TeamID: "team", Threads: []threadSignal{{root: "first", team: "team", unread: 1}, {root: "second", team: "team", unread: 1}}},
				{ID: "other", TeamID: "team", Threads: []threadSignal{{root: "third", team: "team", unread: 1}}},
			}
			next := func() tea.Cmd { return m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")}) }
			first := next()

			request := m.discussion.request
			if next() != nil || m.discussion.request != request {
				t.Fatal("rapid n restarted an in-flight thread load")
			}

			m.Update(first())

			if len(m.channels[0].Threads) != 2 || !strings.Contains(m.status, "Mark thread as read:") {
				t.Fatal("failed acknowledgement dropped the first unread thread")
			}

			retry := next()
			if retry == nil || m.root != "first" {
				t.Fatal("failed acknowledgement was skipped")
			}

			m.Update(retry())

			if len(m.channels[0].Threads) != 1 {
				t.Fatal("successful retry did not advance the unread queue")
			}

			second := next()
			if second == nil || m.root != "second" {
				t.Fatal("second n did not open the second unread thread")
			}

			m.Update(second())
			// Returning to the channel/feed keeps the remaining unread scope intact.
			m.key(tea.KeyMsg{Type: tea.KeyEsc})

			if scope == discussionsID {
				third := next()
				if third == nil || m.root != "third" || m.threadChannelID != "other" {
					t.Fatal("discussions did not continue into the next channel")
				}

				m.Update(third())
			}

			if next() != nil {
				t.Fatal("n reopened a read thread after the unread queue was exhausted")
			}

			expected := int32(2)
			if scope == discussionsID {
				expected = 3
			}

			if marks.Load() != expected {
				t.Fatalf("unexpected number of acknowledged threads: %d", marks.Load())
			}

			if scope == "chat" && len(m.channels[1].Threads) != 1 {
				t.Fatal("channel shortcut consumed a thread from another channel")
			}
		})
	}
}
