package main

import (
	"slices"

	tea "github.com/charmbracelet/bubbletea"
)

type deleteResult struct{ err error }

func (m *app) askDelete(p post) {
	if p.ID == "" {
		return
	}
	if !p.Own {
		m.status = "You can only delete your own messages"
		return
	}
	m.deleteTarget = p
	m.status = ""
}

func (m *app) deleteKey(key tea.KeyMsg) tea.Cmd {
	if m.deleting {
		return nil
	}
	switch commandKeyName(key) {
	case "esc", "n":
		m.deleteTarget = post{}
	case "y":
		m.deleting = true
		id := m.deleteTarget.ID
		return func() tea.Msg { return deleteResult{err: m.api.deletePost(m.ctx, id)} }
	}
	return nil
}

func (m *app) receiveDelete(result deleteResult) tea.Cmd {
	target := m.deleteTarget
	m.deleteTarget, m.deleting = post{}, false
	if result.err != nil {
		m.status = "Delete failed: " + result.err.Error()
		return nil
	}
	if m.reader.ID == target.ID {
		m.reader = post{}
	}
	m.timeline.removePost(target.ID)
	if target.RootID == "" {
		m.clearThread(target.ID)
	}
	if m.root == target.ID {
		m.root, m.discussion, m.fullPane = "", conversation{}, false
		if m.focus == threadPane {
			m.focus = channelPane
		}
	} else {
		m.discussion.removePost(target.ID)
	}
	m.removeFollowedThread(target.ID)
	m.status = "Message deleted"
	return tea.Batch(m.loadHistory(false, false, false, true), m.loadHistory(true, false, false, true))
}

func (c *conversation) removePost(id string) {
	index := slices.IndexFunc(c.posts, func(p post) bool { return p.ID == id })
	if index < 0 {
		return
	}
	c.posts = slices.Delete(c.posts, index, index+1)
	if c.selected > index {
		c.selected--
	} else if c.selected == index {
		c.bodyOffset, c.tableDetails = 0, false
	}
	c.selected = max(0, min(c.selected, len(c.posts)-1))
	if c.before == id {
		c.before = ""
		if len(c.posts) > 0 {
			c.before = c.posts[len(c.posts)-1].ID
		}
	}
}
