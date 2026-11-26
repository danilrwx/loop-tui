package loop

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/danilrwx/telega/tui"
)

type sidebarSettings struct {
	tui.AvatarSettings
	HideReadChannels     bool                       `json:"hide_read_channels"`
	HideReadDirect       bool                       `json:"hide_read_direct_messages"`
	MentionReminder      reminderMode               `json:"mention_reminder,omitempty"`
	WatchedChannels      map[string]map[string]bool `json:"watched_channels,omitempty"`
	HideChatListWhenOpen bool                       `json:"hide_chat_list_when_open,omitempty"`
}

func chatMode(unread bool) string {
	if unread {
		return "Unread"
	}

	return "All"
}

func (m *app) loadPreferences() {
	m.configEnabled = true

	saved := struct {
		sidebarSettings
		CollapseChannels bool `json:"collapse_channels"`
		CollapseDirect   bool `json:"collapse_direct_messages"`
	}{sidebarSettings: sidebarSettings{HideReadDirect: true}}
	if err := tui.ReadSettings("loop-tui", &saved); err != nil {
		m.status = "Settings: " + err.Error()
	}

	m.preferences = saved.sidebarSettings
	m.preferences.HideReadChannels = saved.HideReadChannels || saved.CollapseChannels

	m.preferences.HideReadDirect = saved.HideReadDirect || saved.CollapseDirect
	switch m.preferences.MentionReminder {
	case untilEyes, untilReaction:
	default:
		m.preferences.MentionReminder = untilRead
	}

	if err := tui.ReadSettings(tui.LayoutSettingsName, &m.layoutSettings); err != nil {
		m.status = "Layout settings: " + err.Error()
	}
}

func (m *app) savePreferences() {
	m.ensureChannelCursor()

	if m.configEnabled {
		if err := tui.WriteSettings("loop-tui", m.preferences); err != nil {
			m.status = "Settings: " + err.Error()
		}
	}
}

func (m *app) resizeSidebar(n int) {
	m.layoutSettings.SidebarWidth = n
	if m.configEnabled {
		if err := tui.WriteSettings(tui.LayoutSettingsName, m.layoutSettings); err != nil {
			m.status = "Layout settings: " + err.Error()
		}
	}
}

const settingCount = 7

func (m *app) settingsKey(key tea.KeyMsg) {
	name := commandKeyName(key)
	switch name {
	case "esc", "q", "S":
		m.settingsOpen = false
	case "up", "k":
		m.settingsCursor = max(0, m.settingsCursor-1)
	case "down", "j":
		m.settingsCursor = min(settingCount-1, m.settingsCursor+1)
	case "0":
		if m.settingsCursor == 0 {
			m.resizeSidebar(0)
		}
	case "left", "h", "right", "l":
		if m.settingsCursor != 0 {
			return
		}

		width := m.layoutSettings.SidebarWidth
		if width <= 0 {
			width = tui.SidebarWidth(m.width, 0)
			if width == 0 {
				width = 30
			}
		}

		if name == "left" || name == "h" {
			width--
		} else {
			width++
		}

		m.resizeSidebar(max(20, min(60, width)))
	case " ", "enter":
		switch m.settingsCursor {
		case 0:
			return
		case 1:
			m.preferences.HideReadChannels = !m.preferences.HideReadChannels
		case 2:
			m.preferences.HideReadDirect = !m.preferences.HideReadDirect
		case 3:
			switch m.preferences.MentionReminder {
			case untilRead:
				m.preferences.MentionReminder = untilEyes
			case untilEyes:
				m.preferences.MentionReminder = untilReaction
			default:
				m.preferences.MentionReminder = untilRead
			}
		case 4:
			m.toggleChannelNotifications(m.notificationChannel().ID)
			return
		case 5:
			m.preferences.HideAvatars = !m.preferences.HideAvatars
		case 6:
			m.preferences.HideChatListWhenOpen = !m.preferences.HideChatListWhenOpen
		}

		m.savePreferences()
	}
}

func (m *app) renderSettings(width, height int, row func(string, bool, bool, bool)) {
	sidebar := "Auto"
	if n := m.layoutSettings.SidebarWidth; n > 0 {
		sidebar = fmt.Sprintf("%d columns", n)
	}

	mention := "Until read"

	switch m.preferences.MentionReminder {
	case untilEyes:
		mention = "Until 👀"
	case untilReaction:
		mention = "Until any reaction"
	}

	ch := m.notificationChannel()
	alerts := "Off"
	label := "All-message alerts"

	switch {
	case ch.ID == "":
		alerts = "Select a chat"
	case m.watchesChannel(ch.ID):
		alerts = "On"
	}

	if ch.ID != "" {
		label = ch.DisplayName + " · Alerts"
	}

	hideChatList := "Off"
	if m.preferences.HideChatListWhenOpen {
		hideChatList = "On"
	}

	options := [...]struct{ label, value string }{
		{"Sidebar width", sidebar},
		{"Channels", chatMode(m.preferences.HideReadChannels)},
		{"Direct messages", chatMode(m.preferences.HideReadDirect)},
		{"Mention reminders", mention},
		{label, alerts},
		{"Avatars", m.preferences.AvatarMode()},
		{"Hide chat list when open", hideChatList},
	}

	choices := make([]string, 0, settingCount)
	for _, option := range options {
		choices = append(choices, tui.Clip(option.label, width-len(option.value)-4)+"  "+option.value)
	}

	hint := "jk move  Enter change mode  Esc back"
	if m.settingsCursor == 0 {
		hint = "jk move  hl width  0 auto  Esc back"
	}

	status := m.status
	if status == "" {
		status = "Desktop alerts are delivered by loopd"
	}

	tui.ListDialog("Settings", choices, m.settingsCursor, width, height, status, hint, row)
}
