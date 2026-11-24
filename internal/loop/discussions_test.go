package loop

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
