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

func TestActivityLoadsTabsAndOpensReply(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /api/v4/posts/search":
			var query struct {
				Terms string
				Or    bool `json:"is_or_search"`
			}
			if err := json.NewDecoder(r.Body).Decode(&query); err != nil ||
				query.Terms != `"on-call" "@me" "Daniil"` || !query.Or {
				t.Errorf("unexpected mention search: %+v (%v)", query, err)
			}

			_, _ = fmt.Fprint(w, `{"order":["reply"],"posts":{"reply":{"id":"reply","user_id":"other","channel_id":"chat","root_id":"root","message":"mention preview"}}}`)
		case "GET /api/v4/users/me/posts/reactioned":
			if r.URL.Query().Get("last_reaction_updateat") == "" {
				t.Error("missing reaction cursor")
			}

			_, _ = fmt.Fprint(w, `{"order":["my-post"],"last_reaction_updateat":150,"posts":{"my-post":{"id":"my-post","user_id":"me","channel_id":"chat","message":"reaction preview","metadata":{"reactions":[{"user_id":"other","emoji_name":"eyes","update_at":200},{"user_id":"me","emoji_name":"fire","update_at":300}]}}}}`)
		case "POST /api/v4/users/ids":
			_, _ = fmt.Fprint(w, `[{"id":"other","username":"colleague"}]`)
		case "GET /api/v4/posts/root/thread":
			_, _ = fmt.Fprint(w, `{"order":["root","reply"],"posts":{"root":{"id":"root","channel_id":"chat","user_id":"other","create_at":10},"reply":{"id":"reply","root_id":"root","channel_id":"chat","user_id":"other","create_at":20}}}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	c := newClient(server.URL, "fixture")
	c.me = person{
		ID: "me", Username: "me", FirstName: "Daniil",
		NotifyProps: map[string]string{"mention_keys": "on-call,@all,@me", "first_name": "true"},
	}
	m := newApp(t.Context(), c)
	m.width, m.height = 100, 24
	m.channels = []channel{{ID: "chat", DisplayName: "Chat"}}
	run := func(key tea.KeyMsg) {
		t.Helper()

		if cmd := m.key(key); cmd != nil {
			m.Update(cmd())
		}
	}

	m.channelCursor = activityID

	run(tea.KeyMsg{Type: tea.KeyEnter})

	if screen := m.View(); !strings.Contains(screen, "Mentions & reactions · Mentions") || !strings.Contains(screen, "colleague · Chat") || !strings.Contains(screen, "│ mention preview") {
		t.Fatal("mentions should list the author and post preview")
	}

	run(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})

	if screen := m.View(); !strings.Contains(screen, "Mentions & reactions · Reactions") || !strings.Contains(screen, "colleague 👀") ||
		!strings.Contains(screen, "reaction preview") || m.timeline.before != "150" {
		t.Fatal("reactions should show the latest reaction by someone else and keep the server cursor")
	}

	run(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	run(tea.KeyMsg{Type: tea.KeyEnter})

	if m.active != "chat" || m.root != "root" || m.discussion.selectedPost().ID != "reply" {
		t.Fatal("opening the mention should select the reply in its thread")
	}
}
