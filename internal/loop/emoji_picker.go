package loop

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/danilrwx/telega/tui"
)

const emojiPageSize = 60

type emojiPage struct {
	page  int
	names []string
	err   error
}

func (m *app) openEmojiPicker() tea.Cmd {
	m.reactionCustomIndex = 0
	m.reactionEmojiQuery.Reset()

	focus := m.reactionEmojiQuery.Focus()
	if m.emojiCatalogLoading || time.Since(m.emojiCatalogUpdated) < 5*time.Minute {
		return focus
	}

	m.emojiCatalogLoading = true
	m.emojiCatalogErr = nil

	return tea.Batch(focus, m.loadEmojiPage(0))
}

func (m *app) loadEmojiPage(page int) tea.Cmd {
	return func() tea.Msg {
		var entries []struct{ Name string }

		path := fmt.Sprintf("/emoji?sort=name&per_page=%d&page=%d", emojiPageSize, page)
		err := m.api.request(m.ctx, http.MethodGet, path, nil, &entries)

		result := emojiPage{page: page, err: err}
		for _, emoji := range entries {
			result.names = append(result.names, emoji.Name)
		}

		return result
	}
}

func (m *app) receiveEmojiPage(page emojiPage) tea.Cmd {
	if page.err != nil {
		m.emojiCatalogLoading = false
		m.emojiCatalogErr = page.err

		return nil
	}

	selected := m.selectedEmoji()
	if page.page == 0 {
		m.emojiCatalog = nil
	}

	added := false

	for _, name := range page.names {
		if name != "" && !slices.Contains(m.emojiCatalog, name) {
			m.emojiCatalog = append(m.emojiCatalog, name)
			added = true
		}
	}

	m.keepEmojiSelection(selected)

	if len(page.names) == emojiPageSize && added {
		return m.loadEmojiPage(page.page + 1)
	}

	m.emojiCatalogLoading = false
	m.emojiCatalogUpdated = time.Now()

	return nil
}

func (m *app) emojiMatches() []string {
	term := strings.Trim(strings.ToLower(strings.TrimSpace(m.reactionEmojiQuery.Value())), ":")

	var names []string

	seen := make(map[string]bool)

	add := func(name string) {
		if name == "" || seen[name] || (!strings.Contains(strings.ToLower(name), term) &&
			!strings.Contains(reactionGlyph(name), term)) {
			return
		}

		names = append(names, name)
		seen[name] = true
	}
	for _, item := range m.reactionTarget.Metadata.Reactions {
		add(item.EmojiName)
	}

	for digit := '1'; digit <= '9'; digit++ {
		add(reactionAPINames[tui.QuickReaction(string(digit), loopQuickReactions)])
	}

	for _, name := range m.emojiCatalog {
		add(name)
	}

	if exact := slices.Index(names, term); exact > 0 {
		copy(names[1:exact+1], names[:exact])
		names[0] = term
	}

	return names
}

func (m *app) selectedEmoji() string {
	if index := m.reactionCustomIndex; index >= 0 {
		if names := m.emojiMatches(); index < len(names) {
			return names[index]
		}
	}

	return ""
}

func (m *app) keepEmojiSelection(name string) {
	if m.reactionCustomIndex < 0 {
		return
	}

	names := m.emojiMatches()
	if i := slices.Index(names, name); i >= 0 {
		m.reactionCustomIndex = i
	} else {
		m.reactionCustomIndex = min(m.reactionCustomIndex, max(0, len(names)-1))
	}
}

func (m *app) emojiPickerKey(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case "up", "ctrl+p":
		m.reactionCustomIndex = max(0, m.reactionCustomIndex-1)
	case "down", "ctrl+n":
		m.reactionCustomIndex = min(m.reactionCustomIndex+1, max(0, len(m.emojiMatches())-1))
	case "ctrl+o":
		return m.previewEmoji()
	case "enter":
		return m.chooseReaction(m.selectedEmoji())
	default:
		return m.updateEmojiQuery(key)
	}

	return nil
}

func (m *app) updateEmojiQuery(msg tea.Msg) tea.Cmd {
	old := m.reactionEmojiQuery.Value()

	var cmd tea.Cmd

	m.reactionEmojiQuery, cmd = m.reactionEmojiQuery.Update(msg)
	if old != m.reactionEmojiQuery.Value() {
		m.reactionCustomIndex = 0
	}

	return cmd
}

func (m *app) renderEmojiCatalog(width, height int, row func(string, bool, bool, bool)) {
	names := m.emojiMatches()

	notice := m.status
	switch {
	case notice != "":
	case m.emojiCatalogErr != nil:
		notice = "Emoji catalog: " + m.emojiCatalogErr.Error()
	case m.emojiCatalogLoading:
		notice = "Loading Loop emoji…"
	case len(names) == 0:
		notice = "No matching emoji"
	}

	choices := make([]string, 0, len(names))
	for _, name := range names {
		label := ":" + name + ":"
		if glyph := reactionGlyph(name); glyph != label {
			label = glyph + " " + label
		}

		if slices.ContainsFunc(m.reactionTarget.Metadata.Reactions, func(item reaction) bool {
			return item.UserID == m.api.me.ID && item.EmojiName == name
		}) {
			label += " ✓"
		}

		choices = append(choices, label)
	}

	hint := "↑↓ choose  Enter toggle  Tab quick  Esc back"
	if tui.ImageGraphics() && width >= 65 {
		hint += "  Ctrl+O preview"
	}

	if width < 44 {
		hint = "↑↓ Enter Tab Esc"
	}

	tui.SearchListDialog("Reactions", "/ "+m.reactionEmojiQuery.Value()+"▏", choices, m.reactionCustomIndex, width, height, notice, hint, row)
}
