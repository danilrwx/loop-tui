package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestLoopDirectorySearchOpensDirectMessages(t *testing.T) {
	directCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /api/v4/users/search":
			fmt.Fprint(w, `[{"id":"mira","username":"mira_chen","first_name":"Mira","last_name":"Chen"}]`)
		case "POST /api/v4/channels/direct":
			directCalls++
			fmt.Fprint(w, `{"id":"dm","name":"me__mira","type":"D"}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := newClient(server.URL, "fixture")
	client.me.ID = "me"
	client.teamIDs = []string{"team"}
	m := newApp(t.Context(), client)
	m.width, m.height = 100, 26
	m.channels = []channel{{ID: "general", DisplayName: "General"}}
	m.key(tea.KeyMsg{Type: tea.KeyCtrlK})
	m.key(tea.KeyMsg{Type: tea.KeyRunes, Paste: true, Runes: []rune("@mira")})
	_, cmd := m.Update(chatSearchTick(m.chatSearchID))
	m.Update(cmd())
	if view := m.View(); !strings.Contains(view, "Mira Chen · @mira_chen") || !strings.Contains(view, "╭") {
		t.Fatalf("expected remote user in chat picker: %q", view)
	}
	cmd = m.key(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil || directCalls != 0 {
		t.Fatal("selecting the user should schedule opening the direct chat")
	}
	m.Update(cmd())
	if directCalls != 1 || m.filtering || m.query.Value() != "" || m.active != "dm" {
		t.Fatalf("did not open and restore the sidebar: active=%q query=%q", m.active, m.query.Value())
	}
}

func TestLoopDirectorySearchConfirmsJoiningPublicChannels(t *testing.T) {
	joins := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /api/v4/users/search":
			fmt.Fprint(w, "[]")
		case "POST /api/v4/teams/team/channels/search":
			fmt.Fprint(w, `[{"id":"public","name":"workshop","display_name":"Workshop","type":"O"},{"id":"private","type":"P"}]`)
		case "POST /api/v4/channels/public/members":
			joins++
			w.WriteHeader(http.StatusCreated)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := newClient(server.URL, "fixture")
	client.me.ID = "me"
	client.teamIDs = []string{"team"}
	m := newApp(t.Context(), client)
	m.key(tea.KeyMsg{Type: tea.KeyCtrlK})
	m.key(tea.KeyMsg{Type: tea.KeyRunes, Paste: true, Runes: []rune("workshop")})
	_, cmd := m.Update(chatSearchTick(m.chatSearchID))
	m.Update(cmd())
	if items := m.chatSearchChoices(); len(items) != 1 || items[0].channel.ID != "public" {
		t.Fatalf("expected one public channel, got %+v", items)
	}
	if cmd := m.key(tea.KeyMsg{Type: tea.KeyEnter}); cmd != nil || m.chatSearchJoin.ID != "public" || joins != 0 {
		t.Fatal("join must wait for explicit confirmation")
	}
	m.key(tea.KeyMsg{Type: tea.KeyEsc})
	if joins != 0 || !m.filtering || m.chatSearchJoin.ID != "" {
		t.Fatal("cancel should return to the results")
	}
	m.key(tea.KeyMsg{Type: tea.KeyEnter})
	cmd = m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	m.Update(cmd())
	if joins != 1 || m.active != "public" || m.filtering {
		t.Fatal("confirmation did not join and open the channel")
	}
}
