package loop

import (
	"fmt"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/danilrwx/telega/tui"
)

type unreadChoice struct {
	channel channel
	signal  threadSignal
}

func (m *app) unreadThreadChoices() []unreadChoice {
	ch := m.conversationChannel(m.focus == threadPane)
	if m.focus == channelsPane {
		ch = m.channelByID(m.channelCursor)
	}

	all := ch.ID == discussionsID || m.active == discussionsID && m.focus != channelsPane

	var items []unreadChoice

	for _, candidate := range m.channels {
		if !all && candidate.ID != ch.ID {
			continue
		}

		for _, signal := range candidate.Threads {
			items = append(items, unreadChoice{candidate, signal})
		}
	}

	return items
}

func (m *app) openUnreadChoice(item unreadChoice) tea.Cmd {
	choices := m.unreadThreadChoices()

	next := ""
	if index := slices.IndexFunc(choices, func(choice unreadChoice) bool { return choice.signal.root == item.signal.root }); index >= 0 {
		next = choices[(index+1)%len(choices)].signal.root
	}

	var load tea.Cmd
	if m.focus == channelsPane {
		load = m.openChannel()
	}

	m.unreadNext = next

	p := post{ID: item.signal.root, ChannelID: item.channel.ID}
	if index := slices.IndexFunc(m.timeline.posts, func(p post) bool { return p.ID == item.signal.root }); index >= 0 && m.active == item.channel.ID {
		m.timeline.selectIndex(index)
		p = m.timeline.posts[index]
	}

	return tea.Batch(load, m.openThread(p, false))
}

func (m *app) showUnreadThreads() {
	m.unreadChoices = m.unreadThreadChoices()

	m.unreadSelection = 0
	if len(m.unreadChoices) == 0 {
		m.status = "No unread threads in this scope"
		m.unreadChoices = nil

		return
	}

	if current := slices.IndexFunc(m.unreadChoices, func(item unreadChoice) bool { return item.signal.root == m.root }); current >= 0 {
		m.unreadSelection = current
	}
}

func (m *app) unreadThreadsKey(key tea.KeyMsg) tea.Cmd {
	switch commandKeyName(key) {
	case "esc", "q", "h":
		m.unreadChoices = nil
	case "j", "down":
		m.unreadSelection = min(len(m.unreadChoices)-1, m.unreadSelection+1)
	case "k", "up":
		m.unreadSelection = max(0, m.unreadSelection-1)
	case "enter", "l", "right":
		item := m.unreadChoices[m.unreadSelection]
		m.unreadChoices = nil

		return m.openUnreadChoice(item)
	}

	return nil
}

func (m *app) renderUnreadThreads(width, height int, row func(string, bool, bool, bool)) {
	choices := make([]string, len(m.unreadChoices))
	for i, item := range m.unreadChoices {
		preview := strings.Join(strings.Fields(item.signal.preview), " ")
		if preview == "" {
			preview = "Thread without text"
		}

		badge := fmt.Sprintf("↳%d", item.signal.unread)
		choices[i] = tui.Clip(item.channel.DisplayName+" · "+preview, width-5-ansi.StringWidth(badge)) + " " + badge
	}

	status := "Opening keeps the thread unread; press m in the thread to mark it read"
	if !m.preferences.ManualRead {
		status = "Opening marks the thread read"
	}

	tui.ListDialog("Unread threads", choices, m.unreadSelection, width, height, status, "jk choose  Enter open  Esc back", row)
}

func (m *app) markOpenThreadRead() tea.Cmd {
	through := m.discussion.newest()
	if m.root == "" || through == 0 {
		return nil
	}

	ch := m.conversationChannel(true)

	signal := m.postThreadSignal(ch, m.root)
	if signal.team == "" {
		signal.team = ch.TeamID
	}

	if signal.team == "" && len(m.api.teamIDs) > 0 {
		signal.team = m.api.teamIDs[0]
	}

	if signal.team == "" {
		m.status = "No team available for this thread"
		return nil
	}

	signal.root = m.root
	m.status = ""

	return func() tea.Msg {
		return readResult{channelID: ch.ID, rootID: signal.root, through: through, err: m.api.markThreadRead(m.ctx, signal, through)}
	}
}
