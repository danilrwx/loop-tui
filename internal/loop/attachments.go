package loop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/danilrwx/telega/tui"
)

type draftAttachment struct {
	Path     string
	UploadID string `json:",omitempty"`
	Managed  bool   `json:",omitempty"`
}

const draftAttachmentTitle = tui.DraftAttachmentsTitle

func (m *app) addAttachment(file draftAttachment) bool {
	files := m.restoreDraftFiles(m.draftKey())
	if slices.ContainsFunc(files, func(current draftAttachment) bool { return current.Path == file.Path }) {
		return true
	}

	return m.saveDraftFiles(append(files, file))
}

func (m *app) focusAttachmentDraft() tea.Cmd {
	if m.resourceMode == draftAttachmentTitle {
		m.showDraftAttachments()
	}

	if m.composing {
		m.fitComposer()
		return m.draft.Focus()
	}

	status := m.status
	cmd := m.compose()
	m.status = status

	return cmd
}

func (m *app) showDraftAttachments() {
	m.resourceMode = draftAttachmentTitle
	m.resources = nil
	m.resourceIndex = 0

	files := m.restoreDraftFiles(m.draftKey())
	for _, file := range files {
		m.resources = append(m.resources, resource{label: tui.AttachmentName(filepath.Base(file.Path)), localPath: file.Path})
	}
}

func (m *app) showHistoryAttachments(posts []post) {
	m.resources = nil
	m.resourceIndex = 0

	m.status = ""
	for _, p := range posts {
		m.resources = append(m.resources, postAttachments(p)...)
	}

	if len(m.resources) == 0 {
		m.status = "No attachments in loaded messages"
		return
	}

	m.resourceMode = tui.HistoryAttachmentsTitle
}

func (m *app) resourceKey(key tea.KeyMsg) tea.Cmd {
	name := commandKeyName(key)
	if name == "ctrl+o" && m.resourceMode == draftAttachmentTitle ||
		name == "V" && m.resourceMode == tui.HistoryAttachmentsTitle ||
		(name == "o" || name == "O") && m.resourceMode == "" {
		name = "esc"
	}

	action, index := tui.AttachmentListAction(name, m.resourceIndex, len(m.resources))
	m.resourceIndex = index

	switch action {
	case "close":
		m.resources, m.resourceMode = nil, ""
		if m.composing {
			return m.draft.Focus()
		}
	case "attach":
		if m.resourceMode == draftAttachmentTitle {
			return m.pickAttachment()
		}
	case "edit":
		if m.resourceMode == draftAttachmentTitle && len(m.resources) > 0 {
			return m.editAttachment(m.resourceIndex)
		}
	case "remove":
		if m.resourceMode == draftAttachmentTitle && len(m.resources) > 0 {
			index := m.resourceIndex
			files := m.draftFiles[m.draftKey()]

			file := files[index]
			if m.saveDraftFiles(slices.Delete(slices.Clone(files), index, index+1)) {
				discardAttachmentCopy(file)
			}

			m.showDraftAttachments()

			m.resourceIndex = min(index, max(0, len(m.resources)-1))
			if m.composing {
				m.fitComposer()
			}
		}
	case "open":
		if !m.resourceOpening && len(m.resources) > 0 {
			item := m.resources[m.resourceIndex]
			if m.resourceMode == "" {
				m.resources = nil
			}

			m.resourceOpening = true
			m.status = "Opening…"

			return m.openResource(item, false)
		}
	}

	return nil
}

func (c *client) uploadAttachment(ctx context.Context, channelID, path string) (string, error) {
	info, err := tui.AttachmentStat(path)
	if err != nil {
		return "", err
	}

	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()

	args := url.Values{"channel_id": {channelID}, "filename": {filepath.Base(path)}}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v4/files?"+args.Encode(), io.LimitReader(file, info.Size()))
	if err != nil {
		return "", err
	}

	req.ContentLength = info.Size()
	req.Header.Set("Content-Type", "application/octet-stream")

	client := *c.http
	client.Timeout = 3 * time.Minute

	resp, err := c.do(&client, req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	var result struct {
		Files []fileInfo `json:"file_infos"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	if len(result.Files) != 1 || result.Files[0].ID == "" {
		return "", errors.New("server did not return an attachment ID")
	}

	return result.Files[0].ID, nil
}

func (c *client) sendDraft(ctx context.Context, channelID, rootID, text string, files []draftAttachment) error {
	ids := make([]string, 0, len(files))
	for i := range files {
		if files[i].UploadID == "" {
			id, err := c.uploadAttachment(ctx, channelID, files[i].Path)
			if err != nil {
				return fmt.Errorf("upload %s: %w", tui.AttachmentName(filepath.Base(files[i].Path)), err)
			}

			files[i].UploadID = id
		}

		ids = append(ids, files[i].UploadID)
	}

	return c.send(ctx, channelID, rootID, text, ids)
}
