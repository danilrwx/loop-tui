package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/danilrwx/telega/tui"
)

func TestAttachmentCompletesPathAndStagesFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, "work files")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "report.txt")
	if err := os.WriteFile(path, []byte("report"), 0600); err != nil {
		t.Fatal(err)
	}
	m := newApp(t.Context(), newClient("https://fixture", "fixture"))
	m.active, m.focus = "channel", channelPane
	m.Update(tea.WindowSizeMsg{Width: 70, Height: 14})
	m.pickAttachment()
	m.attachmentPicker.SetPath("~/wor")
	m.key(tea.KeyMsg{Type: tea.KeyTab})
	if got := m.attachmentPicker.Path(); got != "~/work files/" || !strings.Contains(m.View(), "report.txt") {
		t.Fatalf("completion did not open the directory: %q", got)
	}
	m.key(tea.KeyMsg{Type: tea.KeyDown})
	m.key(tea.KeyMsg{Type: tea.KeyDown})
	m.key(tea.KeyMsg{Type: tea.KeyEnter})
	if files := m.draftFiles[m.draftKey()]; len(files) != 1 || files[0].Path != path || files[0].Managed {
		t.Fatalf("file was not staged: %+v", files)
	}
}

func TestLargeTextAttachmentAndPrivateEdit(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	store, err := tui.OpenDraftStore("loop-tui")
	if err != nil {
		t.Fatal(err)
	}
	m := newApp(t.Context(), newClient("https://fixture", "fixture"))
	m.draftStore = store
	m.active, m.focus = "channel", channelPane
	m.compose()
	m.setDraft("initial caption")
	m.saveDraft(m.draft.Value())
	large := strings.Repeat("a line from the text editor\n", 30_000)
	file, err := newAttachmentCopy("message.txt", strings.NewReader(large))
	if err != nil {
		t.Fatal(err)
	}
	m.Update(attachmentEditResult{file: file, index: -1, sourceText: "initial caption"})
	if data, err := os.ReadFile(file.Path); err != nil || string(data) != large || m.draft.Value() != "" {
		t.Fatalf("large text was not attached intact: %d bytes, %v", len(data), err)
	}
	reopened := newApp(t.Context(), m.api)
	reopened.draftStore, reopened.active, reopened.focus = store, "channel", channelPane
	if files := reopened.restoreDraftFiles(reopened.draftKey()); len(files) != 1 || files[0] != file {
		t.Fatalf("attachment did not survive restart: %+v", files)
	}

	original := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(original, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	m.addAttachment(draftAttachment{Path: original, UploadID: "before-edit"})
	edited, err := newAttachmentCopy("notes.txt", strings.NewReader("edited"))
	if err != nil {
		t.Fatal(err)
	}
	m.Update(attachmentEditResult{file: edited, index: 1})
	got, err := os.ReadFile(original)
	if files := m.draftFiles[m.draftKey()]; err != nil || string(got) != "original" || files[1] != edited {
		t.Fatalf("editing should replace the draft copy and keep the original file: %+v, %v", files, err)
	}
	m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	if _, err := os.Stat(edited.Path); !os.IsNotExist(err) {
		t.Fatalf("removed draft file was not cleaned up: %v", err)
	}
}

func TestTextAttachmentUsesChosenFilename(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	t.Setenv("PATH", state)
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")
	if err := os.WriteFile(filepath.Join(state, "vim"), []byte("#!/bin/sh\n"), 0700); err != nil {
		t.Fatal(err)
	}
	m := newApp(t.Context(), newClient("https://fixture", "fixture"))
	m.active, m.focus = "channel", channelPane
	m.compose()
	m.setDraft("draft content")
	m.key(tea.KeyMsg{Type: tea.KeyRunes, Alt: true, Runes: []rune("е")})
	if m.textAttachmentPrompt == nil {
		t.Fatal("Alt+T should open the file name prompt")
	}
	m.key(tea.KeyMsg{Type: tea.KeyRunes, Paste: true, Runes: []rune("../bad.md")})
	if cmd := m.key(tea.KeyMsg{Type: tea.KeyEnter}); cmd != nil || m.textAttachmentPrompt == nil {
		t.Fatal("a file name should not accept a directory")
	}
	m.key(tea.KeyMsg{Type: tea.KeyEsc})
	m.key(tea.KeyMsg{Type: tea.KeyRunes, Alt: true, Runes: []rune("t")})
	m.key(tea.KeyMsg{Type: tea.KeyRunes, Paste: true, Runes: []rune("Отчёт.md")})
	if cmd := m.key(tea.KeyMsg{Type: tea.KeyEnter}); cmd == nil {
		t.Fatal("the name should launch the editor")
	}
	files, err := filepath.Glob(filepath.Join(state, "loop-tui", "draft-attachments", "draft-*", "Отчёт.md"))
	if err != nil || len(files) != 1 {
		t.Fatalf("expected a draft file under the chosen name: %v, %v", files, err)
	}
	if got, err := os.ReadFile(files[0]); err != nil || string(got) != "draft content" {
		t.Fatalf("the editor should start with a copy of the draft: %q, %v", got, err)
	}
}
