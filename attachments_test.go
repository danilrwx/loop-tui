package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/danilrwx/telega/tui"
)

func TestThreadAttachmentDraftAndRetry(t *testing.T) {
	const filename = "снимок экрана.png"
	const contents = "fixture image"
	var uploads, sends atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture" {
			t.Error("request did not authenticate")
		}
		switch r.Method + " " + r.URL.Path {
		case "POST /api/v4/files":
			uploads.Add(1)
			body, err := io.ReadAll(r.Body)
			if err != nil || string(body) != contents || r.ContentLength != int64(len(contents)) ||
				r.URL.Query().Get("channel_id") != "chat" || r.URL.Query().Get("filename") != filename {
				t.Errorf("unexpected upload: %s %q, %v", r.URL, body, err)
			}
			fmt.Fprint(w, `{"file_infos":[{"id":"file1"}]}`)
		case "POST /api/v4/posts":
			var body struct {
				Channel string   `json:"channel_id"`
				Root    string   `json:"root_id"`
				Message string   `json:"message"`
				Files   []string `json:"file_ids"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body.Channel != "chat" || body.Root != "thread" || body.Message != "" ||
				len(body.Files) != 1 || body.Files[0] != "file1" {
				t.Errorf("unexpected post: %+v", body)
			}
			if sends.Add(1) == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
			}
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer backend.Close()

	t.Setenv("XDG_STATE_HOME", t.TempDir())
	store, err := tui.OpenDraftStore("loop-tui")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), filename)
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	makeModel := func() *app {
		m := newApp(t.Context(), newClient(backend.URL, "fixture"))
		m.draftStore = store
		m.active, m.root, m.focus = "chat", "thread", threadPane
		m.Update(tea.WindowSizeMsg{Width: 120, Height: 9})
		return m
	}

	m := makeModel()
	m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	m.attachmentPicker.SetPath(path)
	m.key(tea.KeyMsg{Type: tea.KeyEnter})
	if got := m.View(); !strings.Contains(got, "Caption") || !strings.Contains(got, filename+" [Ctrl+O]") ||
		len(strings.Split(got, "\n")) != m.height {
		t.Fatalf("attachment row does not fit inside the thread pane:\n%s", got)
	}

	m = makeModel()
	if !m.hasDraft() || len(m.restoreDraftFiles(draftKey{channel: "chat"})) != 0 {
		t.Fatal("file-only draft did not survive restart or leaked into the channel draft")
	}
	m.compose()
	m.key(tea.KeyMsg{Type: tea.KeyCtrlO})
	if len(m.resources) != 1 || m.resources[0].localPath != path || uploads.Load() != 0 {
		t.Fatalf("draft picker did not show the local file: %+v", m.resources)
	}
	openerDir := t.TempDir()
	opened := filepath.Join(openerDir, "opened")
	t.Setenv("PATH", openerDir)
	t.Setenv("LOOP_TEST_OPENED", opened)
	opener := []byte("#!/bin/sh\nprintf '%s' \"$1\" > \"$LOOP_TEST_OPENED\"\n")
	if err := os.WriteFile(filepath.Join(openerDir, "xdg-open"), opener, 0700); err != nil {
		t.Fatal(err)
	}
	m.Update(m.key(tea.KeyMsg{Type: tea.KeyEnter})())
	if got, err := os.ReadFile(opened); err != nil || string(got) != path || uploads.Load() != 0 {
		t.Fatalf("preview did not open the local file directly: %q, %v", got, err)
	}
	m.key(tea.KeyMsg{Type: tea.KeyEsc})
	m.Update(m.key(tea.KeyMsg{Type: tea.KeyEnter})())
	if !strings.Contains(m.status, "Send failed") || len(m.draftFiles[m.draftKey()]) != 1 {
		t.Fatalf("failed send lost the draft: %q", m.status)
	}

	m = makeModel()
	m.compose()
	m.Update(m.key(tea.KeyMsg{Type: tea.KeyEnter})())
	if uploads.Load() != 1 || sends.Load() != 2 || makeModel().hasDraft() {
		t.Fatal("retry uploaded again or successful send left the draft")
	}
}

func TestHistoryAttachmentsFollowFocusedPane(t *testing.T) {
	m := newApp(t.Context(), nil)
	m.active, m.root, m.focus = "chat", "thread", threadPane
	p := post{Files: []string{"thread-file"}, Text: "https://example.org"}
	p.Metadata.Files = []fileInfo{{ID: "thread-file", Name: "notes.txt"}}
	m.discussion.posts = []post{p}
	m.timeline.posts = []post{{Files: []string{"channel-file"}}}
	m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("V")})
	if len(m.resources) != 1 || m.resources[0].fileID != "thread-file" || m.resources[0].label != "notes.txt" {
		t.Fatalf("wrong thread attachments: %+v", m.resources)
	}
	m.key(tea.KeyMsg{Type: tea.KeyEsc})
	m.focus = channelPane
	m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("V")})
	if len(m.resources) != 1 || m.resources[0].fileID != "channel-file" {
		t.Fatalf("wrong channel attachments: %+v", m.resources)
	}
}
