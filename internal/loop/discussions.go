package loop

import (
	"cmp"
	"context"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type followedThread struct {
	Post         post
	TeamID       string `json:"-"`
	Unread       int    `json:"unread_replies"`
	Mentions     int    `json:"unread_mentions"`
	LastViewed   int64  `json:"last_viewed_at"`
	Replies      int    `json:"reply_count"`
	LastReply    int64  `json:"last_reply_at"`
	Participants []person
}

type discussionsResult struct {
	items []followedThread
	more  bool
	err   error
}

type followThreadResult struct {
	root   string
	follow bool
	err    error
}

func threadPath(team string) string {
	return "/users/me/teams/" + url.PathEscape(team) + "/threads"
}

func (c *client) followedThreads(ctx context.Context, firstPage bool) ([]followedThread, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 18*time.Second)
	defer cancel()

	pageSize := 100
	if firstPage {
		pageSize = 40
	}

	var (
		all  []followedThread
		more bool
	)

	seen := make(map[string]bool)

	for _, team := range c.teamIDs {
		query := url.Values{"per_page": {strconv.Itoa(pageSize)}, "threadsOnly": {"true"}, "extended": {"true"}}

		for {
			var page struct{ Threads []followedThread }

			err := c.request(ctx, http.MethodGet, threadPath(team)+"?"+query.Encode(), nil, &page)
			if err != nil {
				return all, false, err
			}

			for _, item := range page.Threads {
				if item.Post.ID == "" || item.Post.DeletedAt != 0 || seen[item.Post.ID] {
					continue
				}

				item.TeamID = team
				item.Post.Replies = max(item.Post.Replies, item.Replies)
				all = append(all, item)
				seen[item.Post.ID] = true
			}

			if len(page.Threads) < pageSize {
				break
			}

			if firstPage {
				more = true
				break
			}

			last := page.Threads[len(page.Threads)-1].Post.ID
			if last == "" || last == query.Get("before") {
				break
			}

			query.Set("before", last)
		}
	}

	sortDiscussions(all)

	posts := make([]post, len(all))
	for i, item := range all {
		posts[i] = item.Post
	}

	c.addAuthors(ctx, posts)

	for i := range all {
		all[i].Post = posts[i]
	}

	return all, more, nil
}

func sortDiscussions(items []followedThread) {
	slices.SortStableFunc(items, func(a, b followedThread) int {
		return cmp.Compare(max(b.LastReply, b.Post.CreatedAt), max(a.LastReply, a.Post.CreatedAt))
	})
}

func mergeRecentDiscussions(recent, cached []followedThread) []followedThread {
	seen := make(map[string]bool, len(recent))
	for _, item := range recent {
		seen[item.Post.ID] = true
	}

	for _, item := range cached {
		if !seen[item.Post.ID] {
			recent = append(recent, item)
		}
	}

	sortDiscussions(recent)

	return recent
}

func (m *app) loadDiscussions(firstPage bool) tea.Cmd {
	m.discussionsLoading = true
	if m.active == discussionsID {
		m.timeline.loading = true
	}

	cachePath := m.discussionCachePath

	var previous []followedThread
	if firstPage && m.discussionsComplete {
		previous = slices.Clone(m.followed)
	}

	return func() tea.Msg {
		items, more, err := m.api.followedThreads(m.ctx, firstPage)
		if err == nil && more && previous != nil {
			items, more = mergeRecentDiscussions(items, previous), false
		}

		if err == nil && !more && cachePath != "" {
			saveDiscussionCache(cachePath, items)
		}

		return discussionsResult{items, more, err}
	}
}

func (m *app) receiveDiscussions(msg discussionsResult) tea.Cmd {
	m.discussionsLoading = false
	if m.active == discussionsID {
		m.timeline.loading = false
		if msg.err != nil {
			m.status = "Load discussions: " + msg.err.Error()
		} else if strings.HasPrefix(m.status, "Load discussions: ") {
			m.status = ""
		}
	}

	if msg.err == nil {
		m.discussionsComplete = !msg.more
		if !msg.more {
			m.discussionsUpdatedAt = time.Now()
		}
	}

	if msg.err == nil || m.followed == nil && len(msg.items) > 0 {
		m.followed = msg.items
		if m.followed == nil {
			m.followed = []followedThread{}
		}

		if m.active == discussionsID {
			m.applyDiscussionFilter()
		}
	}

	if msg.more {
		return m.loadDiscussions(false)
	}

	return nil
}

func (m *app) applyDiscussionFilter() {
	var posts []post

	for _, item := range m.followed {
		switch m.feedFilter {
		case 1:
			if item.Unread == 0 && item.Mentions == 0 && item.Post.ID != m.root {
				continue
			}
		case 2:
			me := m.api.me.ID
			if item.Post.UserID != me && !slices.ContainsFunc(item.Participants, func(p person) bool { return p.ID == me }) {
				continue
			}
		}

		p := item.Post
		p.feedAt = item.LastReply
		posts = append(posts, p)
	}

	m.timeline.replace(posts)
}

func (m *app) postThreadSignal(ch channel, id string) threadSignal {
	signal := ch.threadSignal(id)
	if signal.root == "" && m.active == discussionsID {
		for _, item := range m.followed {
			if item.Post.ID == id {
				return threadSignal{
					root: id, team: item.TeamID, unread: max(item.Unread, item.Mentions),
					mentions: item.Mentions, lastViewed: item.LastViewed,
				}
			}
		}
	}

	return signal
}

func (m *app) followSelectedThread(follow bool) tea.Cmd {
	ch := m.activeChannel()

	p := m.timeline.selectedPost()
	if isFeed(m.active) && m.focus != threadPane {
		ch = m.channelByID(p.ChannelID)
	}

	if m.focus == threadPane {
		p.ID = m.root
		p.RootID = ""
	}

	if m.reader.ID != "" {
		p = m.reader
	}

	root := p.RootID
	if root == "" {
		root = p.ID
	}

	if root == "" || ch.ID == "" {
		return nil
	}

	team := m.postThreadSignal(ch, root).team
	if team == "" {
		team = ch.TeamID
	}

	if team == "" && len(m.api.teamIDs) > 0 {
		team = m.api.teamIDs[0]
	}

	if team == "" {
		m.status = "No team available for this discussion"
		return nil
	}

	return m.setThreadFollowing(team, root, follow)
}

func (m *app) setThreadFollowing(team, root string, follow bool) tea.Cmd {
	if m.followChanging {
		return nil
	}

	m.followChanging = true

	return func() tea.Msg {
		method := http.MethodDelete
		if follow {
			method = http.MethodPut
		}

		err := m.api.request(m.ctx, method, threadPath(team)+"/"+url.PathEscape(root)+"/following", nil, nil)

		return followThreadResult{root, follow, err}
	}
}

func (m *app) receiveFollowChange(result followThreadResult) tea.Cmd {
	m.followChanging = false
	if result.err != nil {
		m.status = "Discussion subscription: " + result.err.Error()
		return nil
	}

	if result.follow {
		m.status = "Following discussion"
	} else {
		m.status = "Unfollowed discussion"
		m.clearThread(result.root)
		m.removeFollowedThread(result.root)
	}

	return m.loadChannels()
}

func (m *app) removeFollowedThread(root string) {
	m.followed = slices.DeleteFunc(m.followed, func(item followedThread) bool { return item.Post.ID == root })
	if m.active == discussionsID {
		m.applyDiscussionFilter()
	}
}
