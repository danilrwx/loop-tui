package main

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

const activityPageSize = 30

type activityItem struct {
	post post
	at   int64
	by   string
	icon string
}

type activityPage struct {
	items  []activityItem
	cursor int64
}

type activityResult struct {
	request uint64
	page    activityPage
	older   bool
	err     error
}

type activityJumpResult struct {
	request uint64
	messageLocation
}

func (c *client) loadActivity(ctx context.Context, reactions bool, cursor int64) (activityPage, error) {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	var result struct {
		postList
		LastReaction int64 `json:"last_reaction_updateat"`
	}
	var page activityPage
	if reactions {
		if cursor == 0 {
			cursor = time.Now().UnixMilli()
		}
		q := url.Values{
			"per_page":               {strconv.Itoa(activityPageSize)},
			"last_reaction_updateat": {strconv.FormatInt(cursor, 10)},
		}
		path := "/users/" + url.PathEscape(c.me.ID) + "/posts/reactioned?" + q.Encode()
		if err := c.request(ctx, http.MethodGet, path, nil, &result); err != nil {
			return page, err
		}
		if len(result.Order) > 0 && result.LastReaction < cursor {
			page.cursor = result.LastReaction
		}
	} else {
		terms := c.mentionSearchTerms()
		if terms == "" {
			return page, nil
		}
		_, offset := time.Now().Zone()
		payload := map[string]any{
			"terms": terms, "is_or_search": true, "page": cursor,
			"per_page": activityPageSize, "time_zone_offset": offset,
		}
		if err := c.request(ctx, http.MethodPost, "/posts/search", payload, &result); err != nil {
			return page, err
		}
		if len(result.Order) >= activityPageSize {
			page.cursor = cursor + 1
		}
	}
	var peopleIDs []string
	var posts []post
	for _, id := range result.Order {
		p := result.Posts[id]
		if p.ID == "" || p.DeletedAt != 0 {
			continue
		}
		item := activityItem{post: p, at: p.CreatedAt}
		if reactions {
			var latest reaction
			for _, r := range p.Metadata.Reactions {
				if r.UserID != "" && r.UserID != c.me.ID && (latest.UserID == "" || r.UpdatedAt >= latest.UpdatedAt) {
					latest = r
				}
			}
			if latest.UserID == "" {
				continue
			}
			if latest.UpdatedAt != 0 {
				item.at = latest.UpdatedAt
			}
			item.by, item.icon = latest.UserID, reactionGlyph(latest.EmojiName)
		} else {
			item.by = p.UserID
		}
		peopleIDs = append(peopleIDs, item.by)
		posts = append(posts, p)
		page.items = append(page.items, item)
	}
	c.addAuthors(ctx, posts)
	people := c.users(ctx, peopleIDs)
	for i, item := range page.items {
		who := people[item.by].label()
		if reactions {
			who += " " + item.icon
		}
		page.items[i].by = who
		page.items[i].post = posts[i]
	}
	return page, nil
}

func (c *client) mentionSearchTerms() string {
	var terms []string
	seen := make(map[string]bool)
	keys := strings.Split(c.me.NotifyProps["mention_keys"], ",")
	if c.me.Username != "" {
		keys = append(keys, "@"+c.me.Username)
	}
	if c.me.NotifyProps["first_name"] == "true" {
		keys = append(keys, c.me.FirstName)
	}
	for _, key := range keys {
		key = strings.TrimSpace(key)
		switch strings.ToLower(key) {
		case "", "@all", "@channel", "@here":
			continue
		}
		if !seen[key] {
			terms = append(terms, `"`+strings.ReplaceAll(key, `"`, "")+`"`)
			seen[key] = true
		}
	}
	return strings.Join(terms, " ")
}

func (m *app) receiveActivity(msg activityResult) tea.Cmd {
	if msg.request != m.timeline.request || m.active != activityID {
		return nil
	}
	view := &m.timeline
	view.loading = false
	if msg.err != nil {
		view.pendingOlder, view.pendingPage = 0, false
		m.status = "Load activity: " + msg.err.Error()
		return nil
	}
	view.before = ""
	if msg.page.cursor != 0 {
		view.before = strconv.FormatInt(msg.page.cursor, 10)
	}
	var posts []post
	if msg.older {
		posts = view.posts
	}
	seen := make(map[string]bool, len(posts))
	for _, p := range posts {
		seen[p.ID] = true
	}
	for _, item := range msg.page.items {
		p := item.post
		if seen[p.ID] {
			continue
		}
		seen[p.ID] = true
		p.feedAt = item.at
		if m.feedFilter == 1 {
			p.feedLabel = item.by
		}
		posts = append(posts, p)
	}
	wasEmpty := len(view.posts) == 0
	view.replace(posts)
	return m.continuePaging(wasEmpty)
}

func (m *app) openActivityPost(p post) tea.Cmd {
	if p.ID == "" {
		return nil
	}
	ch := m.channelByID(p.ChannelID)
	p.feedAt, p.feedLabel = 0, ""
	m.sequence++
	request := m.sequence
	m.activityJump = request
	m.status = "Opening message…"
	return func() tea.Msg {
		return activityJumpResult{request, m.api.locateMessage(m.ctx, p, ch)}
	}
}

func (m *app) receiveActivityJump(msg activityJumpResult) tea.Cmd {
	if m.active != activityID || m.activityJump != msg.request {
		return nil
	}
	m.activityJump = 0
	m.status = msg.openError()
	if m.status != "" {
		return nil
	}
	return m.openMessageLocation(msg.messageLocation)
}
