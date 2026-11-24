package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestGlobalMessageSearchOpensExactReply(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.Method + " " + req.URL.Path {
		case "POST /api/v4/posts/search":
			var input struct{ Terms string }
			if err := json.NewDecoder(req.Body).Decode(&input); err != nil || input.Terms != "needle" {
				t.Errorf("wrong search data: %+v %v", input, err)
			}
			fmt.Fprint(w, `{"order":["reply"],"posts":{"reply":{"id":"reply","channel_id":"target","root_id":"root","message":"needle in history","create_at":50}}}`)
		case "GET /api/v4/posts/root/thread":
			fmt.Fprint(w, `{"order":["root","reply","after"],"posts":{"root":{"id":"root","channel_id":"target","message":"root","create_at":10},"reply":{"id":"reply","root_id":"root","channel_id":"target","message":"needle in history","create_at":50},"after":{"id":"after","root_id":"root","channel_id":"target","message":"newer","create_at":60}}}`)
		default:
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	m := newApp(t.Context(), newClient(server.URL, "fixture"))
	m.width, m.height = 100, 24
	m.channels = []channel{{ID: "source", DisplayName: "Source"}, {ID: "target", DisplayName: "Target channel"}}
	m.active, m.focus = "source", channelPane
	m.key(tea.KeyMsg{Type: tea.KeyCtrlF})
	m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("needle"), Paste: true})
	query := m.key(tea.KeyMsg{Type: tea.KeyEnter})
	m.Update(query())
	if got := m.View(); !strings.Contains(got, "Target channel") || !strings.Contains(got, "needle in history") {
		t.Fatalf("missing global result: %q", got)
	}
	jump := m.key(tea.KeyMsg{Type: tea.KeyEnter})
	m.Update(jump())
	if m.messageSearch != nil || m.active != "target" || m.root != "root" || m.discussion.selectedPost().ID != "reply" {
		t.Fatal("opening a search hit must select the exact reply in its original thread")
	}
}
