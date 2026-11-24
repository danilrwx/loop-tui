package main

import (
	"strconv"

	tea "github.com/charmbracelet/bubbletea"
)

const (
	discussionsID = "local:discussions"
	activityID    = "local:activity"
)

var feedChannels = [...]channel{
	{ID: discussionsID, DisplayName: "Discussions"},
	{ID: activityID, DisplayName: "Mentions & reactions"},
}

func isFeed(id string) bool {
	return id == discussionsID || id == activityID
}

func (m *app) loadFeed(older bool) tea.Cmd {
	if m.active == discussionsID {
		if m.discussionsLoading {
			m.timeline.loading = true
			return nil
		}
		return m.loadDiscussions(true)
	}
	if m.timeline.loading || older && m.timeline.before == "" {
		return nil
	}
	m.sequence++
	request := m.sequence
	m.timeline.request, m.timeline.loading = request, true
	var cursor int64
	if older {
		cursor, _ = strconv.ParseInt(m.timeline.before, 10, 64)
	}
	reactions := m.feedFilter == 1
	return func() tea.Msg {
		page, err := m.api.loadActivity(m.ctx, reactions, cursor)
		return activityResult{request, page, older, err}
	}
}

func (m *app) changeFeedFilter() tea.Cmd {
	m.status = ""
	if m.active == discussionsID {
		m.feedFilter = (m.feedFilter + 1) % 3
		m.applyDiscussionFilter()
		m.timeline.selectIndex(0)
		return nil
	}
	m.feedFilter = 1 - m.feedFilter
	m.timeline = conversation{}
	m.activityJump = 0
	return m.loadFeed(false)
}

func (m *app) feedTitle(ch channel) string {
	if ch.ID == discussionsID {
		return ch.DisplayName + " · " + [...]string{"Followed", "Unread", "Participating"}[m.feedFilter]
	}
	return ch.DisplayName + " · " + [...]string{"Mentions", "Reactions"}[m.feedFilter]
}

func (p post) displayTime() int64 {
	if p.feedAt > 0 {
		return p.feedAt
	}
	return p.CreatedAt
}
