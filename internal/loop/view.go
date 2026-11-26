package loop

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/danilrwx/telega/tui"
	"github.com/rivo/uniseg"
)

// ANSI indices keep rendered text linked to the terminal's live palette.
var (
	heading       = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("5"))
	muted         = lipgloss.NewStyle().Faint(true)
	picked        = lipgloss.NewStyle().Reverse(true)
	accent        = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	dateColor     = lipgloss.NewStyle().Foreground(lipgloss.Color("4"))
	unreadColor   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("3"))
	authorColor   = lipgloss.NewStyle().Bold(true)
	reactionColor = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
)

type paneLayout struct {
	sidebar, channel, thread int
}

func (m *app) layout() paneLayout {
	if m.fullPane && m.active != "" {
		switch {
		case m.focus == threadPane && m.root != "":
			return paneLayout{thread: m.width}
		case m.focus == channelPane:
			return paneLayout{channel: m.width}
		}
	}

	var layout paneLayout
	if !m.preferences.HideChatListWhenOpen {
		layout.sidebar = tui.SidebarWidth(m.width, m.layoutSettings.SidebarWidth)
	}

	if m.active == "" || layout.sidebar == 0 && m.focus == channelsPane {
		layout.sidebar = m.width
		return layout
	}

	free := m.width - layout.sidebar
	if layout.sidebar > 0 {
		free--
	}

	layout.channel = free
	if m.root != "" && m.width >= 108 && free >= 73 {
		layout.thread = max(35, free/2)
		layout.channel = free - layout.thread - 1
	} else if m.root != "" && m.focus == threadPane {
		layout.channel, layout.thread = 0, free
	}

	return layout
}

func (m *app) conversationWidth() int {
	layout := m.layout()

	width := layout.channel
	if m.focus == threadPane {
		width = layout.thread
	}

	return tui.ComposerTextWidth(width) + tui.ComposerIndent
}

func (m *app) View() string {
	if m.width < 24 || m.height < 9 {
		if m.width == 0 {
			return ""
		}

		return "Resize your terminal"
	}

	content := m.contentView()

	return m.dialogPopup().Overlay(content, m.width, m.height)
}

func (m *app) contentView() string {
	if m.reader.ID != "" {
		return m.readView()
	}

	suggestion := m.reactionSuggestionRow(m.width - 2)
	height := m.paneHeight()
	layout := m.layout()

	var panes []string
	if layout.sidebar != 0 {
		panes = append(panes, m.sidebar(layout.sidebar, height))
	}

	if layout.channel != 0 {
		panes = append(panes, m.conversationPane(false, layout.channel, height))
	}

	if layout.thread != 0 {
		panes = append(panes, m.conversationPane(true, layout.thread, height))
	}

	content := joinPanes(panes, height)

	status := m.status
	if m.sending {
		status = "Sending…"
	}

	status, keys := m.conversationFooter(status, m.hints(), m.width-2)

	lines := []string{content, " " + status}
	if suggestion != "" {
		lines = append(lines, " "+suggestion)
	}

	lines = append(lines, " "+muted.Render(keys))

	return strings.Join(lines, "\n")
}

func joinPanes(panes []string, height int) string {
	separator := muted.Render("│") + "\n"
	return lipgloss.JoinHorizontal(lipgloss.Top, joinWith(panes, strings.TrimSuffix(strings.Repeat(separator, height), "\n"))...)
}

func (m *app) paneHeight() int {
	height := m.height - 2
	if m.reactionSuggestionRow(m.width-2) != "" {
		height--
	}

	return height
}

func (m *app) conversationPane(thread bool, width, height int) string {
	ch := m.conversationChannel(thread)

	title := ch.DisplayName
	if title == "" {
		title = "Channel"
	}

	view, target := &m.timeline, channelPane

	if thread {
		title = "Thread · " + title
		view, target, ch = &m.discussion, threadPane, channel{}
	} else if isFeed(ch.ID) {
		title = m.feedTitle(ch)
	}

	lines := m.composerLines(thread, width, height)

	transcript := m.transcript(title, view, width, height-len(lines), m.focus == target && !m.composing, !thread, ch)
	if len(lines) == 0 {
		return transcript
	}

	return transcript + "\n" + pane(lines, width)
}

func (m *app) composerLines(thread bool, width, height int) []string {
	if !thread && isFeed(m.active) {
		return nil
	}

	label, target := "Message", channelPane

	key := draftKey{channel: m.conversationChannelID(thread)}
	if thread {
		label, target, key.root = "Reply", threadPane, m.root
	}

	files := m.draftFiles[key]
	if len(files) > 0 {
		label = "Caption"
	}

	lines := []string{muted.Render(tui.ComposerRule(label, width))}

	if len(files) > 0 {
		name := tui.AttachmentName(filepath.Base(files[0].Path))
		if len(files) > 1 {
			name = fmt.Sprintf("%d files · %s", len(files), name)
		}

		indent := strings.Repeat(" ", tui.ComposerIndent)
		lines = append(lines, indent+accent.Render(tui.Clip("File: "+name, max(1, width-len(indent)-10)))+" "+muted.Render("[Ctrl+O]"))
	}

	if m.composing && m.focus == target {
		lines = append(lines, strings.Split(m.draft.View(), "\n")...)

		rows := min(len(m.mention.users), 5, max(0, height-len(lines)-2))
		if rows > 0 {
			start := max(0, m.mention.selected-rows+1)
			for i, p := range m.mention.users[start : start+rows] {
				name := "@" + p.Username
				if display := p.label(); display != p.Username {
					name += " · " + display
				}

				name = tui.Clip(name, width-5)
				if start+i == m.mention.selected {
					lines = append(lines, "  "+picked.Render("› "+name))
				} else {
					lines = append(lines, "    "+name)
				}
			}
		}
	} else if text := m.drafts[key]; text != "" {
		room := max(1, min(tui.ComposerLineLimit(m.height), height-2-len(lines)))
		for _, line := range tui.Wrap(text, tui.ComposerTextWidth(width), room) {
			lines = append(lines, strings.Repeat(" ", tui.ComposerIndent)+line)
		}
	} else {
		lines = append(lines, strings.Repeat(" ", tui.ComposerIndent)+muted.Render("i write"))
	}

	return lines
}

func joinWith(items []string, separator string) []string {
	out := make([]string, 0, len(items)*2)
	for i, item := range items {
		if i > 0 {
			out = append(out, separator)
		}

		out = append(out, item)
	}

	return out
}

func (m *app) conversationFooter(status, keys string, width int) (string, string) {
	if m.deleting {
		return "Deleting…", ""
	}

	if m.reactionTarget.ID != "" {
		return m.reactionFooter(width)
	}

	return tui.Clip(status, width), tui.Clip(keys, width)
}

func (m *app) hints() string {
	switch {
	case m.sending:
		return "Sending…  Ctrl+C quit"
	case m.composing:
		if len(m.mention.users) != 0 {
			return "↑↓ choose  Tab or Enter insert  Esc dismiss"
		}

		return "Enter send  Ctrl+V paste  Alt+A attach  Alt+T text file  Ctrl+O draft files  Ctrl+X editor  Ctrl+J newline  @ mention  Esc keep draft"
	case m.filtering:
		return "↑↓ choose  Enter open  Esc clear"
	case m.focus == channelsPane:
		return "n next  u unread threads  jk move  d DMs  c channels  N alerts  / chats  Ctrl+F messages  S settings"
	case m.focus == threadPane:
		return "n next  u unread threads  m read  jk scroll  Ctrl+U/D page  F expand  ? messages  w/W follow  v read  z react  Z authors  x details  JK posts  h back  m read  D delete  i reply  a attach  V files  o open  O links/files"
	case isFeed(m.active):
		return "n next  u unread threads  jk scroll  Ctrl+U/D page  Enter open  f filter  w/W follow  R refresh  h chats"
	default:
		keys := "n next  u unread threads  jk scroll  Ctrl+U/D page  F expand  ? messages  w/W follow  v read  z react  Z authors  JK posts  t thread  m read  D delete  i write  a attach  V files  o open  O links/files"
		if m.preferences.HideChatListWhenOpen {
			keys = "h chats  " + keys
		}

		return keys
	}
}

func (m *app) sidebarItems() []channel {
	favorites, direct, channels := m.channelGroups()
	items := make([]channel, 0, len(favorites)+len(direct)+len(channels)+3+len(feedChannels))

	searching := strings.TrimSpace(m.query.Value()) != ""
	if !searching {
		for _, feed := range feedChannels {
			if feed.ID == discussionsID {
				for _, ch := range m.channels {
					feed.Unread += len(ch.Threads)
				}
			}

			items = append(items, feed)
		}
	}

	if len(favorites) > 0 {
		items = append(items, channel{DisplayName: "Favorites"})
		items = append(items, favorites...)
	}

	group := func(title string, chats []channel, unreadOnly bool) {
		if len(chats) == 0 {
			return
		}

		items = append(items, channel{DisplayName: "▾ " + title})

		for _, ch := range chats {
			if searching || !unreadOnly || ch.hasUnread() || ch.ID == m.active {
				items = append(items, ch)
			}
		}
	}
	group("Direct messages", direct, m.preferences.HideReadDirect)
	group("Channels", channels, m.preferences.HideReadChannels)

	return items
}

func (m *app) sidebar(width, height int) string {
	lines := make([]string, height)
	lines[0] = heading.Render(" " + tui.Clip(m.sidebarTitle(), width-2))

	items := m.sidebarItems()
	if len(items) == 0 {
		label := "No matching chats"
		if m.channelsLoading {
			label = "Loading chats…"
		}

		lines[1] = " " + muted.Render(tui.Clip(label, width-2))

		return pane(lines, width)
	}

	unreadAfter := func(index int) int {
		unread := 0

		for _, row := range items[index:] {
			if row.hasUnread() {
				unread++
			}
		}

		return unread
	}
	i := slices.IndexFunc(items, func(row channel) bool { return row.ID != "" && row.ID == m.channelCursor })
	room := height - 1
	start := max(0, i-room+1)

	end := min(len(items), start+room)
	if unreadAfter(end) > 0 && room > 1 {
		room--
		start = max(0, i-room+1)
		end = min(len(items), start+room)
		mark := fmt.Sprintf("↓ %d unread chats", unreadAfter(end))
		lines[height-1] = " " + unreadColor.Render(tui.Clip(mark, width-2))
	}

	for row, ch := range items[start:end] {
		if ch.ID == "" {
			lines[row+1] = " " + heading.Render(tui.Clip(ch.DisplayName, width-2))
			continue
		}

		mark, badge := "╵", ""
		if ch.hasUnread() {
			mark = "●"
		}

		if ch.ID == m.active || ch.ID == m.channelCursor {
			mark = "›"
		}

		if ch.Mentions > 0 {
			badge = fmt.Sprintf("@%d", ch.Mentions)
		} else if ch.Unread > 0 {
			badge = fmt.Sprint(ch.Unread)
		}

		if len(ch.Threads) > 0 {
			var replies int
			for _, signal := range ch.Threads {
				replies += signal.unread
			}

			badge = strings.TrimSpace(badge + fmt.Sprintf(" ↳%d", replies))
		}

		if m.watchesChannel(ch.ID) {
			badge = strings.TrimSpace("♢ " + badge)
		}

		badge = tui.Clip(badge, 10)
		available := width - 4 - uniseg.StringWidth(badge)
		name := tui.Clip(ch.DisplayName, available)
		left := mark + " " + padRight(name, available)

		selection := ch.ID == m.channelCursor && m.focus == channelsPane || ch.ID == m.active && m.focus != channelsPane
		if selection {
			lines[row+1] = " " + picked.Render(left+" "+badge)
		} else {
			if ch.hasUnread() {
				left = unreadColor.Render(mark) + " " + authorColor.Render(padRight(name, available))
			}

			lines[row+1] = " " + left + " " + unreadColor.Render(badge)
		}
	}

	return pane(lines, width)
}

func (m *app) transcriptBody(p post, widthBody, room int, showReplies bool, signal threadSignal, tableDetails bool) (message, thumbnail []string, reactions, footer string, maxBody int) {
	message = postLines(p, widthBody, m.messageFormatting(), tableDetails)
	if showReplies && (p.Replies > 0 || signal.unread > 0) {
		replies := "replies"
		if p.Replies == 1 {
			replies = "reply"
		}

		if signal.unread > 0 {
			footer = fmt.Sprintf("↳ %d new  [t]", signal.unread)
		} else {
			footer = fmt.Sprintf("↳ %d %s  [t]", p.Replies, replies)
		}
	}

	maxBody = room - 1

	reactions = reactionSummary(p.Metadata.Reactions)
	if reactions != "" && maxBody > 1 {
		maxBody--
	} else {
		reactions = ""
	}

	if footer != "" && maxBody > 1 {
		maxBody--
	} else {
		footer = ""
	}

	if image := m.inlineImages.Get("file:" + inlineAttachmentID(p)); image != nil &&
		image.Width <= widthBody && len(image.Rows) < maxBody {
		thumbnail = image.Rows
		maxBody -= len(thumbnail)
	}

	return message, thumbnail, reactions, footer, maxBody
}

func (m *app) transcript(title string, view *conversation, width, height int, focused, showReplies bool, ch channel) string {
	lines := make([]string, height)
	lines[0] = heading.Render("  " + tui.Clip(title, width-3))

	if len(view.posts) == 0 {
		label := "No messages"
		if view.loading {
			label = "Loading…"
		} else if view.before != "" {
			label = "No root messages in this page. Press k for older messages."
		}

		lines[1] = "  " + muted.Render(tui.Clip(label, width-3))

		return pane(lines, width)
	}

	bottom := height

	var newerDay string

	for i, p := range view.posts[view.selected:] {
		date := time.UnixMilli(p.displayTime()).Local()
		day := date.Format(time.DateOnly)

		separator := ""
		if newerDay != "" && day != newerDay {
			separator = newerDay
		}

		gap := false

		if i > 0 && separator == "" {
			previous := view.posts[view.selected+i-1]
			gap = p.UserID == "" || p.UserID != previous.UserID ||
				p.CreatedAt/60_000 != previous.CreatedAt/60_000
		}

		room := bottom - 1
		if gap {
			room--
		}

		if separator != "" {
			room--
		}

		if room < 2 {
			break
		}

		source := ch

		var context string

		if isFeed(ch.ID) {
			source = m.channelByID(p.ChannelID)

			context = source.DisplayName
			if p.feedLabel != "" {
				if context == "" {
					context = p.feedLabel
				} else {
					context = p.feedLabel + " · " + context
				}
			}
		}

		block := m.transcriptPost(p, width, room, transcriptPostOptions{
			focused: i == 0 && focused, showReplies: showReplies, signal: m.postThreadSignal(source, p.ID),
			channel:    context,
			unread:     view.readThrough > 0 && p.CreatedAt > view.readThrough && !p.Own,
			scrollable: i == 0, tableDetails: i == 0 && view.tableDetails, offset: view.bodyOffset,
		})

		if gap {
			bottom--
		}

		if separator != "" {
			bottom--
			lines[bottom] = transcriptDateDivider(separator, width)
		}

		bottom -= len(block)
		copy(lines[bottom:], block)

		newerDay = day
	}

	if bottom > 1 && newerDay != "" {
		lines[bottom-1] = transcriptDateDivider(newerDay, width)
	}

	return pane(lines, width)
}

type transcriptPostOptions struct {
	focused, unread, showReplies bool
	scrollable, tableDetails     bool
	signal                       threadSignal
	offset                       int
	channel                      string
}

func (m *app) transcriptPost(p post, width, room int, opts transcriptPostOptions) []string {
	name := authorColor
	if p.Own {
		name = accent.Bold(true)
	} else if p.UserID != "" {
		color := tui.ColorForNick(p.UserID)
		name = name.Foreground(lipgloss.Color(fmt.Sprint(color.ANSI - 30)))
	}

	mark := " "
	rail := muted

	if opts.focused {
		mark = "›"
		rail = accent
	}

	widthBody := max(1, width-11)
	clock := time.UnixMilli(p.displayTime()).Local().Format("15:04")

	timestamp := muted.Render(clock)
	if opts.unread {
		timestamp = unreadColor.Render(clock + " ●")
	}

	message, thumbnail, reactions, footer, maxBody := m.transcriptBody(p, widthBody, room, opts.showReplies, opts.signal, opts.tableDetails)
	start := 0

	var position string

	if opts.scrollable && len(message) > maxBody {
		start = min(opts.offset, len(message)-maxBody)
		position = fmt.Sprintf("  %d–%d/%d", start+1, start+maxBody, len(message))
	}

	titleRoom := width - 2 - lipgloss.Width(timestamp) - lipgloss.Width(position)
	avatar := m.messageAvatar(p)

	avatarPrefix := ""
	if avatar != nil && len(avatar.Rows) == 2 && len(message) > 0 && titleRoom > avatar.Width+3 {
		avatarPrefix = avatar.Rows[0] + " "
		titleRoom -= avatar.Width + 1
	} else {
		avatar = nil
	}

	context := ""
	if opts.channel != "" {
		context = tui.Clip(" · "+opts.channel, max(0, titleRoom-10))
		titleRoom -= lipgloss.Width(context)
	}

	block := []string{mark + avatarPrefix + timestamp + " " + name.Render(tui.Clip(p.Author, titleRoom)) +
		muted.Render(context+position)}
	truncated := len(message) > start+maxBody

	message = message[start:min(len(message), start+maxBody)]
	if !opts.scrollable && truncated {
		message[len(message)-1] = ansi.Cut(message[len(message)-1], 0, widthBody-2) + " …"
	}

	for row, line := range message {
		gutter := "       "
		if row == 0 && avatar != nil {
			gutter = " " + avatar.Rows[1] + strings.Repeat(" ", 6-avatar.Width)
		}

		block = append(block, gutter+rail.Render("│")+" "+line)
	}

	for _, line := range thumbnail {
		block = append(block, "       "+rail.Render("│")+" "+line)
	}

	if reactions != "" {
		block = append(block, "       "+rail.Render("│")+" "+reactionColor.Render(m.inlineReactionLine(p.Metadata.Reactions, widthBody)))
	}

	if footer != "" {
		style := accent
		if opts.signal.unread > 0 {
			style = unreadColor
		}

		block = append(block, "       "+style.Render(tui.Clip(footer, widthBody)))
	}

	return block
}

func transcriptDateDivider(day string, width int) string {
	label := " " + day + " "

	rule := width - len(label)
	if rule < 2 {
		return dateColor.Render(tui.Clip(day, width))
	}

	left := rule / 2

	return muted.Render(strings.Repeat("─", left)) + dateColor.Render(label) +
		muted.Render(strings.Repeat("─", rule-left))
}

func (m *app) readView() string {
	title := m.reader.Author + " · " + time.UnixMilli(m.reader.CreatedAt).Local().Format("2006-01-02 15:04")
	lines := m.readLines(m.reader, m.width-4)
	suggestion := m.reactionSuggestionRow(m.width - 2)
	size := m.readerHeight()
	start := min(m.readerOffset, max(0, len(lines)-size))

	screen := []string{"  " + heading.Render(tui.Clip(title, m.width-4)), ""}
	if avatar := m.messageAvatar(m.reader); avatar != nil && len(avatar.Rows) == 2 {
		screen[0] = "  " + avatar.Rows[0] + " " + heading.Render(tui.Clip(title, m.width-5-avatar.Width))
		screen[1] = "  " + avatar.Rows[1]
	}

	for row := range size {
		if start+row < len(lines) {
			screen = append(screen, "  "+lines[start+row])
		} else {
			screen = append(screen, "")
		}
	}

	status := m.status
	if status == "" && len(lines) > size {
		status = fmt.Sprintf("Lines %d–%d of %d", start+1, start+size, len(lines))
	}

	status, keys := m.conversationFooter(status, "Ctrl+U/D page  jk scroll  g/G start/end  z react  Z authors  O links/files  o open  D delete  Esc back", m.width-2)

	screen = append(screen, " "+status)
	if suggestion != "" {
		screen = append(screen, " "+suggestion)
	}

	screen = append(screen, " "+muted.Render(keys))

	return strings.Join(screen, "\n")
}

func (m *app) readerHeight() int {
	height := m.height - 4
	if m.reactionSuggestionRow(m.width-2) != "" {
		height--
	}

	return max(1, height)
}

func (m *app) readLines(p post, width int) []string {
	lines := postLines(p, width, m.messageFormatting(), true)
	if summary := reactionSummary(p.Metadata.Reactions); summary != "" {
		lines = append(lines, reactionColor.Render(tui.ReactionLine(summary, width, false)))
	}

	return lines
}

func postAttachmentLabel(p post) string {
	if len(p.Files) == 0 {
		return ""
	}

	label := "attachments"
	if len(p.Files) == 1 {
		label = "attachment"
	}

	return fmt.Sprintf("[%d %s · o]", len(p.Files), label)
}

func (m *app) dialogPopup() tui.DialogPopup {
	var render func(int, int, func(string, bool, bool, bool))

	maxWidth := tui.ListPopupWidth

	switch {
	case m.updateDialog != nil:
		render = m.updateDialog.Render
	case m.deleteTarget.ID != "" && !m.deleting:
		confirm := tui.DeleteConfirmation(1, true)
		if m.deleteTarget.RootID == "" {
			confirm.Prompt = "Delete message and any replies for everyone?"
			confirm.ShortPrompt = "Delete with replies?"
		}

		maxWidth = tui.FormPopupWidth
		render = confirm.Render
	case m.unreadChoices != nil:
		render = m.renderUnreadThreads
	case m.messageSearch != nil:
		render = m.messageSearch.Render
	case m.filtering:
		render = m.renderChatSearch
		if m.chatSearchJoin.ID != "" && !m.chatSearchOpening {
			maxWidth = tui.FormPopupWidth
		}
	case m.settingsOpen:
		maxWidth = tui.FormPopupWidth
		render = m.renderSettings
	case m.reactionTarget.ID != "" && m.reactionCustomIndex >= 0:
		render = m.renderEmojiCatalog
	case m.reactionAuthors != nil:
		d := m.reactionAuthors
		render = func(width, height int, row func(string, bool, bool, bool)) {
			tui.ListDialog(tui.ReactionAuthorsTitle, d.Rows, d.Selected, width, height, d.Status, tui.ReactionAuthorsHint, row)
		}
	case m.textAttachmentPrompt != nil:
		maxWidth = tui.FormPopupWidth
		render = func(width, _ int, row func(string, bool, bool, bool)) {
			m.textAttachmentPrompt.Render(width, row)
		}
	case m.attachmentPicker != nil:
		render = m.attachmentPicker.Render
	case m.resourceMode != "" || len(m.resources) != 0:
		render = m.renderResources
	default:
		return tui.DialogPopup{}
	}

	return tui.FloatingDialog(m.width, m.height, maxWidth, render, popupStyle)
}

func (m *app) renderResources(width, height int, row func(string, bool, bool, bool)) {
	title := m.resourceMode
	if title == "" {
		title = "Links and attachments"
	}

	choices := make([]string, len(m.resources))
	for i, item := range m.resources {
		kind, label := "Link", item.label
		if item.fileID != "" || item.localPath != "" {
			kind = "File"
		} else if item.link != "" && label != item.link {
			label += "  ·  " + item.link
		}

		choices[i] = kind + "  " + label
	}

	status := ""
	if m.resourceMode != "" {
		status = m.status
	}

	hint := tui.OpenAttachmentHint
	if m.resourceMode == draftAttachmentTitle {
		hint = tui.DraftAttachmentListHint(len(choices))
		if len(choices) == 0 {
			if status == "" {
				status = "No files in this draft"
			}
		}
	}

	tui.ListDialog(title, choices, m.resourceIndex, width, height, status, hint, row)
}

func pane(lines []string, width int) string {
	for i, line := range lines {
		lines[i] = padRight(line, width)
	}

	return strings.Join(lines, "\n")
}

func padRight(text string, width int) string {
	return text + strings.Repeat(" ", max(0, width-lipgloss.Width(text)))
}

func popupStyle(text string, role tui.PopupRole) string {
	const reset = "\x1b[0m"

	switch role {
	case tui.PopupTitle:
		return reset + heading.Render(text)
	case tui.PopupSelected:
		return reset + picked.Render(text)
	case tui.PopupBorder, tui.PopupMuted:
		return reset + muted.Render(text)
	default:
		return reset + text
	}
}
