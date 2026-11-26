package loop

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/danilrwx/telega/tui"
	"github.com/danilrwx/telega/updater"
)

type focus int

const (
	channelsPane focus = iota
	channelPane
	threadPane
)

type conversation struct {
	posts        []post
	selected     int
	bodyOffset   int
	tableDetails bool
	before       string
	pendingOlder int
	pendingPage  bool
	request      uint64
	loading      bool
	anchored     bool
	readThrough  int64
}

func (c *conversation) selectedPost() post {
	if len(c.posts) == 0 {
		return post{}
	}

	return c.posts[c.selected]
}

func (c *conversation) newest() int64 {
	if len(c.posts) == 0 {
		return 0
	}

	return c.posts[0].CreatedAt
}

func (c *conversation) initializeReadBoundary(unread int) {
	if c.readThrough != 0 || len(c.posts) == 0 {
		return
	}

	if unread >= len(c.posts) {
		c.readThrough = 1
		return
	}

	c.readThrough = c.posts[unread].CreatedAt
}

func (c *conversation) selectIndex(index int) {
	if c.selected != index {
		c.selected, c.bodyOffset, c.tableDetails = index, 0, false
	}
}

func (c *conversation) replace(posts []post) {
	old := c.selectedPost().ID
	followLatest := c.selected == 0 && c.pendingOlder == 0 && c.bodyOffset == 0 && !c.tableDetails

	c.posts = posts
	if !followLatest {
		if i := slices.IndexFunc(posts, func(p post) bool { return p.ID == old }); i >= 0 {
			c.selected = i
			return
		}
	}

	c.selected, c.bodyOffset, c.tableDetails = 0, 0, false
}

type app struct {
	updateApp                        updater.App
	pendingUpdate, updateDialog      *tui.UpdateDialog
	ctx                              context.Context
	api                              *client
	width, height                    int
	inlineImages                     *tui.InlineImages
	inlineLoading                    bool
	focus                            focus
	fullPane                         bool
	channels                         []channel
	chatStatus                       *tui.ChatStatus
	channelCursor, active, root      string
	timeline, discussion             conversation
	sequence                         uint64
	channelsLoading                  bool
	preferences                      sidebarSettings
	layoutSettings                   tui.LayoutSettings
	configEnabled, settingsOpen      bool
	settingsCursor                   int
	filtering, composing, sending    bool
	followed                         []followedThread
	discussionsLoading               bool
	discussionsComplete              bool
	discussionsUpdatedAt             time.Time
	discussionCachePath              string
	feedFilter                       int
	activityJump                     uint64
	threadChannelID                  string
	followChanging                   bool
	messageSearch                    *tui.MessageSearch
	messageSearchScope               channel
	messageSearchRoot                string
	messageSearchPage                int
	messageHits                      []post
	chatSearchOpening                bool
	chatSearchIndex                  int
	chatSearchID                     uint64
	chatSearchResults                []chatSearchChoice
	chatSearchJoin                   channel
	chatSearchStatus                 string
	query                            textinput.Model
	draft                            textarea.Model
	draftStore                       *tui.DraftStore
	drafts                           map[draftKey]string
	draftFiles                       map[draftKey][]draftAttachment
	clipboardDraft                   draftKey
	attachmentPicker                 *tui.FilePicker
	textAttachmentPrompt             *tui.TextAttachmentPrompt
	attachmentEditing                bool
	mention                          mentionState
	mentionID                        uint64
	reader                           post
	readerOffset                     int
	deleteTarget                     post
	deleting                         bool
	reactionTarget                   post
	reacting                         bool
	reactionQueue                    []string
	reactionCustomIndex              int
	reactionSuggestionIndex          int
	reactionEmojiQuery               textinput.Model
	emojiCatalog                     []string
	emojiCatalogLoading              bool
	emojiPreviewLoading              bool
	emojiCatalogUpdated              time.Time
	emojiCatalogErr                  error
	reactionClosing, reactionChanged bool
	reactionAuthors                  *tui.ReactionAuthors
	resources                        []resource
	resourceIndex                    int
	resourceOpening                  bool
	resourceMode                     string
	status                           string
}

func (m *app) username() string {
	if m.api == nil {
		return ""
	}

	return m.api.me.Username
}

type (
	tickMsg        time.Time
	channelsResult struct {
		items []channel
		err   error
	}
)

type historyResult struct {
	request           uint64
	thread, older     bool
	marked            bool
	channelID, rootID string
	page              historyPage
	err, markErr      error
}
type sendResult struct {
	files []draftAttachment
	err   error
}
type readResult struct {
	channelID string
	through   int64
	err       error
}

func newApp(ctx context.Context, api *client) *app {
	query := textinput.New()
	query.PlaceholderStyle, query.CompletionStyle = muted, muted
	query.Prompt = "/ "
	query.Placeholder = "Filter chats"
	query.CharLimit = 120
	draft := textarea.New()
	draft.Prompt = strings.Repeat(" ", tui.ComposerIndent)
	draft.FocusedStyle = textarea.Style{Placeholder: muted, Prompt: muted}
	draft.BlurredStyle = draft.FocusedStyle
	draft.ShowLineNumbers = false
	draft.Placeholder = "Write a message"
	draft.CharLimit = messageLimit
	draft.MaxHeight = 0
	draft.MaxWidth = 0
	draft.SetHeight(1)

	emojiQuery := textinput.New()
	emojiQuery.PlaceholderStyle, emojiQuery.CompletionStyle = muted, muted
	emojiQuery.Prompt, emojiQuery.Placeholder, emojiQuery.CharLimit = " / ", "Find emoji", 80

	return &app{
		ctx: ctx, api: api, query: query, draft: draft,
		reactionCustomIndex: -1, reactionEmojiQuery: emojiQuery, drafts: make(map[draftKey]string), draftFiles: make(map[draftKey][]draftAttachment),
	}
}

func nextTick() tea.Cmd {
	return tea.Tick(5*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m *app) Init() tea.Cmd {
	return tea.Batch(nextTick(), m.loadChannels(), m.checkUpdate())
}

func (m *app) loadChannels() tea.Cmd {
	if m.channelsLoading {
		return nil
	}

	m.channelsLoading = true

	return func() tea.Msg {
		items, err := m.api.channels(m.ctx)
		return channelsResult{items: items, err: err}
	}
}

func (m *app) loadHistory(thread, older, mark, force bool) tea.Cmd {
	if isFeed(m.active) && !thread {
		if older || force {
			return m.loadFeed(older)
		}

		return nil
	}

	view, id := &m.timeline, m.active
	if thread {
		view, id = &m.discussion, m.root
	}

	if id == "" || view.anchored && !older && !force || (view.loading && !force) || (older && view.before == "") {
		return nil
	}

	m.sequence++
	request := m.sequence
	view.request, view.loading = request, true

	before := ""
	if older {
		before = view.before
	}

	ch := m.conversationChannel(thread)
	signal := m.postThreadSignal(ch, id)

	return func() tea.Msg {
		msg := historyResult{request: request, thread: thread, older: older, channelID: ch.ID}
		if thread {
			msg.page.posts, msg.err = m.api.thread(m.ctx, id)

			msg.rootID = id
			if mark && msg.err == nil && len(msg.page.posts) != 0 {
				if signal.root != "" {
					msg.markErr = m.api.markThreadRead(m.ctx, signal, msg.page.posts[0].CreatedAt)
				}

				msg.marked = msg.markErr == nil
			}
		} else {
			msg.channelID = id

			msg.page, msg.err = m.api.timeline(m.ctx, id, before)
			if mark && msg.err == nil {
				msg.markErr = m.api.markRead(m.ctx, id)
				msg.marked = msg.markErr == nil
			}
		}

		return msg
	}
}

func (m *app) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	model, cmd := m.update(msg)

	if m.pendingUpdate != nil && !m.composing && !m.attachmentEditing && len(m.dialogPopup().Lines) == 0 {
		m.updateDialog, m.pendingUpdate = m.pendingUpdate, nil
	}

	if next := m.loadVisibleImages(); next != nil {
		return model, tea.Batch(cmd, next)
	}

	return model, cmd
}

func (m *app) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.draft.SetWidth(max(8, m.conversationWidth()))

		if m.composing {
			m.fitComposer()
		}

		m.query.Width = max(10, m.width-7)
		m.reactionEmojiQuery.Width = max(10, m.width-6)
	case activityResult:
		return m, m.receiveActivity(msg)
	case activityJumpResult:
		return m, m.receiveActivityJump(msg)
	case discussionsResult:
		return m, m.receiveDiscussions(msg)
	case followThreadResult:
		return m, m.receiveFollowChange(msg)
	case searchPostsResult:
		m.receiveSearchPosts(msg)
	case searchJumpResult:
		return m, m.receiveSearchJump(msg)
	case chatSearchTick:
		if m.filtering && uint64(msg) == m.chatSearchID {
			query := strings.TrimSpace(m.query.Value())

			return m, func() tea.Msg {
				items, err := m.api.directorySearch(m.ctx, query)
				return chatSearchResult{uint64(msg), items, err}
			}
		}
	case chatSearchResult:
		if m.filtering && msg.id == m.chatSearchID {
			m.chatSearchResults, m.chatSearchStatus = msg.items, ""
			if msg.err != nil {
				m.chatSearchStatus = "Loop directory unavailable: " + msg.err.Error()
			}
		}
	case chatSearchOpened:
		return m, m.receiveChatSearchOpened(msg)
	case updateAvailable:
		m.pendingUpdate = msg.dialog
	case updateInstalled:
		if m.updateDialog == msg.dialog {
			m.updateDialog.Complete(msg.err)
		}

	case tickMsg:
		var sidebar, discussions tea.Cmd
		if m.sequence == 0 || time.Time(msg).Unix()/5%3 == 0 {
			sidebar = m.loadChannels()
			if m.active == discussionsID && !m.discussionsLoading {
				discussions = m.loadDiscussions(true)
			}
		}

		if m.deleteTarget.ID != "" || m.reactionTarget.ID != "" {
			return m, tea.Batch(nextTick(), sidebar, discussions)
		}
		// Polling never acknowledges reads, even when the open panes are hidden.
		return m, tea.Batch(nextTick(), sidebar, discussions, m.loadHistory(false, false, false, false), m.loadHistory(true, false, false, false))
	case channelsResult:
		m.channelsLoading = false
		if msg.err != nil {
			m.status = "Chats: " + msg.err.Error()

			break
		}

		m.channels = msg.items
		m.syncDiscussionUnread()

		if m.chatStatus != nil {
			m.chatStatus.Publish(chatStatusCounts(m.channels))
		}

		m.ensureChannelCursor()

		if strings.HasPrefix(m.status, "Chats: ") {
			m.status = ""
		}

	case historyResult:
		return m, m.receiveHistory(msg)
	case mentionQuery:
		return m, m.queryMention(uint64(msg))
	case mentionResult:
		if m.composing && m.mention.active && msg.id == m.mentionID {
			m.mention.users = msg.users
			if msg.err != nil {
				m.status = "Mention lookup: " + msg.err.Error()
			}
		}
	case clipboardResult:
		return m, m.receiveClipboard(msg)
	case attachmentEditResult:
		return m, m.finishAttachmentEdit(msg)
	case vimResult:
		if msg.err != nil {
			m.status = "Editor: " + msg.err.Error()
			break
		}

		m.status = ""
		m.setDraft(msg.text)
		m.saveDraft(m.draft.Value())

		return m, m.draft.Focus()
	case emojiPage:
		return m, m.receiveEmojiPage(msg)
	case reactionAuthorsResult:
		if m.reactionAuthors != nil && m.reactionAuthors == msg.dialog {
			m.reactionAuthors.SetRows(msg.rows, msg.status)
		}
	case reactionResult:
		return m, m.receiveReaction(msg)
	case deleteResult:
		return m, m.receiveDelete(msg)
	case sendResult:
		m.sending = false
		if msg.err != nil {
			m.saveDraftFiles(msg.files)
			m.status = "Send failed: " + msg.err.Error()

			break
		}

		m.saveDraft("")

		files := m.draftFiles[m.draftKey()]
		if m.saveDraftFiles(nil) {
			for _, file := range files {
				discardAttachmentCopy(file)
			}
		}

		m.draft.Reset()
		m.draft.Blur()

		m.composing = false
		if !isFeed(m.active) {
			m.timeline.selected, m.timeline.bodyOffset, m.timeline.tableDetails = 0, 0, false
		}

		m.discussion.selected, m.discussion.bodyOffset, m.discussion.tableDetails = 0, 0, false

		return m, tea.Batch(m.loadHistory(false, false, false, true), m.loadHistory(true, false, false, true))
	case readResult:
		if msg.err != nil {
			m.status = "Mark as read: " + msg.err.Error()
		} else {
			m.setRead(msg.channelID)

			if m.active == msg.channelID {
				m.timeline.readThrough = max(m.timeline.readThrough, msg.through)
			}
		}
	case inlineImageResult:
		m.inlineLoading = false
		if m.inlineImages != nil {
			m.inlineImages.Store(msg.key, msg.image)
		}
	case imagePreviewResult:
		if msg.reactionPost != "" {
			m.emojiPreviewLoading = false
			if msg.reactionPost != m.reactionTarget.ID || m.reactionCustomIndex < 0 {
				if m.status == "Loading emoji image…" {
					m.status = ""
				}

				break
			}
		} else {
			m.resourceOpening = false
		}

		if msg.err != nil {
			m.status = "Preview: " + msg.err.Error()
			break
		}

		m.status = ""

		return m, tea.Exec(&imagePreview{image: msg.image, title: msg.title, path: msg.path},
			func(err error) tea.Msg { return resourceResult{err: err} })
	case resourceResult:
		m.resourceOpening = false

		m.status = ""
		if msg.err != nil {
			m.status = "Open failed: " + msg.err.Error()
		}
	case tea.KeyMsg:
		return m, m.key(msg)
	default:
		if m.reactionEmojiQuery.Focused() {
			return m, m.updateEmojiQuery(msg)
		}

		if m.attachmentPicker != nil || m.textAttachmentPrompt != nil || m.messageSearch != nil {
			return m, nil
		}

		if m.composing && m.resourceMode == "" && len(m.resources) == 0 && !m.sending {
			text := m.draft.Value()

			var cmd tea.Cmd

			m.draft, cmd = m.draft.Update(msg)
			if m.draft.Value() != text {
				m.fitComposer()
				m.saveDraft(m.draft.Value())

				return m, tea.Batch(cmd, m.refreshMention())
			}

			return m, cmd
		}

		if m.filtering {
			old := m.query.Value()

			var cmd tea.Cmd

			m.query, cmd = m.query.Update(msg)
			if old != m.query.Value() {
				return m, tea.Batch(cmd, m.chatSearchChanged())
			}

			return m, cmd
		}
	}

	return m, nil
}

func (m *app) receiveHistory(msg historyResult) tea.Cmd {
	view := &m.timeline
	label := "History: "

	if msg.thread {
		view = &m.discussion
		label = "Thread: "
	}

	if msg.request != view.request || !view.loading {
		return nil
	}

	view.loading = false
	// Keep the target visible while the message action is open.
	if m.deleteTarget.ID != "" || m.reactionTarget.ID != "" {
		return nil
	}

	if msg.err != nil {
		view.pendingOlder, view.pendingPage = 0, false
		m.status = label + msg.err.Error()

		return nil
	}

	if strings.HasPrefix(m.status, label) {
		m.status = ""
	}

	if msg.thread {
		view.replace(msg.page.posts)
		view.initializeReadBoundary(m.postThreadSignal(m.conversationChannel(true), m.root).unread)

		if msg.markErr != nil {
			m.status = "Mark thread as read: " + msg.markErr.Error()
		} else if msg.marked {
			m.clearThread(m.root)

			for i := range m.followed {
				if m.followed[i].Post.ID == m.root {
					m.followed[i].Unread, m.followed[i].Mentions = 0, 0
					break
				}
			}
		}

		return nil
	}

	if msg.older {
		wasEmpty := len(view.posts) == 0

		seen := make(map[string]bool, len(view.posts))
		for _, p := range view.posts {
			seen[p.ID] = true
		}

		for _, p := range msg.page.posts {
			if !seen[p.ID] {
				view.posts = append(view.posts, p)
			}
		}

		view.before = msg.page.before

		return m.continuePaging(wasEmpty)
	}

	fresh := msg.page.posts

	previousInPage := len(view.posts) > 0 && view.posts[0].CreatedAt >= msg.page.floor
	if !previousInPage {
		view.before = msg.page.before
	}

	if previousInPage {
		seen := make(map[string]bool, len(fresh))
		for _, p := range fresh {
			seen[p.ID] = true
		}

		for _, p := range view.posts {
			if p.CreatedAt <= msg.page.floor && !seen[p.ID] {
				fresh = append(fresh, p)
			}
		}
	}

	view.replace(fresh)
	view.initializeReadBoundary(m.activeChannel().Unread)

	if msg.markErr != nil {
		m.status = "Mark as read: " + msg.markErr.Error()
	}

	if msg.marked {
		m.setRead(m.active)
	}

	return m.continuePaging(false)
}

func (m *app) continuePaging(wasEmpty bool) tea.Cmd {
	view := &m.timeline
	if view.pendingOlder == 0 {
		return nil
	}

	if len(view.posts) > 0 {
		start := view.selected
		if wasEmpty {
			start = -1
		}

		moved := min(view.pendingOlder, len(view.posts)-1-start)
		target := max(0, start+moved)

		view.pendingOlder -= moved
		if moved > 0 && view.pendingPage {
			index, offset, stopped := m.pageTarget(view, start, target, false)
			view.selectIndex(index)

			view.bodyOffset = offset
			if stopped {
				view.pendingOlder = 0
			}
		} else {
			view.selectIndex(target)
		}
	}

	if view.before == "" {
		view.pendingOlder, view.pendingPage = 0, false
	}

	if view.pendingOlder != 0 {
		return m.loadHistory(false, true, false, false)
	}

	view.pendingPage = false

	return nil
}

func (m *app) moveHistory(view *conversation, delta int) tea.Cmd {
	view.pendingPage = false
	if delta < 0 {
		view.pendingOlder = 0
		view.selectIndex(max(0, view.selected+delta))

		return nil
	}

	target := view.selected + view.pendingOlder + delta
	last := max(0, len(view.posts)-1)
	view.selectIndex(min(target, last))

	if m.focus == channelPane && target > last && view.before != "" {
		view.pendingOlder = target - last
		return m.loadHistory(false, true, false, false)
	}

	return nil
}

func (m *app) historyViewport(thread bool) (width, room int) {
	layout := m.layout()

	paneWidth := layout.channel
	if thread {
		paneWidth = layout.thread
	}

	height := m.paneHeight()

	return max(1, paneWidth-11), height - len(m.composerLines(thread, paneWidth, height)) - 1
}

// pageTarget stops a page jump on the first message that needs its own scrolling.
func (m *app) pageTarget(view *conversation, start, end int, thread bool) (index, offset int, stopped bool) {
	width, room := m.historyViewport(thread)
	if start == end || room < 2 {
		return end, 0, false
	}

	step := 1
	if end < start {
		step = -1
	}

	ch := m.conversationChannel(thread)

	for index := start + step; index != end+step; index += step {
		p := view.posts[index]

		source := ch
		if isFeed(ch.ID) {
			source = m.channelByID(p.ChannelID)
		}

		body, _, _, _, limit := m.transcriptBody(p, width, room, !thread, m.postThreadSignal(source, p.ID), false)
		if len(body) > limit {
			offset := 0
			if step > 0 {
				offset = len(body) - limit
			}

			return index, offset, true
		}
	}

	return end, 0, false
}

func (m *app) scrollHistory(view *conversation, key string) tea.Cmd {
	delta := -pageDistance(key, max(1, (m.height-5)/3))
	if len(view.posts) == 0 {
		return m.moveHistory(view, delta)
	}

	thread := m.focus == threadPane

	width, room := m.historyViewport(thread)
	if room >= 2 {
		p := view.selectedPost()

		source := m.conversationChannel(thread)
		if isFeed(source.ID) {
			source = m.channelByID(p.ChannelID)
		}

		body, _, _, _, limit := m.transcriptBody(p, width, room, !thread, m.postThreadSignal(source, p.ID), view.tableDetails)
		last := max(0, len(body)-limit)
		before := min(view.bodyOffset, last)

		view.bodyOffset = max(0, min(last, before+pageDistance(key, limit)))
		if view.bodyOffset != before {
			view.pendingOlder, view.pendingPage = 0, false
			return nil
		}
	}

	last := len(view.posts) - 1

	target := view.selected + delta
	if delta > 0 {
		target += view.pendingOlder
	}

	index, offset, stopped := m.pageTarget(view, view.selected, max(0, min(target, last)), thread)
	if index != view.selected {
		view.selectIndex(index)
		view.bodyOffset = offset
	}

	if !stopped && target > last && !thread && view.before != "" {
		view.pendingOlder, view.pendingPage = target-last, true
		return m.loadHistory(false, true, false, false)
	}

	view.pendingOlder, view.pendingPage = 0, false

	return nil
}

func pageDistance(key string, page int) int {
	switch key {
	case "k", "up":
		return -1
	case "j", "down":
		return 1
	case "pgup":
		return -page
	case "pgdown", " ":
		return page
	case "ctrl+u":
		return -max(1, page/2)
	default: // ctrl+d
		return max(1, page/2)
	}
}

func (m *app) setRead(id string) {
	for i := range m.channels {
		if m.channels[i].ID == id {
			m.channels[i].Unread, m.channels[i].Mentions = 0, 0
			break
		}
	}

	m.ensureChannelCursor()
}

func (m *app) clearThread(root string) {
	for i := range m.channels {
		ch := &m.channels[i]
		ch.Threads = slices.DeleteFunc(ch.Threads, func(signal threadSignal) bool { return signal.root == root })
	}

	m.ensureChannelCursor()
}

func (m *app) channelGroups() (favorites, direct, channels []channel) {
	query := m.query.Value()
	for _, ch := range m.channels {
		if !tui.ChatMatches(ch.DisplayName+" "+ch.Name, ch.Username, query) {
			continue
		}

		switch {
		case ch.Favorite:
			favorites = append(favorites, ch)
		case ch.Direct:
			direct = append(direct, ch)
		default:
			channels = append(channels, ch)
		}
	}

	return favorites, direct, channels
}

func (m *app) filteredChannels() []channel {
	rows := m.sidebarItems()

	items := make([]channel, 0, len(rows))
	for _, row := range rows {
		if row.ID != "" {
			items = append(items, row)
		}
	}

	return items
}

func (m *app) ensureChannelCursor() {
	items := m.filteredChannels()
	if len(items) == 0 {
		m.channelCursor = ""
	} else if slices.IndexFunc(items, func(ch channel) bool { return ch.ID == m.channelCursor }) == -1 {
		m.channelCursor = items[0].ID
		if i := slices.IndexFunc(items, func(ch channel) bool { return !isFeed(ch.ID) }); i >= 0 {
			m.channelCursor = items[i].ID
		}
	}
}

func (m *app) moveChannel(delta int) {
	items := m.filteredChannels()

	i := slices.IndexFunc(items, func(ch channel) bool { return ch.ID == m.channelCursor })
	if len(items) != 0 {
		m.channelCursor = items[max(0, min(i+delta, len(items)-1))].ID
	}
}

func (m *app) openChannel() tea.Cmd {
	if m.channelCursor == "" {
		return nil
	}

	m.query.Reset()

	m.focus, m.fullPane = channelPane, false
	if m.active == m.channelCursor {
		return nil
	}

	m.active, m.root = m.channelCursor, ""
	m.threadChannelID, m.feedFilter, m.activityJump = "", 0, 0
	m.timeline, m.discussion = conversation{}, conversation{}

	m.timeline.readThrough = m.activeChannel().LastViewed
	if m.active == discussionsID {
		m.applyDiscussionFilter()
	}

	m.status = ""
	m.draft.SetWidth(max(8, m.conversationWidth()))

	if m.active == discussionsID && time.Since(m.discussionsUpdatedAt) < 15*time.Second {
		return nil
	}

	load := m.loadHistory(false, false, true, true)
	if !isFeed(m.active) && m.hasDraft() {
		return tea.Batch(load, m.compose())
	}

	return load
}

func (m *app) openThread(p post, reply bool) tea.Cmd {
	if p.ID == "" {
		return nil
	}

	if m.root != p.ID {
		m.root, m.threadChannelID = p.ID, p.ChannelID

		m.discussion = conversation{readThrough: m.postThreadSignal(m.conversationChannel(true), p.ID).lastViewed}
		if p.CreatedAt != 0 {
			m.discussion.posts = []post{p}
		}
	}

	m.focus, m.fullPane = threadPane, false
	m.status = ""

	load := m.loadHistory(true, false, true, true)
	if m.hasDraft() || reply {
		return tea.Batch(load, m.compose())
	}

	return load
}

func (m *app) compose() tea.Cmd {
	m.resetMention()
	m.status = ""
	m.draft.SetWidth(max(8, m.conversationWidth()))
	m.composing = true
	focus := m.draft.Focus()
	m.restoreDraftFiles(m.draftKey())
	m.setDraft(m.restoreDraft(m.draftKey()))

	return focus
}

func (m *app) key(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case "ctrl+c":
		return tea.Quit
	}

	if m.updateDialog != nil {
		return m.updateKey(key)
	}

	if m.attachmentEditing {
		return nil
	}

	if m.messageSearch != nil {
		return m.messageSearchKey(key)
	}

	if m.reactionAuthors != nil {
		if m.reactionAuthors.Key(commandKeyName(key)) {
			m.reactionAuthors = nil
		}

		return nil
	}

	if m.deleteTarget.ID != "" {
		return m.deleteKey(key)
	}

	if m.reactionTarget.ID != "" {
		return m.reactionKey(key)
	}

	if m.settingsOpen {
		m.settingsKey(key)
		return nil
	}

	if m.textAttachmentPrompt != nil {
		return m.textAttachmentNameKey(key)
	}

	if m.attachmentPicker != nil {
		return m.attachmentPathKey(key)
	}

	if m.resourceMode != "" || len(m.resources) > 0 {
		return m.resourceKey(key)
	}

	if m.reader.ID != "" {
		pageSize := m.readerHeight()
		last := max(0, len(m.readLines(m.reader, m.width-4))-pageSize)
		m.readerOffset = min(m.readerOffset, last)

		name := commandKeyName(key)
		switch name {
		case "esc", "h", "q":
			m.reader = post{}
		case "o":
			return m.openPostResource(m.reader)
		case "O":
			m.showResources(m.reader)
		case "D":
			m.askDelete(m.reader)
		case "z":
			m.pickReaction(m.reader)
		case "Z":
			return m.showReactionAuthors(m.reader)

		case "w", "W":
			return m.followSelectedThread(name == "w")
		case "j", "down", "k", "up", "pgup", "pgdown", "ctrl+u", "ctrl+d", " ":
			m.readerOffset = max(0, min(last, m.readerOffset+pageDistance(name, pageSize)))
		case "g", "home":
			m.readerOffset = 0
		case "G", "end":
			m.readerOffset = last
		}

		return nil
	}

	if !m.composing && m.focus != channelsPane && (!isFeed(m.active) || m.focus == threadPane) && key.Paste && tui.ClipboardPNGPaste(string(key.Runes)) {
		return tea.Batch(m.compose(), m.pasteClipboard(true))
	}

	if m.composing {
		old := m.draft.Value()

		cmd := m.composeKey(key)
		if m.composing && old != m.draft.Value() {
			m.fitComposer()
		}

		m.saveDraft(m.draft.Value())

		return cmd
	}

	if m.filtering {
		return m.chatSearchKey(key)
	}

	name := commandKeyName(key)
	switch name {
	case "?", "ctrl+f":
		m.beginMessageSearch(name == "ctrl+f")
		return nil
	case "q":
		return tea.Quit
	case "N":
		if ch := m.notificationChannel(); !isFeed(ch.ID) {
			m.toggleChannelNotifications(ch.ID)
		}

		return nil
	case "S":
		m.settingsOpen = true
		m.status = ""

		return nil
	case "/", "ctrl+k":
		m.filtering = true
		m.chatSearchIndex = 0

		return m.query.Focus()
	case "ctrl+n", "ctrl+p":
		delta := 1
		if name == "ctrl+p" {
			delta = -1
		}

		if m.focus != channelsPane && m.active != "" {
			m.channelCursor = m.active
			m.ensureChannelCursor()
		}

		m.moveChannel(delta)

		return m.openChannel()
	case "R":
		m.timeline.anchored = false

		m.status = ""
		if m.active == discussionsID && !m.discussionsLoading {
			return tea.Batch(m.loadChannels(), m.loadDiscussions(false), m.loadHistory(true, false, false, true))
		}

		return tea.Batch(m.loadChannels(), m.loadHistory(false, false, false, true), m.loadHistory(true, false, false, true))
	case "tab":
		m.fullPane = false
		if m.focus == channelsPane && m.active != "" {
			m.focus = channelPane
		} else if m.focus == channelPane && m.root != "" {
			m.focus = threadPane
		} else {
			m.focus = channelsPane
			m.channelCursor = m.active
			m.ensureChannelCursor()
		}

		return nil
	}

	if m.focus == channelsPane {
		switch name {
		case "j", "down":
			m.moveChannel(1)
		case "k", "up":
			m.moveChannel(-1)
		case "pgup", "pgdown", "ctrl+u", "ctrl+d":
			m.moveChannel(pageDistance(name, max(1, m.height-5)))
		case "n":
			return m.openUnreadThread()
		case "enter", "l", "right":
			return m.openChannel()
		case "c":
			m.preferences.HideReadChannels = !m.preferences.HideReadChannels
			m.savePreferences()
		case "d":
			m.preferences.HideReadDirect = !m.preferences.HideReadDirect
			m.savePreferences()
		case "esc":
			if m.query.Value() != "" {
				m.query.Reset()
				m.ensureChannelCursor()
			} else if m.active != "" {
				m.focus = channelPane
			} else {
				return tea.Quit
			}
		}

		return nil
	}

	if isFeed(m.active) && m.focus == channelPane {
		p := m.timeline.selectedPost()

		switch name {
		case "f":
			return m.changeFeedFilter()
		case "enter", "l", "right", "t", "r":
			if m.active == activityID {
				return m.openActivityPost(p)
			}

			return m.openThread(p, name == "r")
		case "n":
			if m.active == discussionsID {
				return m.openUnreadThread()
			}

			return nil
		case "i", "a", "alt+t", "ctrl+o", "ctrl+v", "ctrl+x", "m":
			return nil
		}
	}

	view := &m.timeline
	if m.focus == threadPane {
		view = &m.discussion
	}

	switch name {
	case "F":
		m.fullPane = !m.fullPane
		m.draft.SetWidth(max(8, m.conversationWidth()))
	case "h", "left", "esc":
		if m.fullPane && name == "esc" {
			m.fullPane = false
			m.draft.SetWidth(max(8, m.conversationWidth()))

			return nil
		}

		m.fullPane = false
		if m.focus == threadPane {
			m.focus, m.root = channelPane, ""

			m.discussion = conversation{}
			if m.active == discussionsID {
				m.applyDiscussionFilter()
			}
		} else {
			m.focus = channelsPane
		}
	case "J":
		return m.moveHistory(view, -1)
	case "K":
		return m.moveHistory(view, 1)
	case "j", "down", "k", "up", "pgup", "pgdown", "ctrl+u", "ctrl+d":
		return m.scrollHistory(view, name)
	case "G", "end":
		if view.anchored {
			*view = conversation{}
			return m.loadHistory(false, false, false, true)
		}

		view.selected, view.bodyOffset, view.tableDetails = 0, 0, false
		view.pendingOlder, view.pendingPage = 0, false
	case "x":
		if view.selectedPost().ID != "" {
			view.tableDetails, view.bodyOffset = !view.tableDetails, 0
		}
	case "v":
		m.reader, m.readerOffset = view.selectedPost(), 0
	case "o":
		return m.openPostResource(view.selectedPost())
	case "O":
		m.showResources(view.selectedPost())
	case "D":
		m.askDelete(view.selectedPost())
	case "z":
		m.pickReaction(view.selectedPost())
	case "Z":
		return m.showReactionAuthors(view.selectedPost())
	case "w", "W":
		return m.followSelectedThread(name == "w")
	case "V":
		m.showHistoryAttachments(view.posts)
	case "a":
		return m.pickAttachment()
	case "alt+t":
		return m.nameTextAttachment()
	case "ctrl+o":
		m.showDraftAttachments()
	case "n":
		return m.openUnreadThread()
	case "t", "enter", "l", "right":
		if m.focus == channelPane {
			return m.openThread(view.selectedPost(), false)
		}
	case "r":
		if m.focus == channelPane {
			return m.openThread(view.selectedPost(), true)
		}

		return m.compose()
	case "i":
		return m.compose()
	case "ctrl+v":
		return tea.Batch(m.compose(), m.pasteClipboard(false))
	case "ctrl+x":
		return m.editInVim()
	case "m":
		if m.focus == threadPane {
			view.readThrough = view.newest()
			return m.loadHistory(true, false, true, true)
		}

		id, through := m.active, view.newest()

		return func() tea.Msg { return readResult{channelID: id, through: through, err: m.api.markRead(m.ctx, id)} }
	}

	return nil
}

func (m *app) composeKey(key tea.KeyMsg) tea.Cmd {
	if m.sending {
		return nil
	}

	if key.Paste && tui.ClipboardPNGPaste(string(key.Runes)) {
		return m.pasteClipboard(true)
	}

	switch commandKeyName(key) {
	case "ctrl+v":
		return m.pasteClipboard(false)
	case "ctrl+x":
		return m.editInVim()
	case "alt+a":
		return m.pickAttachment()
	case "alt+t":
		return m.nameTextAttachment()
	case "ctrl+o":
		m.showDraftAttachments()
		return nil
	}

	if len(m.mention.users) > 0 {
		switch key.String() {
		case "tab", "enter":
			m.acceptMention()
			return nil
		case "up":
			m.mention.selected = max(0, m.mention.selected-1)
			return nil
		case "down":
			m.mention.selected = min(len(m.mention.users)-1, m.mention.selected+1)
			return nil
		}
	}

	switch key.String() {
	case "esc":
		if m.mention.active {
			m.resetMention()
			m.status = ""

			return nil
		}

		m.composing = false
		m.status = ""
		m.draft.Blur()
	case "enter":
		if m.clipboardDraft == m.draftKey() {
			return nil
		}

		text := m.draft.Value()

		files := slices.Clone(m.draftFiles[m.draftKey()])
		if strings.TrimSpace(text) == "" && len(files) == 0 {
			return nil
		}

		if draftLength(text) > messageLimit {
			m.status = "Shorten the message before sending"
			return nil
		}

		m.sending = true
		m.resetMention()
		m.status = ""
		key := m.draftKey()

		return func() tea.Msg {
			return sendResult{files: files, err: m.api.sendDraft(m.ctx, key.channel, key.root, text, files)}
		}
	case "ctrl+j":
		m.draft.InsertString("\n")
		return m.refreshMention()
	default:
		var cmd tea.Cmd

		m.draft, cmd = m.draft.Update(key)

		return tea.Batch(cmd, m.refreshMention())
	}

	return nil
}

func (m *app) activeChannel() channel {
	return m.conversationChannel(m.focus == threadPane)
}

func (m *app) conversationChannel(thread bool) channel {
	return m.channelByID(m.conversationChannelID(thread))
}

func (m *app) conversationChannelID(thread bool) string {
	if thread && isFeed(m.active) {
		return m.threadChannelID
	}

	return m.active
}

func (m *app) notificationChannel() channel {
	if m.focus == channelsPane {
		return m.channelByID(m.channelCursor)
	}

	return m.activeChannel()
}

func (m *app) channelByID(id string) channel {
	for _, ch := range feedChannels {
		if ch.ID == id {
			return ch
		}
	}

	for _, ch := range m.channels {
		if ch.ID == id {
			return ch
		}
	}

	return channel{}
}

func (ch channel) threadSignal(id string) threadSignal {
	for _, signal := range ch.Threads {
		if signal.root == id {
			return signal
		}
	}

	return threadSignal{}
}

func (m *app) sidebarTitle() string {
	if query := m.query.Value(); query != "" && !m.filtering {
		return fmt.Sprintf("Chats / %s", query)
	}

	return "Chats"
}
