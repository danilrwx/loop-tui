package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type (
	person struct{ ID, Username string }
	post   struct {
		ID, Type  string
		ChannelID string `json:"channel_id"`
		UserID    string `json:"user_id"`
		RootID    string `json:"root_id"`
		Text      string `json:"message"`
		CreatedAt int64  `json:"create_at"`
	}
)

type reaction struct {
	PostID    string `json:"post_id"`
	UserID    string `json:"user_id"`
	EmojiName string `json:"emoji_name"`
}
type threadSignal struct {
	root, team       string
	unread, mentions int
	lastViewed       int64
}
type channel struct {
	ID, Name, DisplayName, TeamID string
	Latest, LastViewed            int64
	Unread, Mentions              int
	Direct                        bool
	Threads                       []threadSignal
}
type client struct {
	baseURL, token string
	http           *http.Client
	me             person
	teamIDs        []string
	teamNames      map[string]string
}

var (
	errUnauthorized = errors.New("the server rejected the session; log in again")
	errNotFound     = errors.New("server returned HTTP 404")
)

func newClient(baseURL, token string) *client {
	return &client{baseURL: baseURL, token: token, http: &http.Client{Timeout: 12 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (c *client) request(ctx context.Context, method, path string, input, output any) error {
	resp, err := c.response(ctx, c.http, method, path, input)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

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

	_ = resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, errUnauthorized
	}

	if resp.StatusCode == http.StatusNotFound {
		return nil, errNotFound
	}

	return nil, fmt.Errorf("server returned HTTP %d", resp.StatusCode)
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

	return nil
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

		if err := c.loadUnreadThreads(ctx, team, threadSignals, seenThreads); err != nil {
			return nil, fmt.Errorf("load unread threads: %w", err)
		}
	}

	out := make([]channel, 0, len(raw))
	for _, ch := range raw {
		name := ch.DisplayName
		if name == "" {
			name = ch.Name
		}

		team := c.teamNames[ch.TeamID]
		if len(c.teamIDs) > 1 && team != "" {
			name += " · " + team
		}

		item := channel{
			ID: ch.ID, Name: ch.Name, DisplayName: name, TeamID: ch.TeamID, Latest: ch.LastPost,
			Direct: ch.Type == "D" || ch.Type == "G",
		}

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

	return out, nil
}

func (c *client) loadUnreadThreads(ctx context.Context, team string, byChannel map[string][]threadSignal, seen map[string]bool) error {
	const pageSize = 100

	query := url.Values{
		"unread": {"true"}, "threadsOnly": {"true"}, "per_page": {strconv.Itoa(pageSize)},
	}
	path := "/users/me/teams/" + url.PathEscape(team) + "/threads" + "?"

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
