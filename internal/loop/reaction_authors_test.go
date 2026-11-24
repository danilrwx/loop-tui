package loop

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestReactionAuthorsFromThreadAndReader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v4/posts/selected/reactions":
			_, _ = fmt.Fprint(w, `[{"user_id":"one","emoji_name":"eyes"},{"user_id":"one","emoji_name":"pupupu"},{"user_id":"me","emoji_name":"+1"}]`)
		case "POST /api/v4/users/ids":
			_, _ = fmt.Fprint(w, `[{"id":"one","first_name":"Mira","last_name":"Chen"}]`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	api := newClient(server.URL, "token")
	api.me = person{ID: "me", Username: "self"}
	m := newApp(t.Context(), api)
	m.width, m.height, m.focus = 90, 20, threadPane
	selected := post{ID: "selected"}
	m.discussion.posts = []post{selected}
	shortcut := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("Я")}

	for _, openReader := range []bool{false, true} {
		if openReader {
			m.reader = selected
		}

		cmd := m.key(shortcut)
		if cmd == nil || !strings.Contains(m.View(), "Loading reactions") {
			t.Fatal("Shift+Z should open the reaction authors dialog")
		}

		result := cmd()
		m.Update(result)

		for _, row := range []string{"👀  Mira Chen", ":pupupu:  Mira Chen", "👍  You"} {
			if !strings.Contains(m.View(), row) {
				t.Fatalf("missing %q from dialog: %s", row, m.View())
			}
		}

		m.key(tea.KeyMsg{Type: tea.KeyDown})

		if m.reactionAuthors.Selected != 1 {
			t.Fatal("Down should scroll the dialog")
		}

		m.key(tea.KeyMsg{Type: tea.KeyEsc})
		m.Update(result)

		if m.reactionAuthors != nil || (m.reader.ID != "") != openReader {
			t.Fatal("closing the dialog should restore the previous view and ignore stale results")
		}
	}
}
