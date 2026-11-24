package main

import (
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
)

type mentionState struct {
	active   bool
	term     string
	users    []person
	selected int
}

type mentionQuery uint64
type mentionResult struct {
	id    uint64
	users []person
	err   error
}

type mentionRange struct {
	term       string
	start, end int
	atLineEnd  bool
}

func mentionAtCursor(draft textarea.Model) (mentionRange, bool) {
	lines := strings.Split(draft.Value(), "\n")
	line := []rune(lines[draft.Line()])
	info := draft.LineInfo()
	cursor := min(len(line), info.StartColumn+info.ColumnOffset)
	start := cursor
	for start > 0 && mentionRune(line[start-1]) {
		start--
	}
	if start == 0 || line[start-1] != '@' || (start > 1 && (mentionRune(line[start-2]) || line[start-2] == '@')) {
		return mentionRange{}, false
	}
	end := cursor
	for end < len(line) && mentionRune(line[end]) {
		end++
	}
	return mentionRange{
		term: string(line[start:cursor]), start: start - 1, end: end, atLineEnd: end == len(line),
	}, true
}

func mentionRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r) || r == '_' || r == '-' || r == '.'
}

func (m *app) resetMention() {
	m.mentionID++
	m.mention = mentionState{}
}

func (m *app) refreshMention() tea.Cmd {
	target, ok := mentionAtCursor(m.draft)
	if m.mention.active == ok && (!ok || m.mention.term == target.term) {
		return nil
	}
	m.resetMention()
	if strings.HasPrefix(m.status, "Mention lookup: ") {
		m.status = ""
	}
	if !ok {
		return nil
	}
	m.mention = mentionState{active: true, term: target.term}
	id := m.mentionID
	return tea.Tick(160*time.Millisecond, func(time.Time) tea.Msg { return mentionQuery(id) })
}

func (m *app) queryMention(id uint64) tea.Cmd {
	if !m.composing || !m.mention.active || id != m.mentionID {
		return nil
	}
	term, ch := m.mention.term, m.activeChannel()
	return func() tea.Msg {
		users, err := m.api.autocomplete(m.ctx, ch, term)
		return mentionResult{id: id, users: users, err: err}
	}
}

func (m *app) acceptMention() {
	target, ok := mentionAtCursor(m.draft)
	if ok && target.term == m.mention.term {
		username := m.mention.users[m.mention.selected].Username
		m.draft.SetCursor(target.start)
		for i := target.start; i < target.end; i++ {
			m.draft, _ = m.draft.Update(tea.KeyMsg{Type: tea.KeyDelete})
		}
		suffix := ""
		if target.atLineEnd {
			suffix = " "
		}
		m.draft.InsertString("@" + username + suffix)
	}
	m.resetMention()
}
