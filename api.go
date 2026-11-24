package main

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

type person struct {
	ID          string
	Username    string
	FirstName   string            `json:"first_name"`
	LastName    string            `json:"last_name"`
	NotifyProps map[string]string `json:"notify_props"`
	Nickname    string
}

func (p person) label() string {
	for _, name := range []string{p.Nickname, strings.TrimSpace(p.FirstName + " " + p.LastName), p.Username} {
		if name != "" {
			return name
		}
	}
	return "Unknown user"
}

type channel struct {
	ID, Name, DisplayName, TeamID, Username string
	Latest, LastViewed                      int64
	Unread, Mentions                        int
	Favorite, Direct                        bool
	Threads                                 []threadSignal
}

func (c channel) hasUnread() bool {
	return c.Unread > 0 || c.Mentions > 0 || len(c.Threads) > 0
}

type threadSignal struct {
	root, team       string
	unread, mentions int
	lastViewed       int64
}

type post struct {
	ID        string
	Type      string
	ChannelID string   `json:"channel_id"`
	UserID    string   `json:"user_id"`
	RootID    string   `json:"root_id"`
	Text      string   `json:"message"`
	CreatedAt int64    `json:"create_at"`
	DeletedAt int64    `json:"delete_at"`
	Replies   int      `json:"reply_count"`
	Files     []string `json:"file_ids"`
	Metadata  struct {
		Files     []fileInfo
		Reactions []reaction
	}
	Author    string `json:"-"`
	Own       bool   `json:"-"`
	feedAt    int64
	feedLabel string
}

type postList struct {
	Order []string
	Posts map[string]post
}

type historyPage struct {
	posts  []post
	before string
	floor  int64
}

type client struct {
	baseURL, token string
	http           *http.Client
	me             person
	teamNames      map[string]string
	teamIDs        []string
	mu             sync.Mutex
	people         map[string]person
}

var errUnauthorized = errors.New("the server rejected the access token; restart loop-tui with a valid token")
var errNotFound = errors.New("Loop returned HTTP 404")

func newClient(baseURL, token string) *client {
	return &client{
		baseURL: baseURL,
		token:   token,
		http: &http.Client{
			Timeout:       12 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		people: make(map[string]person),
	}
}

func (c *client) request(ctx context.Context, method, path string, input, output any) error {
	resp, err := c.response(ctx, c.http, method, path, input)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if output != nil {
		return json.NewDecoder(resp.Body).Decode(output)
	}
	return nil
}

func (c *client) response(ctx context.Context, client *http.Client, method, path string, input any) (*http.Response, error) {
	var body []byte
	if input != nil {
		var err error
		body, err = json.Marshal(input)
		if err != nil {
			return nil, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+"/api/v4"+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.do(client, req)
}

func (c *client) do(client *http.Client, req *http.Request) (*http.Response, error) {
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp, nil
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, errUnauthorized
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, errNotFound
	}
	return nil, fmt.Errorf("Loop returned HTTP %d", resp.StatusCode)
}

func (c *client) connect(ctx context.Context) error {
	if err := c.request(ctx, http.MethodGet, "/users/me", nil, &c.me); err != nil {
		return fmt.Errorf("sign in: %w", err)
	}
	var teams []struct {
		ID   string
		Name string `json:"display_name"`
	}
	if err := c.request(ctx, http.MethodGet, "/users/me/teams", nil, &teams); err != nil {
		return fmt.Errorf("load teams: %w", err)
	}
	c.teamNames = make(map[string]string, len(teams))
	for _, team := range teams {
		c.teamNames[team.ID] = team.Name
		c.teamIDs = append(c.teamIDs, team.ID)
	}
	c.people[c.me.ID] = c.me
	return nil
}

func (c *client) users(ctx context.Context, ids []string) map[string]person {
	found := make(map[string]person, len(ids))
	var missing []string
	seen := make(map[string]bool)
	c.mu.Lock()
	for _, id := range ids {
		if p, ok := c.people[id]; ok {
			found[id] = p
		} else if id != "" && !seen[id] {
			missing = append(missing, id)
			seen[id] = true
		}
	}
	c.mu.Unlock()
	for batch := range slices.Chunk(missing, 100) {
		var users []person
		if err := c.request(ctx, http.MethodPost, "/users/ids", batch, &users); err != nil {
			break
		}
		c.mu.Lock()
		for _, p := range users {
			found[p.ID], c.people[p.ID] = p, p
		}
		c.mu.Unlock()
	}
	return found
}

func (c *client) channels(ctx context.Context) ([]channel, error) {
	var raw []struct {
		ID, Name, Type string
		DisplayName    string `json:"display_name"`
		TeamID         string `json:"team_id"`
		LastPost       int64  `json:"last_post_at"`
		Total          int    `json:"total_msg_count"`
		RootTotal      *int   `json:"total_msg_count_root"`
	}
	if err := c.request(ctx, http.MethodGet, "/users/me/channels", nil, &raw); err != nil {
		return nil, err
	}
	type membership struct {
		ChannelID  string `json:"channel_id"`
		Read       int    `json:"msg_count"`
		RootRead   *int   `json:"msg_count_root"`
		Mentions   int    `json:"mention_count"`
		LastViewed int64  `json:"last_viewed_at"`
	}
	byChannel := make(map[string]membership, len(raw))
	favorites := make(map[string]int)
	threadSignals := make(map[string][]threadSignal)
	seenThreads := make(map[string]bool)
	for _, team := range c.teamIDs {
		var members []membership
		path := "/users/me/teams/" + url.PathEscape(team) + "/channels/members"
		if err := c.request(ctx, http.MethodGet, path, nil, &members); err != nil {
			return nil, fmt.Errorf("load unread counts: %w", err)
		}
		for _, member := range members {
			byChannel[member.ChannelID] = member
		}
		var sidebar struct {
			Categories []struct {
				Type       string
				ChannelIDs []string `json:"channel_ids"`
			}
		}
		path = "/users/me/teams/" + url.PathEscape(team) + "/channels/categories"
		if err := c.request(ctx, http.MethodGet, path, nil, &sidebar); err != nil && !errors.Is(err, errNotFound) {
			return nil, fmt.Errorf("load favorites: %w", err)
		}
		for _, category := range sidebar.Categories {
			if category.Type != "favorites" {
				continue
			}
			for _, id := range category.ChannelIDs {
				if _, ok := favorites[id]; !ok {
					favorites[id] = len(favorites)
				}
			}
		}
		if err := c.loadUnreadThreads(ctx, team, threadSignals, seenThreads); err != nil {
			return nil, fmt.Errorf("load unread threads: %w", err)
		}
	}
	var peers []string
	for _, ch := range raw {
		if ch.Type == "D" {
			peers = append(peers, c.directPeer(ch.Name))
		}
	}
	names := c.users(ctx, peers)
	out := make([]channel, 0, len(raw))
	for _, ch := range raw {
		name, username := ch.DisplayName, ""
		if ch.Type == "D" {
			if peer, ok := names[c.directPeer(ch.Name)]; ok {
				name, username = peer.label(), peer.Username
			} else if name == "" {
				name = "Direct message"
			}
		}
		if name == "" {
			name = ch.Name
		}
		team := c.teamNames[ch.TeamID]
		if len(c.teamIDs) > 1 && team != "" {
			name += " · " + team
		}
		item := channel{
			ID: ch.ID, Name: ch.Name, DisplayName: name, TeamID: ch.TeamID, Username: username, Latest: ch.LastPost,
			Direct: ch.Type == "D" || ch.Type == "G",
		}
		_, item.Favorite = favorites[ch.ID]
		item.Threads = threadSignals[ch.ID]
		if member, ok := byChannel[ch.ID]; ok {
			item.LastViewed = member.LastViewed
			item.Unread = max(0, ch.Total-member.Read)
			if ch.RootTotal != nil && member.RootRead != nil {
				item.Unread = max(0, *ch.RootTotal-*member.RootRead)
			}
			item.Mentions = member.Mentions
		}
		out = append(out, item)
	}
	slices.SortStableFunc(out, func(a, b channel) int {
		if a.Favorite {
			if !b.Favorite {
				return -1
			}
			return cmp.Compare(favorites[a.ID], favorites[b.ID])
		}
		if b.Favorite {
			return 1
		}
		if a.Latest != b.Latest {
			if a.Latest > b.Latest {
				return -1
			}
			return 1
		}
		return strings.Compare(strings.ToLower(a.DisplayName), strings.ToLower(b.DisplayName))
	})
	return out, nil
}

func (c *client) directPeer(name string) string {
	first, second, found := strings.Cut(name, "__")
	if !found {
		return ""
	}
	if first == c.me.ID {
		return second
	}
	if second == c.me.ID {
		return first
	}
	return ""
}

func (c *client) autocomplete(ctx context.Context, ch channel, term string) ([]person, error) {
	team := ch.TeamID
	if team == "" && len(c.teamIDs) > 0 {
		team = c.teamIDs[0]
	}
	query := url.Values{"name": {term}, "limit": {"8"}}
	if team != "" {
		query.Set("in_team", team)
		if ch.ID != "" {
			query.Set("in_channel", ch.ID)
		}
	}
	var response struct {
		Users        []person
		OutOfChannel []person `json:"out_of_channel"`
	}
	if err := c.request(ctx, http.MethodGet, "/users/autocomplete?"+query.Encode(), nil, &response); err != nil {
		return nil, err
	}
	names := make(map[string]bool)
	var choices []person
	for _, p := range append(response.Users, response.OutOfChannel...) {
		if p.Username == "" || names[p.Username] {
			continue
		}
		names[p.Username] = true
		choices = append(choices, p)
		if len(choices) == 8 {
			break
		}
	}
	return choices, nil
}

func (c *client) loadUnreadThreads(ctx context.Context, team string, byChannel map[string][]threadSignal, seen map[string]bool) error {
	const pageSize = 100
	query := url.Values{
		"unread": {"true"}, "threadsOnly": {"true"}, "per_page": {strconv.Itoa(pageSize)},
	}
	path := threadPath(team) + "?"
	for {
		var page struct {
			Threads []struct {
				Post       post
				Unread     int   `json:"unread_replies"`
				Mentions   int   `json:"unread_mentions"`
				LastViewed int64 `json:"last_viewed_at"`
			}
		}
		err := c.request(ctx, http.MethodGet, path+query.Encode(), nil, &page)
		if errors.Is(err, errNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		for _, thread := range page.Threads {
			p := thread.Post
			if p.ID == "" || p.ChannelID == "" || seen[p.ID] || thread.Unread <= 0 && thread.Mentions <= 0 {
				continue
			}
			seen[p.ID] = true
			byChannel[p.ChannelID] = append(byChannel[p.ChannelID], threadSignal{
				root: p.ID, team: team, unread: max(thread.Unread, thread.Mentions),
				mentions: thread.Mentions, lastViewed: thread.LastViewed,
			})
		}
		if len(page.Threads) < pageSize {
			return nil
		}
		last := page.Threads[len(page.Threads)-1].Post.ID
		if last == "" || last == query.Get("before") {
			return nil
		}
		query.Set("before", last)
	}
}

func (c *client) markThreadRead(ctx context.Context, signal threadSignal, through int64) error {
	path := threadPath(signal.team) + "/" +
		url.PathEscape(signal.root) + "/read/" + strconv.FormatInt(through, 10)
	return c.request(ctx, http.MethodPut, path, nil, nil)
}

func (c *client) timeline(ctx context.Context, channelID, before string) (historyPage, error) {
	const pageSize = 60
	var result historyPage
	replyCounts := make(map[string]int)
	// An entire page can consist of replies. Use the oldest server post as the cursor.
	for range 5 {
		query := url.Values{"per_page": {strconv.Itoa(pageSize)}, "collapsedThreads": {"true"}, "include_file_metadata": {"true"}}
		if before != "" {
			query.Set("before", before)
		}
		var list postList
		path := "/channels/" + url.PathEscape(channelID) + "/posts?" + query.Encode()
		if err := c.request(ctx, http.MethodGet, path, nil, &list); err != nil {
			return historyPage{}, err
		}
		result.before = ""
		if len(list.Order) > 0 {
			last := list.Order[len(list.Order)-1]
			result.floor = list.Posts[last].CreatedAt
			if len(list.Order) == pageSize && last != before {
				result.before = last
			}
		}
		for _, id := range list.Order {
			p := list.Posts[id]
			if p.ID != "" && p.DeletedAt == 0 && p.RootID != "" {
				replyCounts[p.RootID]++
			}
		}
		for _, id := range list.Order {
			p := list.Posts[id]
			if p.ID != "" && p.DeletedAt == 0 && p.RootID == "" {
				p.Replies = max(p.Replies, replyCounts[p.ID])
				result.posts = append(result.posts, p)
			}
		}
		if len(result.posts) != 0 || result.before == "" {
			break
		}
		before = result.before
	}
	c.addAuthors(ctx, result.posts)
	return result, nil
}

func (c *client) thread(ctx context.Context, rootID string) ([]post, error) {
	var list postList
	if err := c.request(ctx, http.MethodGet, "/posts/"+url.PathEscape(rootID)+"/thread?include_file_metadata=true", nil, &list); err != nil {
		return nil, err
	}
	var posts []post
	for _, id := range list.Order {
		p := list.Posts[id]
		if p.ID != "" && p.DeletedAt == 0 && (p.ID == rootID || p.RootID == rootID) {
			posts = append(posts, p)
		}
	}
	slices.SortStableFunc(posts, func(a, b post) int {
		switch {
		case a.ID == b.ID:
			return 0
		case a.ID == rootID:
			return 1
		case b.ID == rootID:
			return -1
		case a.CreatedAt > b.CreatedAt:
			return -1
		case a.CreatedAt < b.CreatedAt:
			return 1
		default:
			return 0
		}
	})
	c.addAuthors(ctx, posts)
	return posts, nil
}

func (c *client) addAuthors(ctx context.Context, posts []post) {
	ids := make([]string, 0, len(posts))
	for _, p := range posts {
		ids = append(ids, p.UserID)
	}
	people := c.users(ctx, ids)
	for i, p := range posts {
		if p.UserID == c.me.ID {
			posts[i].Own = true
			posts[i].Author = "You"
		} else {
			posts[i].Author = people[p.UserID].label()
		}
	}
}

func (c *client) markRead(ctx context.Context, channelID string) error {
	path := "/channels/members/" + url.PathEscape(c.me.ID) + "/view"
	return c.request(ctx, http.MethodPost, path, map[string]any{
		"channel_id": channelID, "collapsed_threads_supported": true,
	}, nil)
}

func (c *client) send(ctx context.Context, channelID, rootID, text string, fileIDs []string) error {
	body := map[string]any{"channel_id": channelID, "root_id": rootID, "message": text}
	if len(fileIDs) > 0 {
		body["file_ids"] = fileIDs
	}
	return c.request(ctx, http.MethodPost, "/posts", body, nil)
}

func (c *client) deletePost(ctx context.Context, id string) error {
	return c.request(ctx, http.MethodDelete, "/posts/"+url.PathEscape(id), nil, nil)
}
