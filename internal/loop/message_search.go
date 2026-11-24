package loop

import (
	"cmp"
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/danilrwx/telega/tui"
)

const searchPageSize = 40

type searchPostsResult struct {
	dialog     *tui.MessageSearch
	revision   uint64
	posts      []post
	page, next int
	err        error
}

type searchJumpResult struct {
	dialog   *tui.MessageSearch
	revision uint64
	messageLocation
}

type messageLocation struct {
	hit     post
	channel channel
	page    historyPage
	err     error
}

func (m *app) beginMessageSearch(global bool) {
	scope := m.activeChannel()
	if isFeed(scope.ID) {
		scope = channel{}
	}

	label := scope.DisplayName
	if m.focus == threadPane {
		label = "Thread · " + label
	}

	m.messageSearchScope, m.messageSearchRoot = scope, ""
	if m.focus == threadPane {
		m.messageSearchRoot = m.root
	}

	m.messageSearch = &tui.MessageSearch{Global: global || scope.ID == "", Scope: label}
	m.messageHits, m.messageSearchPage = nil, 0
}

func (m *app) messageSearchKey(key tea.KeyMsg) tea.Cmd {
	name, text := attachmentInputKey(key)

	dialog := m.messageSearch
	switch action := dialog.Key(name, text); action {
	case "close":
		m.messageSearch = nil
		m.messageHits = nil
	case "open":
		hit := m.messageHits[dialog.Selected]
		ch := m.channelByID(hit.ChannelID)
		rev := dialog.Revision
		dialog.Busy = true

		return func() tea.Msg {
			return searchJumpResult{dialog, rev, m.api.locateMessage(m.ctx, hit, ch)}
		}
	case "search", "more":
		page := 0
		if action == "more" {
			page = m.messageSearchPage
		} else {
			m.messageHits, m.messageSearchPage = nil, 0
		}

		scope, root := m.messageSearchScope, m.messageSearchRoot
		if dialog.Global {
			scope, root = channel{}, ""
		}

		rev, term := dialog.Revision, dialog.Query()

		return func() tea.Msg {
			posts, next, err := m.api.searchLoopPosts(m.ctx, term, scope, root, page)
			return searchPostsResult{dialog: dialog, revision: rev, posts: posts, page: page, next: next, err: err}
		}
	}

	return nil
}

func (m *app) receiveSearchPosts(result searchPostsResult) {
	d := m.messageSearch
	if d == nil || d != result.dialog || d.Revision != result.revision {
		return
	}

	if result.err != nil {
		d.SetResults(nil, result.page > 0, result.page > 0, "Search: "+result.err.Error())
		return
	}

	m.messageSearchPage = result.next

	seen := make(map[string]bool, len(m.messageHits))
	for _, hit := range m.messageHits {
		seen[hit.ID] = true
	}

	labels := make([]string, 0, len(result.posts))
	for _, p := range result.posts {
		if seen[p.ID] {
			continue
		}

		seen[p.ID] = true
		m.messageHits = append(m.messageHits, p)

		label := time.UnixMilli(p.CreatedAt).Local().Format("02 Jan 15:04")
		if ch := m.channelByID(p.ChannelID); d.Global && ch.DisplayName != "" {
			label = ch.DisplayName + " · " + label
		}

		if p.Author != "" {
			label += " · " + p.Author
		}

		label += " · " + strings.Join(strings.Fields(p.Text), " ")
		labels = append(labels, label)
	}

	d.SetResults(labels, result.next >= 0, result.page > 0, "")
}

func (c *client) locateMessage(ctx context.Context, hit post, ch channel) messageLocation {
	result := messageLocation{hit: hit, channel: ch}
	if result.channel.ID == "" {
		var item directoryChannel

		result.err = c.request(ctx, http.MethodGet, "/channels/"+url.PathEscape(hit.ChannelID), nil, &item)
		if result.err != nil {
			return result
		}

		result.channel = channel{
			ID: hit.ChannelID, Name: item.Name, DisplayName: item.DisplayName, TeamID: item.TeamID,
			Direct: item.Type == "D" || item.Type == "G",
		}
		if result.channel.DisplayName == "" {
			result.channel.DisplayName = item.Name
		}
	}

	if hit.RootID != "" {
		result.page.posts, result.err = c.thread(ctx, hit.RootID)
	} else {
		result.page, result.err = c.postsAround(ctx, hit)
	}

	return result
}

func (m *app) receiveSearchJump(result searchJumpResult) tea.Cmd {
	d := m.messageSearch
	if d == nil || d != result.dialog || d.Revision != result.revision {
		return nil
	}

	d.Busy = false
	if err := result.openError(); err != "" {
		d.Status = err
		return nil
	}

	m.messageSearch, m.messageHits = nil, nil

	return m.openMessageLocation(result.messageLocation)
}

func (r messageLocation) openError() string {
	if r.err != nil {
		return "Open message: " + r.err.Error()
	}

	if r.index() < 0 {
		return "Message is unavailable"
	}

	return ""
}

func (r messageLocation) index() int {
	return slices.IndexFunc(r.page.posts, func(p post) bool { return p.ID == r.hit.ID })
}

func (m *app) openMessageLocation(result messageLocation) tea.Cmd {
	index := result.index()
	m.reader = post{}

	changed := m.active != result.channel.ID
	if changed {
		if m.channelByID(result.channel.ID).ID == "" {
			m.channels = append(m.channels, result.channel)
		}

		m.active, m.channelCursor = result.channel.ID, result.channel.ID
		m.feedFilter, m.activityJump = 0, 0
		m.timeline, m.discussion = conversation{}, conversation{}
	}

	m.fullPane = false
	if result.hit.RootID != "" {
		m.root, m.focus = result.hit.RootID, threadPane
		m.threadChannelID = result.channel.ID
		m.discussion = conversation{posts: result.page.posts, selected: index}
		m.draft.SetWidth(max(8, m.conversationWidth()))

		if changed {
			return m.loadHistory(false, false, false, true)
		}

		return nil
	}

	m.root, m.focus = "", channelPane
	m.discussion = conversation{}
	m.timeline = conversation{posts: result.page.posts, selected: index, before: result.page.before, anchored: true}
	m.draft.SetWidth(max(8, m.conversationWidth()))

	return nil
}

func (c *client) searchLoopPosts(ctx context.Context, term string, scope channel, root string, page int) ([]post, int, error) {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()

	if scope.ID != "" && !scope.Direct && scope.Name != "" {
		term += " in:" + scope.Name
	}

	for range 3 {
		items, more, err := c.searchLoopPage(ctx, term, scope, page)
		if err != nil {
			return nil, -1, err
		}

		if scope.ID != "" {
			items = slices.DeleteFunc(items, func(p post) bool {
				return p.ChannelID != scope.ID || root != "" && p.ID != root && p.RootID != root
			})
		}

		if !more {
			c.addAuthors(ctx, items)
			return items, -1, nil
		}

		page++

		if len(items) > 0 {
			c.addAuthors(ctx, items)
			return items, page, nil
		}
	}

	return nil, page, nil
}

func (c *client) searchLoopPage(ctx context.Context, term string, scope channel, page int) ([]post, bool, error) {
	payload := map[string]any{"terms": term, "is_or_search": false, "page": page, "per_page": searchPageSize}
	teamPath := func(team string) string { return "/teams/" + url.PathEscape(team) + "/posts/search" }

	var items []post

	seen := make(map[string]bool)

	fetch := func(path string) (bool, error) {
		var result postList
		if err := c.request(ctx, http.MethodPost, path, payload, &result); err != nil {
			return false, err
		}

		for _, id := range result.Order {
			if p := result.Posts[id]; p.ID != "" && p.DeletedAt == 0 && !seen[id] {
				items = append(items, p)
				seen[id] = true
			}
		}

		return len(result.Order) >= searchPageSize, nil
	}
	if scope.TeamID != "" {
		more, err := fetch(teamPath(scope.TeamID))
		return items, more, err
	}

	more, err := fetch("/posts/search")
	if !errors.Is(err, errNotFound) || len(c.teamIDs) == 0 {
		return items, more, err
	}

	more = false

	for _, team := range c.teamIDs {
		next, teamErr := fetch(teamPath(team))
		if teamErr != nil {
			return nil, false, teamErr
		}

		more = more || next
	}

	slices.SortStableFunc(items, func(a, b post) int { return cmp.Compare(b.CreatedAt, a.CreatedAt) })

	return items, more, nil
}

func (c *client) postsAround(ctx context.Context, hit post) (historyPage, error) {
	var page historyPage

	page.posts = append(page.posts, hit)
	path := "/channels/" + url.PathEscape(hit.ChannelID) + "/posts?"
	seen := map[string]bool{hit.ID: true}

	var oldest post

	for _, direction := range []string{"before", "after"} {
		q := url.Values{direction: {hit.ID}, "per_page": {"30"}, "collapsedThreads": {"true"}, "include_file_metadata": {"true"}}

		var result postList
		if err := c.request(ctx, http.MethodGet, path+q.Encode(), nil, &result); err != nil {
			return historyPage{}, err
		}

		for _, id := range result.Order {
			p := result.Posts[id]
			if p.ID == "" {
				continue
			}

			if direction == "before" && (oldest.ID == "" || p.CreatedAt < oldest.CreatedAt) {
				oldest = p
			}

			if !seen[id] && p.RootID == "" && p.DeletedAt == 0 {
				seen[id] = true

				page.posts = append(page.posts, p)
			}
		}

		if direction == "before" && len(result.Order) >= 30 {
			page.before = oldest.ID
		}
	}

	slices.SortStableFunc(page.posts, func(a, b post) int { return cmp.Compare(b.CreatedAt, a.CreatedAt) })
	c.addAuthors(ctx, page.posts)

	return page, nil
}
