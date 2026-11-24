package loop

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

type demoTransport struct {
	channel, thread string
}

func newDemoClient() *client {
	now := time.Now().Truncate(time.Minute)
	posts := postList{Order: []string{"field-kit", "checklist", "welcome"}, Posts: map[string]post{
		"checklist": {
			ID: "checklist", ChannelID: "field-notes", UserID: "mira", CreatedAt: now.Add(-6 * time.Minute).UnixMilli(),
			Text: "### Ready for Saturday\n\n| Item | Status |\n| --- | --- |\n| Radio | ✅ Checked |\n| Batteries | ✅ Packed |\n| Map | Printed |",
		},
		"field-kit": {
			ID: "field-kit", ChannelID: "field-notes", UserID: "theo", CreatedAt: now.Add(-3 * time.Minute).UnixMilli(),
			Text: "The field terminal is ready.\nPickup plans go in this thread.", Replies: 2,
		},
		"welcome": {
			ID: "welcome", ChannelID: "field-notes", UserID: "mira", CreatedAt: now.Add(-9 * time.Minute).UnixMilli(),
			Text: "Welcome to the offline demo.",
		},
	}}
	thread := postList{Order: []string{"reply-2", "reply-1", "field-kit"}, Posts: map[string]post{
		"field-kit": posts.Posts["field-kit"],
		"reply-1": {
			ID: "reply-1", ChannelID: "field-notes", RootID: "field-kit", UserID: "mira",
			CreatedAt: now.Add(-2 * time.Minute).UnixMilli(), Text: "I’ll bring the radio and map.",
		},
		"reply-2": {
			ID: "reply-2", ChannelID: "field-notes", RootID: "field-kit", UserID: "you",
			CreatedAt: now.Add(-time.Minute).UnixMilli(), Text: "I’ll bring a spare battery.",
		},
	}}
	lastReply := thread.Posts["reply-2"]
	lastReply.Metadata.Reactions = []reaction{{PostID: "reply-2", UserID: "mira", EmojiName: "white_check_mark"}}
	thread.Posts[lastReply.ID] = lastReply
	channelData, _ := json.Marshal(posts)
	threadData, _ := json.Marshal(thread)
	api := newClient("https://demo.invalid", "")
	api.http.Transport = demoTransport{channel: string(channelData), thread: string(threadData)}

	return api
}

func (demo demoTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	path := strings.TrimPrefix(req.URL.Path, "/api/v4")
	status := http.StatusOK
	body := "{}"

	switch req.Method + " " + path {
	case "GET /users/me":
		body = `{"id":"you","username":"you"}`
	case "GET /users/me/teams":
		body = `[{"id":"demo","display_name":"Observatory Lab"}]`
	case "GET /users/me/channels":
		body = `[
			{"id":"field-notes","name":"field-notes","display_name":"Field Notes","team_id":"demo","total_msg_count":3},
			{"id":"workshop","name":"workshop","display_name":"Workshop","team_id":"demo","total_msg_count":4},
			{"id":"mira-dm","name":"mira__you","type":"D","total_msg_count":2}
		]`
	case "GET /users/me/teams/demo/channels/categories":
		body = `{"categories":[{"type":"favorites","channel_ids":["field-notes"]}]}`
	case "GET /users/me/teams/demo/channels/members":
		body = `[{"channel_id":"field-notes","msg_count":2},{"channel_id":"mira-dm","msg_count":1},{"channel_id":"workshop","msg_count":0,"mention_count":1}]`
	case "GET /users/me/teams/demo/threads":
		body = `{"threads":[]}`
	case "POST /users/ids":
		body = `[{"id":"mira","username":"mira","first_name":"Mira","last_name":"Chen"},{"id":"theo","username":"theo","first_name":"Theo","last_name":"Park"}]`
	case "GET /channels/field-notes/posts":
		body = demo.channel
	case "GET /channels/workshop/posts", "GET /channels/mira-dm/posts":
		body = `{"order":[],"posts":{}}`
	case "GET /posts/field-kit/thread":
		body = demo.thread
	case "POST /channels/members/me/view":
	default:
		status = http.StatusNotFound
	}

	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: req}, nil
}
