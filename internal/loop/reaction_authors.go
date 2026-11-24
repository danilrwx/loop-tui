package loop

import (
	"context"
	"net/http"
	"net/url"
	"slices"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/danilrwx/telega/tui"
)

type reactionAuthorsResult struct {
	dialog *tui.ReactionAuthors
	rows   []string
	status string
}

func (c *client) postReactions(ctx context.Context, postID string) ([]reaction, error) {
	var items []reaction

	err := c.request(ctx, http.MethodGet, "/posts/"+url.PathEscape(postID)+"/reactions", nil, &items)

	return items, err
}

func (m *app) showReactionAuthors(p post) tea.Cmd {
	if p.ID == "" {
		return nil
	}

	dialog := &tui.ReactionAuthors{Status: "Loading reactions…"}
	m.reactionAuthors = dialog

	return func() tea.Msg {
		result := reactionAuthorsResult{dialog: dialog}

		items, err := m.api.postReactions(m.ctx, p.ID)
		if err != nil {
			result.status = "Could not load reaction authors: " + err.Error()
			return result
		}

		ids := make([]string, 0, len(items))
		for _, item := range items {
			ids = append(ids, item.UserID)
		}

		people := m.api.users(m.ctx, ids)
		for _, item := range items {
			name := people[item.UserID].label()
			if item.UserID == m.api.me.ID {
				name = "You"
			}

			result.rows = append(result.rows, reactionGlyph(item.EmojiName)+"  "+name)
		}

		slices.Sort(result.rows)

		return result
	}
}
