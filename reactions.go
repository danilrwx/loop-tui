package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/danilrwx/telega/tui"
	"github.com/kyokomi/emoji/v2"
)

type reaction struct {
	PostID    string `json:"post_id"`
	UserID    string `json:"user_id"`
	EmojiName string `json:"emoji_name"`
	UpdatedAt int64  `json:"update_at"`
}

type reactionResult struct {
	items []reaction
	err   error
}

var loopQuickReactions = tui.ReactionOptions("😂",
	tui.ReactionOption{Emoji: "🔙", Label: "Back"},
	tui.ReactionOption{Emoji: "✅", Label: "Check"},
	tui.ReactionOption{Emoji: "👀", Label: "Eyes"},
)

var reactionAPINames = map[string]string{
	"👍": "+1", "❤": "heart", "🔥": "fire",
	"😂": "joy", "🎉": "tada", "👎": "-1",
	"🔙": "back", "✅": "white_check_mark", "👀": "eyes",
}

var reactionDisplay = map[string]string{
	"+1": "👍", "thumbsup": "👍", "heart": "❤", "fire": "🔥",
	"joy": "😂", "tada": "🎉", "-1": "👎", "thumbsdown": "👎",
	"back": "🔙", "white_check_mark": "✅", "eyes": "👀", "ok_hand": "👌",
	"grin": "😁", "rolling_on_the_floor_laughing": "🤣", "rofl": "🤣", "scream": "😱",
}

func reactionGlyph(name string) string {
	if glyph := reactionDisplay[name]; glyph != "" {
		return glyph
	}
	code := ":" + name + ":"
	if glyph := emoji.CodeMap()[code]; glyph != "" {
		return glyph
	}
	return code
}

func reactionSummary(items []reaction) string {
	if len(items) == 0 {
		return ""
	}
	var order []string
	counts := make(map[string]int)
	for _, item := range items {
		if item.EmojiName == "" {
			continue
		}
		label := reactionGlyph(item.EmojiName)
		if counts[label] == 0 {
			order = append(order, label)
		}
		counts[label]++
	}
	for i, label := range order {
		order[i] = fmt.Sprintf("%s %d", label, counts[label])
	}
	return strings.Join(order, "  ")
}

func (c *client) applyReaction(ctx context.Context, postID, name string, addOnly bool) ([]reaction, error) {
	postPath := "/posts/" + url.PathEscape(postID) + "/reactions"
	items, err := c.postReactions(ctx, postID)
	if err != nil {
		return nil, err
	}
	glyph := reactionGlyph(name)
	index := slices.IndexFunc(items, func(item reaction) bool {
		return item.UserID == c.me.ID && reactionGlyph(item.EmojiName) == glyph
	})
	if index >= 0 {
		if addOnly {
			return items, nil
		}
		path := "/users/" + url.PathEscape(c.me.ID) + postPath + "/" + url.PathEscape(items[index].EmojiName)
		if err := c.request(ctx, http.MethodDelete, path, nil, nil); err != nil {
			return nil, err
		}
		return slices.Delete(items, index, index+1), nil
	}

	item := reaction{PostID: postID, UserID: c.me.ID, EmojiName: name}
	if err := c.request(ctx, http.MethodPost, "/reactions", item, nil); err != nil {
		return nil, err
	}
	return append(items, item), nil
}

type reactionSuggestion struct {
	name, glyph string
	count       int
}

func (m *app) reactionSuggestions() []reactionSuggestion {
	var choices []reactionSuggestion
	positions := make(map[string]int)
	own := make(map[string]bool)
	for _, item := range m.reactionTarget.Metadata.Reactions {
		if item.UserID == m.api.me.ID {
			own[reactionGlyph(item.EmojiName)] = true
		}
	}
	for _, item := range m.reactionTarget.Metadata.Reactions {
		glyph := reactionGlyph(item.EmojiName)
		if item.EmojiName == "" || own[glyph] {
			continue
		}
		if index, ok := positions[glyph]; ok {
			choices[index].count++
		} else {
			positions[glyph] = len(choices)
			choices = append(choices, reactionSuggestion{item.EmojiName, glyph, 1})
		}
	}
	return choices
}

func (m *app) reactionSuggestionRow(width int) string {
	if m.reactionTarget.ID == "" || m.reactionCustomIndex >= 0 {
		return ""
	}
	choices := m.reactionSuggestions()
	if len(choices) == 0 {
		return ""
	}
	index := min(m.reactionSuggestionIndex, len(choices)-1)
	choice := choices[index]
	return tui.ReactionSuggestion(choice.glyph, choice.count, index, len(choices), width)
}

func (m *app) pickReaction(p post) {
	if p.ID != "" {
		m.reactionTarget = p
		m.reactionSuggestionIndex = 0
		m.reactionCustomIndex = -1
		m.status = ""
	}
}

func (m *app) reactionKey(key tea.KeyMsg) tea.Cmd {
	if m.reactionClosing {
		return nil
	}
	if key.String() == "esc" || m.reactionCustomIndex < 0 && commandKeyName(key) == "z" {
		if m.reacting {
			m.reactionClosing = true
			return nil
		}
		return m.closeReactionPicker()
	}
	switch key.String() {
	case "tab":
		if m.reactionCustomIndex < 0 {
			return m.openEmojiPicker()
		}
		m.reactionCustomIndex = -1
		m.reactionEmojiQuery.Blur()
		return nil
	}
	if m.reactionCustomIndex >= 0 {
		return m.emojiPickerKey(key)
	}
	switch key.String() {
	case "left", "right", "enter":
		choices := m.reactionSuggestions()
		if len(choices) == 0 {
			return nil
		}
		index := min(m.reactionSuggestionIndex, len(choices)-1)
		switch key.String() {
		case "left":
			m.reactionSuggestionIndex = max(0, index-1)
		case "right":
			m.reactionSuggestionIndex = min(len(choices)-1, index+1)
		case "enter":
			if !m.reacting {
				return m.applyReaction(choices[index].name, true)
			}
		}
		return nil
	default:
		return m.chooseReaction(reactionAPINames[tui.QuickReaction(key.String(), loopQuickReactions)])
	}
}

func (m *app) chooseReaction(name string) tea.Cmd {
	if name == "" {
		return nil
	}
	if m.reacting {
		m.reactionQueue = append(m.reactionQueue, name)
		return nil
	}
	return m.applyReaction(name, false)
}

func (m *app) applyReaction(name string, addOnly bool) tea.Cmd {
	id := m.reactionTarget.ID
	m.reacting = true
	return func() tea.Msg {
		items, err := m.api.applyReaction(m.ctx, id, name, addOnly)
		return reactionResult{items, err}
	}
}

func (m *app) receiveReaction(result reactionResult) tea.Cmd {
	m.reacting = false
	if result.err != nil {
		m.status = "Reaction failed: " + result.err.Error()
	} else {
		m.reactionChanged = true
		selected := m.selectedEmoji()
		m.reactionTarget.Metadata.Reactions = result.items
		m.keepEmojiSelection(selected)
		id := m.reactionTarget.ID
		for _, react := range result.items {
			m.acknowledgeReaction(react)
		}
		for _, view := range []*conversation{&m.timeline, &m.discussion} {
			if i := slices.IndexFunc(view.posts, func(p post) bool { return p.ID == id }); i >= 0 {
				view.posts[i].Metadata.Reactions = result.items
			}
		}
		if m.reader.ID == id {
			m.reader.Metadata.Reactions = result.items
		}
	}
	if len(m.reactionQueue) > 0 {
		name := m.reactionQueue[0]
		m.reactionQueue = m.reactionQueue[1:]
		return m.applyReaction(name, false)
	}
	if m.reactionClosing {
		return m.closeReactionPicker()
	}
	return nil
}

func (m *app) closeReactionPicker() tea.Cmd {
	m.reactionTarget, m.reactionClosing = post{}, false
	m.reactionCustomIndex = -1
	m.reactionEmojiQuery.Blur()
	m.reactionQueue = nil
	changed := m.reactionChanged
	m.reactionChanged = false
	if changed {
		return tea.Batch(m.loadHistory(false, false, false, true), m.loadHistory(true, false, false, true))
	}
	return nil
}

func (m *app) reactionPrompt() string {
	if m.status != "" {
		return m.status
	}
	var own []string
	for _, item := range m.reactionTarget.Metadata.Reactions {
		if item.UserID == m.api.me.ID {
			own = append(own, reactionGlyph(item.EmojiName))
		}
	}
	if len(own) != 0 {
		return "Your reactions: " + strings.Join(own, " ") + " · select one again to remove"
	}
	return "Choose a reaction · select it again to remove"
}

func (m *app) reactionFooter(width int) (string, string) {
	prompt := m.reactionPrompt()
	if m.reacting {
		prompt = "Saving reactions…"
	}
	const tab = "  Tab picker"
	if width >= 64 {
		status, keys := tui.ReactionFooter(prompt, width-len(tab), false, loopQuickReactions)
		return status, keys + tab
	}
	status, keys := tui.ReactionFooter(prompt, width, false, loopQuickReactions)
	return tui.Clip(status, width-4) + " Tab", keys
}
