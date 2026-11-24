package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestReactionPickerTogglesOnlyOwnReaction(t *testing.T) {
	peer := reaction{PostID: "reply", UserID: "peer", EmojiName: "thumbsup"}
	custom := reaction{PostID: "reply", UserID: "peer", EmojiName: "ship_it"}
	applied := make(chan string, 12)
	customNames := []string{"pupupu", "sbogom", "sad_pepe", "thank_you", "press_f"}
	serverReactions := []reaction{peer, custom}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture" {
			t.Error("missing authentication")
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v4/emoji":
			var names []string
			switch r.URL.Query().Get("page") {
			case "0":
				for i := range emojiPageSize {
					names = append(names, fmt.Sprintf("fixture_%02d", i))
				}
			case "1":
				names = customNames
			default:
				t.Errorf("unexpected emoji catalog page: %s", r.URL)
			}
			entries := make([]map[string]string, 0, len(names))
			for _, name := range names {
				entries = append(entries, map[string]string{"name": name})
			}
			if err := json.NewEncoder(w).Encode(entries); err != nil {
				t.Error(err)
			}
		case "GET /api/v4/posts/reply/reactions":
			if err := json.NewEncoder(w).Encode(serverReactions); err != nil {
				t.Error(err)
			}
		case "POST /api/v4/reactions":
			var item reaction
			if err := json.NewDecoder(r.Body).Decode(&item); err != nil || item.PostID != "reply" || item.UserID != "me" {
				t.Errorf("unexpected reaction: %+v, %v", item, err)
			}
			applied <- "add " + item.EmojiName
			serverReactions = append(serverReactions, item)
			w.WriteHeader(http.StatusCreated)
		default:
			name, ok := strings.CutPrefix(r.URL.Path, "/api/v4/users/me/posts/reply/reactions/")
			if r.Method != http.MethodDelete || !ok {
				t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				w.WriteHeader(http.StatusNotFound)
				return
			}
			applied <- "remove " + name
			serverReactions = slices.DeleteFunc(serverReactions, func(item reaction) bool {
				return item.UserID == "me" && item.EmojiName == name
			})
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer backend.Close()
	api := newClient(backend.URL, "fixture")
	api.me.ID = "me"
	m := newApp(t.Context(), api)
	m.Update(tea.WindowSizeMsg{Width: 90, Height: 22})
	m.active, m.root, m.focus = "chat", "root", threadPane
	reply := post{ID: "reply", RootID: m.root, Text: "Selected reply"}
	m.discussion.posts = []post{reply}
	key := func(text string) tea.Cmd {
		return m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(text)})
	}

	key("z")
	if got := m.View(); !strings.Contains(got, reply.Text) || !strings.Contains(got, "1 👍") ||
		!strings.Contains(got, "7 🔙") || !strings.Contains(got, "8 ✅") ||
		!strings.Contains(got, "9 👀") || !strings.Contains(got, "Esc cancel") || !strings.Contains(got, "Tab picker") {
		t.Fatalf("picker should keep the conversation visible: %q", got)
	}
	m.Update(tea.WindowSizeMsg{Width: 24, Height: 14})
	if status, keys := m.reactionFooter(m.width - 2); !strings.Contains(status, "😂") ||
		!strings.Contains(keys, "🔙") || !strings.Contains(keys, "✅") || !strings.Contains(keys, "👀") || !strings.Contains(status, "Tab") {
		t.Fatalf("narrow picker lost reaction choices: %q / %q", status, keys)
	}
	m.Update(tea.WindowSizeMsg{Width: 90, Height: 22})
	m.key(tea.KeyMsg{Type: tea.KeyEsc})
	if key("1") != nil {
		t.Fatal("cancelled picker still accepts a reaction")
	}
	key("z")
	for _, want := range []string{"↳ 👍 2  :ship_it: 1", "↳ 👍 1  :ship_it: 1"} {
		cmd := key("1")
		if cmd == nil {
			t.Fatal("picker should support another choice without reopening")
		}
		m.Update(cmd())
		if got := m.View(); !strings.Contains(got, want) {
			t.Fatalf("expected %q under the reply: %q", want, got)
		}
	}
	first := key("7")
	if first == nil || key("8") != nil || key("9") != nil {
		t.Fatal("quick reactions should be queued behind the first")
	}
	if cmd := m.key(tea.KeyMsg{Type: tea.KeyTab}); cmd == nil {
		t.Fatal("opening the emoji picker should load the catalog")
	}
	if got := m.View(); !strings.Contains(got, "Thread · Channel") || !strings.Contains(got, "Reactions") || !strings.Contains(got, "Loading Loop emoji") {
		t.Fatalf("picker should keep the conversation visible while loading: %q", got)
	}
	m.draftFiles[m.draftKey()] = []draftAttachment{{Path: "/fixture.png"}}
	m.Update(tea.WindowSizeMsg{Width: 24, Height: 9})
	if got := m.View(); len(strings.Split(got, "\n")) != 9 || !strings.Contains(got, "↑↓ Enter Tab Esc") {
		t.Fatalf("picker and attachment row should fit in a small window: %q", got)
	}
	m.Update(tea.WindowSizeMsg{Width: 90, Height: 22})
	_, nextPage := m.Update(m.loadEmojiPage(0)())
	if nextPage == nil {
		t.Fatal("emoji picker did not request a second page")
	}
	m.Update(nextPage())
	for _, name := range customNames {
		m.reactionEmojiQuery.Reset()
		key(":" + name + ":")
		if got := m.View(); !strings.Contains(got, "› :"+name+":") {
			t.Fatalf("search did not find %s on the second page: %q", name, got)
		}
		if m.key(tea.KeyMsg{Type: tea.KeyEnter}) != nil {
			t.Fatal("custom reaction should be queued while a reaction is being saved")
		}
	}
	m.key(tea.KeyMsg{Type: tea.KeyEsc})
	for i := 0; i < 8; i++ {
		if first == nil {
			t.Fatalf("queued reaction %d was lost", i+1)
		}
		_, first = m.Update(first())
	}
	if m.reactionTarget.ID != "" || first == nil {
		t.Fatal("Esc should close the picker and refresh history once queued reactions finish")
	}
	for _, want := range []string{
		"add +1", "remove +1", "add back", "add white_check_mark", "add eyes",
		"add pupupu", "add sbogom", "add sad_pepe", "add thank_you", "add press_f",
	} {
		select {
		case got := <-applied:
			if got != want {
				t.Fatalf("got %q, want %q", got, want)
			}
		default:
			t.Fatalf("missing %q", want)
		}
	}
	summary := reactionSummary(m.discussion.selectedPost().Metadata.Reactions)
	if !strings.Contains(summary, "👍 1  :ship_it: 1  🔙 1  ✅ 1  👀 1") || !strings.Contains(summary, ":press_f: 1") {
		t.Fatalf("reactions should coexist on the post: %q", summary)
	}
	key("z")
	if got := m.View(); !strings.Contains(got, "Enter 👍 1") || !strings.Contains(got, "9 👀") {
		t.Fatalf("quick picker should suggest the existing reaction alongside the digit shortcuts: %q", got)
	}
	m.key(tea.KeyMsg{Type: tea.KeyRight})
	if got := m.View(); !strings.Contains(got, "Enter :ship_it: 1") {
		t.Fatalf("custom reaction should be selectable from the suggestions: %q", got)
	}
	join := m.key(tea.KeyMsg{Type: tea.KeyEnter})
	m.key(tea.KeyMsg{Type: tea.KeyEnter})
	m.Update(join())
	if got := <-applied; got != "add ship_it" {
		t.Fatalf("the suggestion must use the exact server emoji name: %q", got)
	}
	if len(m.reactionQueue) != 0 || strings.Contains(m.View(), "Enter :ship_it:") {
		t.Fatal("already joined reactions should disappear from the suggestions")
	}

}
