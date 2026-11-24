package loop

import (
	"io"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/danilrwx/telega/tui"
)

type attachmentEditResult struct {
	file       draftAttachment
	index      int
	sourceText string
	err        error
}

func newAttachmentCopy(name string, content io.Reader) (draftAttachment, error) {
	path, err := tui.CopyDraftAttachment("loop-tui", name, content)
	return draftAttachment{Path: path, Managed: err == nil}, err
}

func discardAttachmentCopy(file draftAttachment) {
	if file.Managed {
		tui.RemoveDraftAttachment("loop-tui", file.Path)
	}
}

func (m *app) nameTextAttachment() tea.Cmd {
	m.textAttachmentPrompt = &tui.TextAttachmentPrompt{}
	return nil
}

func (m *app) textAttachmentNameKey(key tea.KeyMsg) tea.Cmd {
	name, text := attachmentInputKey(key)
	switch action, filename := m.textAttachmentPrompt.Key(name, text); action {
	case "close":
		m.textAttachmentPrompt = nil
	case "edit":
		m.textAttachmentPrompt = nil
		return m.newTextAttachment(filename)
	}

	return nil
}

func (m *app) newTextAttachment(name string) tea.Cmd {
	m.resetMention()

	text := m.restoreDraft(m.draftKey())
	if m.composing {
		text = m.draft.Value()
	}

	return m.openAttachmentEditor(name, strings.NewReader(text), -1, text)
}

func (m *app) editAttachment(index int) tea.Cmd {
	file := m.restoreDraftFiles(m.draftKey())[index]
	if _, err := tui.AttachmentStat(file.Path); err != nil {
		m.status = "Edit attachment: " + err.Error()
		return nil
	}

	source, err := os.Open(file.Path)
	if err != nil {
		m.status = "Edit attachment: " + err.Error()
		return nil
	}
	defer func() { _ = source.Close() }()

	return m.openAttachmentEditor(filepath.Base(file.Path), source, index, "")
}

func (m *app) openAttachmentEditor(name string, content io.Reader, index int, sourceText string) tea.Cmd {
	file, err := newAttachmentCopy(name, content)
	if err != nil {
		m.status = "Edit attachment: " + err.Error()
		if m.attachmentPicker != nil {
			m.attachmentPicker.Status = m.status
		}

		return nil
	}

	cmd, err := tui.EditorFileCommand(m.ctx, file.Path)
	if err != nil {
		discardAttachmentCopy(file)

		m.status = "Editor: " + err.Error()
		if m.attachmentPicker != nil {
			m.attachmentPicker.Status = m.status
		}

		return nil
	}

	m.attachmentEditing = true

	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return attachmentEditResult{file: file, index: index, sourceText: sourceText, err: err}
	})
}

func (m *app) finishAttachmentEdit(result attachmentEditResult) tea.Cmd {
	m.attachmentEditing = false

	if result.err == nil {
		_, result.err = tui.AttachmentStat(result.file.Path)
	}

	if result.err != nil {
		discardAttachmentCopy(result.file)

		m.status = "Edit attachment: " + result.err.Error()
		if m.attachmentPicker != nil {
			m.attachmentPicker.Status = m.status
		}

		return nil
	}

	m.status = ""

	files := m.restoreDraftFiles(m.draftKey())
	if result.index >= 0 {
		old := files[result.index]
		files = append([]draftAttachment(nil), files...)

		files[result.index] = result.file
		if !m.saveDraftFiles(files) {
			discardAttachmentCopy(result.file)
			return nil
		}

		discardAttachmentCopy(old)
		m.showDraftAttachments()
		m.resourceIndex = result.index

		return nil
	}

	if !m.addAttachment(result.file) {
		discardAttachmentCopy(result.file)

		if m.attachmentPicker != nil {
			m.attachmentPicker.Status = m.status
		}

		return nil
	}

	if result.sourceText != "" {
		if m.composing && m.draft.Value() == result.sourceText {
			m.setDraft("")
			m.saveDraft("")
		} else if !m.composing && m.restoreDraft(m.draftKey()) == result.sourceText {
			m.saveDraft("")
		}
	}

	m.attachmentPicker = nil

	return m.focusAttachmentDraft()
}
