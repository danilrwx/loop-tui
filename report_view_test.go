package main

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestReportLinksRetainDifferentLabelsForTheSamePipeline(t *testing.T) {
	const pipeline = "https://example.org/pipelines/123"
	var markdown strings.Builder
	markdown.WriteString("| Cluster | Passed |\n| --- | ---: |\n")
	labels := []string{"redos (nfs)", "astra (local)", "alt (replicated)"}
	for _, label := range append(append([]string(nil), labels...), labels[0]) {
		fmt.Fprintf(&markdown, "| [%s](%s) | 12 |\n", label, pipeline)
	}
	report := post{ID: "report", Text: markdown.String()}
	m := newApp(t.Context(), newClient("https://example.org", "fixture"))
	m.width, m.height, m.active, m.focus = 100, 18, "chat", channelPane
	m.timeline.posts = []post{report}
	m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("o")})
	if len(m.resources) != len(labels) {
		t.Fatalf("expected one resource per cluster label, got %+v", m.resources)
	}
	for i, got := range m.resources {
		if got.label != labels[i] || got.link != pipeline {
			t.Fatalf("unrecognized link for %s: %+v", labels[i], got)
		}
	}
}

func TestScrollLongReportAndExpandTheThread(t *testing.T) {
	var markdown strings.Builder
	markdown.WriteString("| Test | Reason |\n| --- | --- |\n")
	for i := range 50 {
		fmt.Fprintf(&markdown, "| Test #%02d | Report detail #%02d |\n", i, i)
	}
	m := newApp(t.Context(), nil)
	m.width, m.height, m.active, m.root, m.focus = 140, 17, "chat", "root", threadPane
	m.channels = []channel{{ID: "chat", DisplayName: "Example"}}
	m.discussion.posts = []post{{ID: "report", Text: markdown.String()}, {ID: "older", Text: "Earlier reply"}}
	m.timeline.posts = []post{{ID: "root", Text: "Original message"}}
	key := func(typ tea.KeyType) { m.key(tea.KeyMsg{Type: typ}) }
	runeKey := func(ch rune) { m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}}) }
	screen := func() string { return ansi.Strip(m.View()) }

	if initial := screen(); !strings.Contains(initial, "Report detail #00") || strings.Contains(initial, "#49") {
		t.Fatal("expected the start of the table in the split pane")
	}
	key(tea.KeyCtrlD)
	if m.discussion.bodyOffset == 0 || m.discussion.selected != 0 {
		t.Fatal("paging down must scroll the selected report before moving between posts")
	}
	key(tea.KeyCtrlU)
	if m.discussion.bodyOffset != 0 || m.discussion.selected != 0 {
		t.Fatal("paging up should return to the start of the same report")
	}
	key(tea.KeyCtrlD)
	offset := m.discussion.bodyOffset
	m.discussion.replace(append([]post{{ID: "newer", Text: "New reply"}}, m.discussion.posts...))
	if m.discussion.selectedPost().ID != "report" || m.discussion.bodyOffset != offset {
		t.Fatal("a background refresh should not jump away from the report being read")
	}
	runeKey('F')
	if layout := m.layout(); layout.thread != m.width || layout.channel != 0 || layout.sidebar != 0 {
		t.Fatalf("focused thread did not expand: %+v", layout)
	}
	for range 20 {
		key(tea.KeyCtrlD)
		if strings.Contains(screen(), "Report detail #49") {
			break
		}
	}
	if !strings.Contains(screen(), "Report detail #49") || m.discussion.selectedPost().ID != "report" {
		t.Fatal("the end of the table must be reachable within the expanded thread")
	}
	key(tea.KeyEsc)
	if m.layout().sidebar == 0 || m.root != "root" || m.focus != threadPane {
		t.Fatal("Esc should restore the split without closing the thread")
	}
	runeKey('v')
	key(tea.KeyEnd)
	if !strings.Contains(screen(), "Report detail #49") {
		t.Fatal("the full-message reader should also reach the end")
	}
}

func TestPagingStopsOnLongReportsBetweenShortMessages(t *testing.T) {
	report := post{ID: "report", Text: "Start of report\n" + strings.Repeat("Middle of report\n", 60) + "End of report"}
	m := newApp(t.Context(), nil)
	m.width, m.height, m.active, m.focus = 90, 30, "chat", channelPane
	m.timeline.posts = []post{{ID: "newer"}, {ID: "short"}, report, {ID: "older"}}
	key := func(typ tea.KeyType) { m.key(tea.KeyMsg{Type: typ}) }
	key(tea.KeyCtrlU)
	tail := m.timeline.bodyOffset
	if m.timeline.selectedPost().ID != "report" || tail == 0 || !strings.Contains(ansi.Strip(m.View()), "End of report") {
		t.Fatal("paging up should stop on the end of a long report")
	}
	key(tea.KeyCtrlU)
	if m.timeline.selectedPost().ID != "report" || m.timeline.bodyOffset >= tail {
		t.Fatal("the next page should continue reading the same report")
	}
	m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("K")})
	key(tea.KeyCtrlD)
	if m.timeline.selectedPost().ID != "report" || m.timeline.bodyOffset != 0 || !strings.Contains(ansi.Strip(m.View()), "Start of report") {
		t.Fatal("paging down from older messages should stop on the start of the report")
	}
}

func TestPagingStopsOnALongReportFetchedFromOlderHistory(t *testing.T) {
	report := post{ID: "report", Text: "Start of report\n" + strings.Repeat("Middle of report\n", 60) + "End of report"}
	m := newApp(t.Context(), nil)
	m.width, m.height, m.active, m.focus = 90, 30, "chat", channelPane
	m.timeline = conversation{posts: []post{{ID: "newer"}}, before: "newer"}
	if cmd := m.key(tea.KeyMsg{Type: tea.KeyCtrlU}); cmd == nil {
		t.Fatal("expected a request for older posts")
	}
	m.Update(historyResult{
		request: m.timeline.request, older: true,
		page: historyPage{posts: []post{{ID: "short"}, report, {ID: "oldest"}}},
	})
	if m.timeline.selectedPost().ID != "report" || m.timeline.pendingOlder != 0 ||
		!strings.Contains(ansi.Strip(m.View()), "End of report") {
		t.Fatal("a fetched report must stop paging at its end")
	}
}

func TestLineKeysReachTheEndBeforeMovingToTheNextPost(t *testing.T) {
	var text strings.Builder
	for line := 1; line <= 64; line++ {
		fmt.Fprintf(&text, "Report line %02d\n", line)
	}
	m := newApp(t.Context(), nil)
	m.width, m.height, m.active, m.focus = 170, 45, "chat", channelPane
	m.timeline.posts = []post{{ID: "newer", Text: "Next post"}, {ID: "report", Text: text.String()}}
	m.timeline.selectIndex(1)
	key := func(typ tea.KeyType) { m.key(tea.KeyMsg{Type: typ}) }
	runeKey := func(ch rune) { m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}}) }
	screen := func() string { return ansi.Strip(m.View()) }

	if initial := screen(); !strings.Contains(initial, "Report line 01") || strings.Contains(initial, "Report line 64") {
		t.Fatalf("expected only the start of the report on the first screen: %q", initial)
	}
	runeKey('j')
	if m.timeline.bodyOffset != 1 || m.timeline.selectedPost().ID != "report" {
		t.Fatal("j should scroll one line inside the report")
	}
	key(tea.KeyUp)
	if m.timeline.bodyOffset != 0 || m.timeline.selectedPost().ID != "report" {
		t.Fatal("the up arrow should scroll a line in the opposite direction")
	}
	for steps := 0; steps < 64 && !strings.Contains(screen(), "Report line 64"); steps++ {
		key(tea.KeyDown)
		if m.timeline.selectedPost().ID != "report" {
			t.Fatal("down skipped to the next post before showing the last line")
		}
	}
	if !strings.Contains(screen(), "Report line 64") {
		t.Fatal("the last line of the report was never shown")
	}
	key(tea.KeyDown)
	if m.timeline.selectedPost().ID != "newer" {
		t.Fatal("down at the end of the report should move to the next post")
	}
	runeKey('k')
	if m.timeline.selectedPost().ID != "report" || !strings.Contains(screen(), "Report line 64") {
		t.Fatal("k should return to the end of the preceding report")
	}
}

func TestReportPreviewsKeepFailuresVisibleAndLogsReachable(t *testing.T) {
	const finalLine = "FINAL_DIAGNOSTIC"
	report := post{
		ID: "report",
		Text: "### Failed tests\n\n| Tests | Reason |\n| --- | --- |\n" +
			"| PeeringMigration | create peer migration. " + strings.Repeat("The webhook denied the request with a timeout. ", 130) + finalLine + " |\n" +
			"| SynchronizedAfterSuite | Controller logs contain an error. |",
	}
	m := newApp(t.Context(), nil)
	m.width, m.height, m.active, m.root, m.focus, m.fullPane = 90, 22, "chat", "root", threadPane, true
	m.discussion.posts = []post{report, {ID: "older", Text: "Previous reply"}}
	runeKey := func(ch rune) { m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}}) }
	screen := func() string { return ansi.Strip(m.View()) }

	summary := screen()
	for _, fragment := range []string{"PeeringMigration", "create peer migration", "more lines  [x]", "SynchronizedAfterSuite", "Controller logs contain an error."} {
		if !strings.Contains(summary, fragment) {
			t.Fatalf("compact report is missing %q: %s", fragment, summary)
		}
	}
	if strings.Contains(summary, finalLine) {
		t.Fatal("the complete diagnostic should be folded in chat")
	}

	runeKey('x')
	if !m.discussion.tableDetails || strings.Contains(screen(), "more lines  [x]") {
		t.Fatal("x did not expand the diagnostic in place")
	}
	for range 32 {
		m.key(tea.KeyMsg{Type: tea.KeyPgDown})
		if strings.Contains(screen(), finalLine) {
			break
		}
	}
	if m.discussion.selectedPost().ID != report.ID || !strings.Contains(screen(), finalLine) {
		t.Fatal("paging must reach the final diagnostic in the expanded report")
	}
	runeKey('x')
	if m.discussion.tableDetails || !strings.Contains(screen(), "SynchronizedAfterSuite") {
		t.Fatal("x did not restore the compact view")
	}

	runeKey('v')
	runeKey('G')
	if !strings.Contains(screen(), finalLine) {
		t.Fatal("the full-message reader must retain the complete diagnostic")
	}
}
