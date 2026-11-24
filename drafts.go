package main

import "strings"

type draftKey struct {
	channel, root string
}

func (m *app) draftKey() draftKey {
	key := draftKey{channel: m.active}
	if m.focus == threadPane {
		key.channel, key.root = m.conversationChannelID(true), m.root
	}
	return key
}

func (m *app) diskDraftKey(key draftKey) string {
	return strings.Join([]string{m.api.baseURL, m.api.me.ID, key.channel, key.root}, "\x00")
}

func (m *app) restoreDraft(key draftKey) string {
	if draft, ok := m.drafts[key]; ok {
		return draft
	}
	var draft string
	if m.draftStore != nil {
		if _, err := m.draftStore.Load(m.diskDraftKey(key), &draft); err != nil {
			m.status = "Load draft: " + err.Error()
		}
	}
	m.drafts[key] = draft
	return draft
}

func (m *app) saveDraft(text string) {
	key := m.draftKey()
	if key.channel == "" || m.drafts[key] == text {
		return
	}
	if m.draftStore != nil {
		var value any
		if text != "" {
			value = text
		}
		if err := m.draftStore.Save(m.diskDraftKey(key), value); err != nil {
			m.status = "Save draft: " + err.Error()
		}
	}
	m.drafts[key] = text
}

func (m *app) hasDraft() bool {
	key := m.draftKey()
	text := m.restoreDraft(key)
	return len(m.restoreDraftFiles(key)) > 0 || text != ""
}

func (m *app) restoreDraftFiles(key draftKey) []draftAttachment {
	if files, ok := m.draftFiles[key]; ok {
		return files
	}
	var files []draftAttachment
	if m.draftStore != nil {
		if _, err := m.draftStore.Load(m.diskDraftKey(key)+"\x00files", &files); err != nil {
			m.status = "Load attachments: " + err.Error()
		}
	}
	m.draftFiles[key] = files
	return files
}

func (m *app) saveDraftFiles(files []draftAttachment) bool {
	return m.saveDraftFilesFor(m.draftKey(), files)
}

func (m *app) saveDraftFilesFor(key draftKey, files []draftAttachment) bool {
	if key.channel == "" {
		return false
	}
	if m.draftStore != nil {
		var value any
		if len(files) > 0 {
			value = files
		}
		if err := m.draftStore.Save(m.diskDraftKey(key)+"\x00files", value); err != nil {
			m.status = "Save attachments: " + err.Error()
			return false
		}
	}
	m.draftFiles[key] = files
	return true
}
