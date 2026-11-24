package loop

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestChannelThreadWorkflow(t *testing.T) {
	var (
		sent       map[string]string
		viewed     string
		threadRead bool
	)

	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		switch r.Method + " " + r.URL.Path {
		case "GET /api/v4/users/me":
			_, _ = fmt.Fprint(w, `{"id":"me","username":"mine"}`)
		case "GET /api/v4/users/me/teams":
			_, _ = fmt.Fprint(w, `[{"id":"team","display_name":"Team"}]`)
		case "GET /api/v4/users/me/channels":
			_, _ = fmt.Fprint(w, `[{"id":"chat","name":"town","display_name":"Town","last_post_at":2000,"total_msg_count":7},
			{"id":"dm","name":"me__peer","type":"D","last_post_at":3000},
			{"id":"other","name":"elsewhere","display_name":"Elsewhere","last_post_at":4000}]`)
		case "GET /api/v4/users/me/teams/team/channels/members":
			_, _ = fmt.Fprint(w, `[{"channel_id":"chat","msg_count":5,"mention_count":1,"last_viewed_at":500}]`)
		case "GET /api/v4/users/me/teams/team/threads":
			_, _ = fmt.Fprint(w, `{"threads":[{"post":{"id":"root","channel_id":"chat"},"unread_replies":1,"last_viewed_at":1000}]}`)
		case "GET /api/v4/users/me/teams/team/channels/categories":
			_, _ = fmt.Fprint(w, `{"categories":[
				{"type":"custom","channel_ids":["other"]},
				{"type":"favorites","channel_ids":["chat","dm"]}]}`)
		case "POST /api/v4/users/ids":
			_, _ = fmt.Fprint(w, `[{"id":"peer","first_name":"Mira","last_name":"Chen"}]`)
		case "GET /api/v4/channels/chat/posts":
			if r.URL.Query().Get("collapsedThreads") != "true" {
				t.Error("request did not ask for collapsed threads")
			}

			_, _ = fmt.Fprint(w, threadFixture)
		case "POST /api/v4/channels/members/me/view":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}

			viewed, _ = body["channel_id"].(string)
			if body["collapsed_threads_supported"] != true {
				t.Error("viewing the channel should leave followed threads unread")
			}
		case "GET /api/v4/posts/root/thread":
			_, _ = fmt.Fprint(w, threadFixture)
		case "PUT /api/v4/users/me/teams/team/threads/root/read/2000":
			threadRead = true
		case "GET /api/v4/files/file1/info":
			_, _ = fmt.Fprint(w, `{"id":"file1","name":"../../notes.txt","mime_type":"text/plain"}`)
		case "GET /api/v4/files/file1":
			_, _ = fmt.Fprint(w, "test attachment")
		case "POST /api/v4/posts":
			if err := json.NewDecoder(r.Body).Decode(&sent); err != nil {
				t.Error(err)
			}

			w.WriteHeader(http.StatusCreated)
		default:
			t.Errorf("unexpected API request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer s.Close()

	api := newClient(s.URL, "fixture")
	if err := api.connect(t.Context()); err != nil {
		t.Fatal(err)
	}

	m := newApp(t.Context(), api)
	m.Update(tea.WindowSizeMsg{Width: 124, Height: 25})
	m.Update(m.loadChannels()())

	if len(m.channels) != 3 || m.channels[0].ID != "chat" || m.channels[0].Unread != 2 ||
		m.channels[1].DisplayName != "Mira Chen" || !m.channels[1].Favorite || m.channels[2].Favorite {
		t.Fatalf("unexpected channel list: %+v", m.channels)
	}

	sidebar := m.sidebar(35, 12)
	discussions := strings.Index(sidebar, "Discussions")
	activity := strings.Index(sidebar, "Mentions & reactions")

	top, bottom := strings.Index(sidebar, "Favorites"), strings.Index(sidebar, "Channels")
	if discussions < 0 || activity <= discussions || top <= activity || bottom <= top ||
		!strings.Contains(sidebar[top:bottom], "Mira Chen") || !strings.Contains(sidebar[bottom:], "Elsewhere") {
		t.Fatalf("unexpected sidebar order:\n%s", sidebar)
	}

	m.Update(m.key(tea.KeyMsg{Type: tea.KeyEnter})())

	if viewed != "chat" || m.channels[0].Unread != 0 {
		t.Fatalf("did not mark the opened channel read: %q %+v", viewed, m.channels[0])
	}

	if got := m.View(); !strings.Contains(got, "Root topic") || !strings.Contains(got, "↳ 1 new") ||
		!strings.Contains(got, "●") || strings.Contains(got, "Separate reply") {
		t.Fatalf("unexpected collapsed timeline:\n%s", got)
	}

	m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("o")})

	if got := m.View(); !strings.Contains(got, "notes.txt") || !strings.Contains(got, "https://example.org/guide") {
		t.Fatalf("attachment and link menu:\n%s", got)
	}

	tmp := t.TempDir()

	opened := filepath.Join(tmp, "opened")
	if err := os.WriteFile(filepath.Join(tmp, "xdg-open"), []byte("#!/bin/sh\nprintf '%s' \"$1\" > \"$LOOP_TEST_OPENED\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", tmp+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("XDG_CACHE_HOME", tmp)
	t.Setenv("LOOP_TEST_OPENED", opened)
	m.Update(m.key(tea.KeyMsg{Type: tea.KeyEnter})())

	chosen, err := os.ReadFile(opened)
	if err != nil || filepath.Base(string(chosen)) != "notes.txt" {
		t.Fatalf("unexpected attachment opened: %q, %v", chosen, err)
	}

	data, err := os.ReadFile(string(chosen))
	if err != nil || string(data) != "test attachment" {
		t.Fatalf("unexpected downloaded attachment: %q, %v", data, err)
	}

	m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("o")})
	m.key(tea.KeyMsg{Type: tea.KeyDown})
	m.Update(m.key(tea.KeyMsg{Type: tea.KeyEnter})())

	if got, err := os.ReadFile(opened); err != nil || string(got) != "https://example.org/guide" {
		t.Fatalf("unexpected link opened: %q, %v", got, err)
	}

	m.Update(m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})())

	if !threadRead || len(m.channels[0].Threads) != 0 {
		t.Fatal("opening the unread thread did not clear its badge")
	}

	if got := m.View(); !strings.Contains(got, "Separate reply") || !strings.Contains(got, "Thread · Town") {
		t.Fatalf("unexpected open thread:\n%s", got)
	}

	for _, width := range []int{25, 63, 88, 108, 124} {
		m.Update(tea.WindowSizeMsg{Width: width, Height: 16})

		for _, line := range strings.Split(m.View(), "\n") {
			if lipgloss.Width(line) > width {
				t.Errorf("line overflows %d columns: %q", width, line)
			}
		}
	}

	m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("line one")})
	m.key(tea.KeyMsg{Type: tea.KeyCtrlJ})
	m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("line two")})
	m.Update(m.key(tea.KeyMsg{Type: tea.KeyEnter})())

	if sent["channel_id"] != "chat" || sent["root_id"] != "root" || sent["message"] != "line one\nline two" {
		t.Fatalf("reply request: %#v", sent)
	}
}

const threadFixture = `{"order":["answer","root"],"posts":{
  "root":{"id":"root","channel_id":"chat","user_id":"peer","create_at":1000,
      "message":"Root topic [guide](https://example.org/guide)","file_ids":["file1"],
      "metadata":{"files":[{"id":"file1","name":"notes.txt"}]}},
  "answer":{"id":"answer","channel_id":"chat","root_id":"root","user_id":"me","create_at":2000,"message":"Separate reply"}
}}`

func TestTimelineUsesReplyOnlyPageCursor(t *testing.T) {
	var requests []string

	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			_, _ = fmt.Fprint(w, "[]")
			return
		}

		before := r.URL.Query().Get("before")
		requests = append(requests, before)
		list := postList{Posts: make(map[string]post)}

		if before == "" {
			for i := range 60 {
				id := fmt.Sprintf("reply-%d", i)
				list.Order = append(list.Order, id)
				list.Posts[id] = post{ID: id, RootID: "root", CreatedAt: int64(100 - i)}
			}
		} else {
			list.Order = []string{"root"}
			list.Posts["root"] = post{ID: "root", CreatedAt: 35}
		}

		if err := json.NewEncoder(w).Encode(list); err != nil {
			t.Error(err)
		}
	}))
	defer s.Close()

	page, err := newClient(s.URL, "fixture").timeline(t.Context(), "chat", "")
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(requests, []string{"", "reply-59"}) || len(page.posts) != 1 || page.posts[0].Replies != 60 {
		t.Fatalf("requests %v, page %+v", requests, page)
	}
}

func TestLinksDisplayOnceAndKeepFullTarget(t *testing.T) {
	url := "https://example.org/first/second/project/long_project/merge_requests/844"
	p := post{Text: "See [" + url + "](" + url + ") and [guide](https://example.org/docs)."}

	render := strings.Join(postLines(p, 120, messageFormatting{}, true), "\n")
	if strings.Contains(render, "https://") || !strings.Contains(render, "↗ example.org/…/merge_requests/844") ||
		!strings.Contains(render, "↗ guide.") {
		t.Fatalf("unexpected formatted message: %q", render)
	}

	links := messageResources(p, "https://chat.example.org")
	if len(links) != 2 || links[0].link != url || links[1].link != "https://example.org/docs" {
		t.Fatalf("message did not retain full link targets: %+v", links)
	}

	code := "    curl https://example.org/docs"
	if got := strings.Join(postLines(post{Text: code}, 120, messageFormatting{}, true), "\n"); got != code {
		t.Fatalf("code URL should remain verbatim: %q", got)
	}
}

func TestRelativeLinksUseConfiguredServer(t *testing.T) {
	const server = "https://chat.example.org/team"

	p := post{Text: "See [guide](/docs)."}
	lines := strings.Join(postLines(p, 80, messageFormatting{server: server}, true), "\n")

	items := messageResources(p, server)
	if !strings.Contains(lines, "↗ guide.") || len(items) != 1 || items[0].link != "https://chat.example.org/docs" {
		t.Fatalf("relative link should use the configured server: %q, %+v", lines, items)
	}
}

func TestResourceLabelsKeepInlineText(t *testing.T) {
	p := post{Text: "Read [**the** `setup` guide](/docs) and ![the *status* diagram](/status.png)."}

	items := messageResources(p, "https://chat.example.org")
	if len(items) != 2 || items[0].label != "the setup guide" || items[1].label != "the status diagram" {
		t.Fatalf("unexpected labels: %+v", items)
	}
}

func TestOfflineDemoLoadsChatAndThread(t *testing.T) {
	api := newDemoClient()
	if err := api.connect(t.Context()); err != nil {
		t.Fatal(err)
	}

	channels, err := api.channels(t.Context())
	if err != nil || len(channels) != 3 || !channels[0].Favorite {
		t.Fatalf("demo channels: %+v, %v", channels, err)
	}

	page, err := api.timeline(t.Context(), channels[0].ID, "")
	if err != nil || len(page.posts) != 3 || page.posts[0].Replies != 2 {
		t.Fatalf("demo history: %+v, %v", page, err)
	}

	replies, err := api.thread(t.Context(), page.posts[0].ID)
	if err != nil || len(replies) != 3 || replies[0].Author != "You" {
		t.Fatalf("demo thread: %+v, %v", replies, err)
	}
}

func TestFencedCodeUsesAFrameAndKeepsIndentation(t *testing.T) {
	const fence = "```"

	text := "before\n" + fence + "bash\n  printf '%s' value\n" + fence + "\nafter"

	got := strings.Join(postLines(post{Text: text}, 80, messageFormatting{}, true), "\n")
	if strings.Contains(got, fence) || !strings.Contains(got, "╭─ bash\n│   printf '%s' value\n╰─") {
		t.Fatalf("unexpected formatted code: %q", got)
	}
}
