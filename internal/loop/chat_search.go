package loop

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/danilrwx/telega/tui"
)

type chatSearchChoice struct {
	channel channel
	person  person
	join    bool
}

func (c chatSearchChoice) caption() string {
	switch {
	case c.person.ID != "":
		return c.person.label() + " · @" + c.person.Username
	case c.join:
		return c.channel.DisplayName + "  ·  Join channel"
	case c.channel.Username != "":
		return c.channel.DisplayName + " · @" + c.channel.Username
	default:
		return c.channel.DisplayName
	}
}

type chatSearchTick uint64

type chatSearchResult struct {
	id    uint64
	items []chatSearchChoice
	err   error
}

type chatSearchOpened struct {
	channel channel
	err     error
}

type directoryChannel struct {
	ID, Name, Type string
	DisplayName    string `json:"display_name"`
	TeamID         string `json:"team_id"`
}

func (c *client) directorySearch(ctx context.Context, query string) ([]chatSearchChoice, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	term, usersOnly := strings.CutPrefix(query, "@")
	term = strings.TrimSpace(term)
	payload := map[string]any{"term": term, "limit": 30}

	var (
		items []chatSearchChoice
		users []person
	)

	err := c.request(ctx, http.MethodPost, "/users/search", payload, &users)
	for _, user := range users {
		if user.ID != "" && user.Username != "" && user.ID != c.me.ID {
			items = append(items, chatSearchChoice{person: user})
		}
	}

	if !usersOnly {
		for _, team := range c.teamIDs {
			var matches []directoryChannel

			path := "/teams/" + url.PathEscape(team) + "/channels/search"
			if searchErr := c.request(ctx, http.MethodPost, path, map[string]string{"term": term}, &matches); searchErr != nil {
				err = errors.Join(err, searchErr)
				continue
			}

			for _, item := range matches {
				if item.ID == "" || item.Type != "O" {
					continue
				}

				name := item.DisplayName
				if name == "" {
					name = item.Name
				}

				if teamName := c.teamNames[team]; len(c.teamIDs) > 1 && teamName != "" {
					name += " · " + teamName
				}

				ch := channel{ID: item.ID, Name: item.Name, DisplayName: name, TeamID: team}
				items = append(items, chatSearchChoice{channel: ch, join: true})
			}
		}
	}

	return items, err
}

func (m *app) chatSearchChoices() []chatSearchChoice {
	seen := make(map[string]bool)
	knownUsers := make(map[string]channel)

	var items []chatSearchChoice

	for _, ch := range feedChannels {
		if tui.ChatMatches(ch.DisplayName, "", m.query.Value()) {
			items = append(items, chatSearchChoice{channel: ch})
		}
	}

	for _, ch := range m.channels {
		if tui.ChatMatches(ch.DisplayName+" "+ch.Name, ch.Username, m.query.Value()) {
			items = append(items, chatSearchChoice{channel: ch})
			seen[ch.ID] = true
		}

		if ch.Direct && m.api != nil {
			knownUsers[m.api.directPeer(ch.Name)] = ch
		}
	}

	userOnly := strings.HasPrefix(strings.TrimSpace(m.query.Value()), "@")
	for _, item := range m.chatSearchResults {
		if item.person.ID != "" {
			if userOnly && !tui.ChatMatches("", item.person.Username, m.query.Value()) {
				continue
			}

			if ch, known := knownUsers[item.person.ID]; known {
				if ch.ID != "" && !seen[ch.ID] {
					ch.Username = item.person.Username
					items = append(items, chatSearchChoice{channel: ch})
					seen[ch.ID] = true
				}
			} else {
				items = append(items, item)
				knownUsers[item.person.ID] = channel{}
			}
		} else if !seen[item.channel.ID] {
			items = append(items, item)
			seen[item.channel.ID] = true
		}
	}

	return items
}

func (m *app) chatSearchChanged() tea.Cmd {
	m.chatSearchIndex, m.chatSearchStatus = 0, ""
	m.chatSearchResults = nil
	m.chatSearchID++
	m.ensureChannelCursor()

	if m.api == nil || utf8.RuneCountInString(strings.TrimPrefix(strings.TrimSpace(m.query.Value()), "@")) < 2 {
		return nil
	}

	m.chatSearchStatus = "Searching Loop…"
	id := m.chatSearchID

	return tea.Tick(240*time.Millisecond, func(time.Time) tea.Msg { return chatSearchTick(id) })
}

func (m *app) closeChatSearch() {
	m.filtering = false
	m.query.Reset()
	m.query.Blur()
	m.chatSearchID++
	m.chatSearchResults, m.chatSearchJoin, m.chatSearchStatus = nil, channel{}, ""
}

func (m *app) chatSearchKey(key tea.KeyMsg) tea.Cmd {
	if m.chatSearchOpening {
		return nil
	}

	if m.chatSearchJoin.ID != "" {
		switch commandKeyName(key) {
		case "esc", "n":
			m.chatSearchJoin = channel{}
		case "y":
			return m.openDirectoryChat(chatSearchChoice{channel: m.chatSearchJoin, join: true})
		}

		return nil
	}

	switch key.String() {
	case "esc", "ctrl+k":
		m.closeChatSearch()
		m.ensureChannelCursor()
	case "enter":
		items := m.chatSearchChoices()
		if len(items) == 0 {
			return nil
		}

		item := items[min(m.chatSearchIndex, len(items)-1)]
		if item.join {
			m.chatSearchJoin = item.channel
			return nil
		}

		if item.person.ID != "" {
			return m.openDirectoryChat(item)
		}

		m.closeChatSearch()
		m.channelCursor = item.channel.ID

		return m.openChannel()
	case "up", "ctrl+p":
		m.chatSearchIndex = max(0, m.chatSearchIndex-1)
	case "down", "ctrl+n":
		m.chatSearchIndex = min(m.chatSearchIndex+1, max(0, len(m.chatSearchChoices())-1))
	default:
		old := m.query.Value()

		var cmd tea.Cmd

		m.query, cmd = m.query.Update(key)
		if old != m.query.Value() {
			return tea.Batch(cmd, m.chatSearchChanged())
		}

		return cmd
	}

	return nil
}

func (m *app) openDirectoryChat(item chatSearchChoice) tea.Cmd {
	m.chatSearchOpening = true
	m.chatSearchStatus = "Opening chat…"

	return func() tea.Msg {
		if item.person.ID != "" {
			var reply directoryChannel

			err := m.api.request(m.ctx, http.MethodPost, "/channels/direct", []string{m.api.me.ID, item.person.ID}, &reply)
			if err == nil && reply.ID == "" {
				err = errors.New("server did not return a channel")
			}

			return chatSearchOpened{channel: channel{
				ID: reply.ID, Name: reply.Name, Direct: true,
				DisplayName: item.person.label(), Username: item.person.Username,
			}, err: err}
		}

		path := "/channels/" + url.PathEscape(item.channel.ID) + "/members"
		err := m.api.request(m.ctx, http.MethodPost, path, map[string]string{"user_id": m.api.me.ID}, nil)

		return chatSearchOpened{channel: item.channel, err: err}
	}
}

func (m *app) receiveChatSearchOpened(result chatSearchOpened) tea.Cmd {
	m.chatSearchOpening = false

	m.chatSearchJoin = channel{}
	if result.err != nil {
		m.chatSearchStatus = "Open chat: " + result.err.Error()
		return nil
	}

	m.closeChatSearch()

	if !slices.ContainsFunc(m.channels, func(ch channel) bool { return ch.ID == result.channel.ID }) {
		m.channels = append(m.channels, result.channel)
	}

	m.channelCursor = result.channel.ID

	return tea.Batch(m.openChannel(), m.loadChannels())
}

func (m *app) renderChatSearch(width, height int, row func(string, bool, bool, bool)) {
	if m.chatSearchJoin.ID != "" && !m.chatSearchOpening {
		row("Join channel?", true, false, false)

		for _, line := range tui.Wrap(m.chatSearchJoin.DisplayName, width, 2) {
			row(line, false, false, false)
		}

		row("", false, false, false)
		row(tui.Clip("y join  Esc back", width), false, false, true)

		return
	}

	items := m.chatSearchChoices()

	labels := make([]string, 0, len(items))
	for _, item := range items {
		labels = append(labels, item.caption())
	}

	tui.ChatSearchList("Find a chat", m.query.Value(), labels, m.chatSearchIndex, width, height, m.chatSearchStatus, row)
}
