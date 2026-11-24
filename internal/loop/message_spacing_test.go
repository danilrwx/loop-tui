package loop

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func TestRepeatedAuthorMessagesKeepReactionsCloseToBody(t *testing.T) {
	m := newApp(t.Context(), nil)
	older := post{ID: "1", Text: "First [guide](https://example.org)\n \n", Author: "You", UserID: "me", CreatedAt: 61000}
	newer := post{ID: "2", Text: "Next link https://example.org\r\n\t", Author: "You", UserID: "me", CreatedAt: 62000}
	other := post{ID: "3", Text: "Another author", Author: "Peer", UserID: "peer", CreatedAt: 63000}
	older.Metadata.Reactions = []reaction{{EmojiName: "eyes"}}
	newer.Metadata.Reactions = older.Metadata.Reactions
	view := conversation{posts: []post{other, newer, older}}
	lines := strings.Split(ansi.Strip(m.transcript("Chat", &view, 80, 18, true, false, channel{})), "\n")
	first := -1

	for i, line := range lines {
		if strings.Contains(line, "First ↗ guide") {
			first = i
			break
		}
	}

	if first < 0 || !strings.Contains(lines[first+1], "↳ 👀 1") ||
		!strings.Contains(lines[first+2], "You") || !strings.Contains(lines[first+3], "Next link") ||
		!strings.Contains(lines[first+4], "↳ 👀 1") || strings.TrimSpace(lines[first+5]) != "" ||
		!strings.Contains(lines[first+6], "Peer") {
		t.Fatalf("unexpected message spacing:\n%s", strings.Join(lines, "\n"))
	}
}

func TestDateDividerFillsPaneWithoutAnExtraGap(t *testing.T) {
	m := newApp(t.Context(), nil)
	day := time.Date(2026, time.November, 12, 21, 50, 0, 0, time.Local)
	older := post{ID: "1", Text: "Older message", Author: "Pavel", UserID: "pavel", CreatedAt: day.UnixMilli(), Replies: 2}
	newer := post{ID: "2", Text: "Newer message", Author: "You", UserID: "me", CreatedAt: day.AddDate(0, 0, 11).UnixMilli()}
	view := conversation{posts: []post{newer, older}}
	lines := strings.Split(ansi.Strip(m.transcript("Chat", &view, 60, 15, true, true, channel{})), "\n")

	want := strings.Repeat("─", 24) + " 2026-11-23 " + strings.Repeat("─", 24)
	for i, line := range lines {
		if line == want && i > 0 && i+1 < len(lines) &&
			strings.Contains(lines[i-1], "2 replies") && strings.Contains(lines[i+1], "You") {
			return
		}
	}

	t.Fatalf("expected the full-width date directly between the two messages:\n%s", strings.Join(lines, "\n"))
}
