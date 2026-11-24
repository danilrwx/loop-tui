package main

import (
	"bytes"
	"context"
	"errors"
	"image/png"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/danilrwx/telega/tui"
)

type clipboardResult struct {
	key  draftKey
	file draftAttachment
	text string
	err  error
}

func (m *app) pasteClipboard(imageOnly bool) tea.Cmd {
	key := m.draftKey()
	if m.clipboardDraft.channel != "" || key.channel == "" {
		return nil
	}
	m.clipboardDraft = key
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 4*time.Second)
		defer cancel()
		result := clipboardResult{key: key}
		for _, cmd := range tui.ClipboardCommands(ctx, "image/png") {
			data, err := cmd.Output()
			if err != nil {
				continue
			}
			if _, err = png.DecodeConfig(bytes.NewReader(data)); err == nil {
				result.file, result.err = newAttachmentCopy("clipboard.png", bytes.NewReader(data))
				return result
			}
		}
		if !imageOnly {
			commands := tui.ClipboardCommands(ctx, "text")
			for _, cmd := range commands {
				data, err := cmd.Output()
				if err == nil && len(data) > 0 && utf8.Valid(data) &&
					!bytes.ContainsRune(data, 0) && !tui.ClipboardPNGPaste(string(data)) {
					result.text = string(data)
					return result
				}
			}
			if len(commands) == 0 {
				if text, err := clipboard.ReadAll(); err == nil && text != "" && !tui.ClipboardPNGPaste(text) {
					result.text = text
					return result
				}
			}
		}
		result.err = errors.New("Clipboard has no readable PNG or text")
		if imageOnly {
			result.err = errors.New("Clipboard has no readable PNG")
		}
		return result
	}
}

func (m *app) receiveClipboard(result clipboardResult) tea.Cmd {
	m.clipboardDraft = draftKey{}
	if result.err != nil {
		m.status = "Paste: " + result.err.Error()
		return nil
	}
	if result.file.Path != "" {
		m.status = ""
		files := m.restoreDraftFiles(result.key)
		m.saveDraftFilesFor(result.key, append(files, result.file))
		if m.draftKey() == result.key {
			if m.resourceMode == draftAttachmentTitle {
				m.showDraftAttachments()
			}
			if m.composing {
				m.fitComposer()
			}
		}
		return nil
	}
	if m.composing && m.draftKey() == result.key && m.resourceMode == "" && m.attachmentPicker == nil && m.textAttachmentPrompt == nil {
		m.draft.InsertString(strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(result.text))
		m.fitComposer()
		m.saveDraft(m.draft.Value())
		return m.refreshMention()
	}
	return nil
}
