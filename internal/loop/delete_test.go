package loop

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestDeleteReplyRequiresConfirmationAndKeepsMessageOnFailure(t *testing.T) {
	replies := make(chan string, 2)

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/api/v4/posts/reply" ||
			r.Header.Get("Authorization") != "Bearer fixture" {
			t.Errorf("unexpected deletion request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)

			return
		}

		status := http.StatusNoContent
		if len(replies) == 0 {
			status = http.StatusForbidden
		}

		replies <- r.URL.Path

		w.WriteHeader(status)
	}))
	defer backend.Close()

	m := newApp(t.Context(), newClient(backend.URL, "fixture"))
	m.width, m.height = 88, 20
	m.active, m.root, m.focus = "chat", "root", threadPane
	root := post{ID: "root", Text: "Question"}
	reply := post{ID: "reply", Text: "My reply", Own: true, RootID: "root"}
	m.timeline.posts = []post{root}
	m.discussion.posts = []post{reply, root}
	key := func(r rune) tea.Cmd {
		return m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}

	m.discussion.selected = 1
	if key('D') != nil || m.deleteTarget.ID != "" || !strings.Contains(m.status, "your own") {
		t.Fatal("another author's message should not be offered for deletion")
	}

	m.discussion.selected = 0

	if key('D') != nil {
		t.Fatal("delete should wait for confirmation")
	}

	view := m.View()

	popup := m.dialogPopup()
	if popup.X <= 0 || popup.Y <= 0 || !strings.Contains(view, "Delete message for everyone?") ||
		!strings.Contains(view, "y confirm deletion  Esc cancel") ||
		!strings.Contains(view, "Thread") {
		t.Fatalf("confirmation should float over the visible thread: %q", view)
	}

	m.discussion.request, m.discussion.loading = 2, true
	m.receiveHistory(historyResult{request: 2, thread: true, page: historyPage{posts: []post{root}}})

	if m.discussion.selectedPost().ID != reply.ID {
		t.Fatal("confirmation lost the visible target during a refresh")
	}

	m.key(tea.KeyMsg{Type: tea.KeyEsc})

	if key('y') != nil || m.deleteTarget.ID != "" || len(replies) != 0 {
		t.Fatal("cancelled deletion still accepts confirmation")
	}

	key('D')

	remove := key('y')
	if remove == nil || key('y') != nil {
		t.Fatal("confirmation should schedule exactly one request")
	}

	m.Update(remove())

	if len(replies) != 1 || m.discussion.selectedPost().ID != reply.ID ||
		!strings.Contains(m.status, "Delete failed") {
		t.Fatalf("failed delete did not preserve the reply: %q", m.status)
	}

	key('D')

	_, refresh := m.Update(key('y')())
	if len(replies) != 2 || refresh == nil || m.root != root.ID ||
		m.discussion.selectedPost().ID != root.ID || m.timeline.selectedPost().ID != root.ID {
		t.Fatal("successful reply deletion should keep the thread and refresh history")
	}
}

func TestDeletingRootClosesItsThread(t *testing.T) {
	m := newApp(t.Context(), nil)
	m.width, m.height = 88, 20
	m.active, m.root, m.focus = "chat", "root", threadPane
	root := post{ID: "root", Text: "My topic", Own: true}
	m.timeline.posts = []post{root, {ID: "older"}}
	m.discussion.posts = []post{{ID: "reply", RootID: root.ID}, root}
	m.reader = root
	m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("D")})

	if got := m.View(); !strings.Contains(got, "Delete message and any replies for everyone?") {
		t.Fatalf("missing root deletion warning: %q", got)
	}

	m.receiveDelete(deleteResult{})

	if m.root != "" || m.reader.ID != "" || m.focus != channelPane ||
		len(m.discussion.posts) != 0 || m.timeline.selectedPost().ID != "older" {
		t.Fatal("deleting a root should close its thread and leave the channel open")
	}
}
