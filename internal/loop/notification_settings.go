package loop

import "fmt"

type reminderMode string

const (
	untilRead     reminderMode = ""
	untilEyes     reminderMode = "eyes"
	untilReaction reminderMode = "reaction"
)

func (m *app) watchesChannel(id string) bool {
	return m.api != nil && m.preferences.WatchedChannels[m.api.baseURL][id]
}

func (m *app) toggleChannelNotifications(id string) {
	if id == "" || m.api == nil {
		return
	}

	if m.preferences.WatchedChannels == nil {
		m.preferences.WatchedChannels = make(map[string]map[string]bool)
	}

	server := m.api.baseURL

	watched := m.preferences.WatchedChannels[server]
	if watched == nil {
		watched = make(map[string]bool)
		m.preferences.WatchedChannels[server] = watched
	}

	state := "on"

	if watched[id] {
		delete(watched, id)

		state = "off"
	} else {
		watched[id] = true
	}

	m.status = fmt.Sprintf("All-message alerts %s · %s", state, m.channelByID(id).DisplayName)
	m.savePreferences()
}
