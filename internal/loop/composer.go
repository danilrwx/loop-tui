package loop

import (
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/danilrwx/telega/tui"
	"github.com/rivo/uniseg"
)

const messageLimit = 16000

type vimResult struct {
	text string
	err  error
}

func (m *app) editInVim() tea.Cmd {
	if !m.composing {
		m.compose()
	}

	m.resetMention()

	editor, err := tui.NewEditorDraft(m.ctx, m.draft.Value())
	if err != nil {
		m.status = "Editor: " + err.Error()
		return nil
	}

	return tea.ExecProcess(editor.Command, func(err error) tea.Msg {
		text, err := editor.Finish(err)
		return vimResult{text: text, err: err}
	})
}

func draftLength(text string) int {
	count := 0
	for _, line := range strings.Split(text, "\n") {
		count += uniseg.StringWidth(line)
	}

	return count + strings.Count(text, "\n")
}

func (m *app) setDraft(text string) {
	// Keep longer drafts from Vim intact until the user shortens them to send.
	m.draft.CharLimit = max(messageLimit, draftLength(text))
	m.draft.SetValue(text)
	m.fitComposer()
}

func (m *app) fitComposer() {
	limit := min(tui.ComposerLineLimit(m.height), m.height-6)
	if len(m.draftFiles[m.draftKey()]) > 0 {
		limit--
	}

	limit = max(1, limit)
	probe := m.draft

	rows := probe.LineInfo().Height
	for rows < limit && probe.Line() > 0 {
		probe.CursorStart()
		probe.CursorUp()
		rows += probe.LineInfo().Height
	}

	probe = m.draft
	for rows < limit && probe.Line()+1 < probe.LineCount() {
		line := probe.Line()
		probe.CursorEnd()
		probe.CursorDown()

		if probe.Line() == line {
			break
		}

		rows += probe.LineInfo().Height
	}

	height := min(rows, limit)
	if height != m.draft.Height() {
		info := m.draft.LineInfo()
		line, col := m.draft.Line(), info.StartColumn+info.ColumnOffset
		m.draft.SetHeight(height)

		if m.composing {
			// Reset the scroll origin before restoring the caret in the resized view.
			moveTextarea(&m.draft, 0, 0)
			m.draft, _ = m.draft.Update(nil)
			moveTextarea(&m.draft, line, col)
		}
	}
	// Bubbles fills the viewport's scrollable content during View.
	_ = m.draft.View()
	m.draft, _ = m.draft.Update(nil)
}

func moveTextarea(draft *textarea.Model, line, col int) {
	for draft.Line() > line {
		draft.CursorStart()
		draft.CursorUp()
	}

	for draft.Line() < line {
		previous := draft.Line()
		draft.CursorEnd()
		draft.CursorDown()

		if draft.Line() == previous {
			break
		}
	}

	draft.SetCursor(col)
}
