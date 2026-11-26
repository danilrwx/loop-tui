package loop

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/danilrwx/telega/tui"
)

type inlineImageResult struct {
	key   string
	image *tui.InlineImage
}

func inlineAttachmentID(p post) string {
	if len(p.Files) == 0 {
		return ""
	}

	for _, file := range p.Metadata.Files {
		if file.ID != "" && tui.InlineImageFile(file.Name, file.MIME) {
			return file.ID
		}
	}

	if len(p.Metadata.Files) == 0 {
		return p.Files[0]
	}

	return ""
}

func (c *client) emojiImage(ctx context.Context, name string) (*http.Response, error) {
	var emoji struct{ ID string }
	if err := c.request(ctx, http.MethodGet, "/emoji/name/"+url.PathEscape(name), nil, &emoji); err != nil {
		return nil, err
	}

	if emoji.ID == "" {
		return nil, errors.New("emoji has no image")
	}

	return c.response(ctx, c.http, http.MethodGet, "/emoji/"+url.PathEscape(emoji.ID)+"/image", nil)
}

func (c *client) mediaKey(kind, id string) string {
	return c.baseURL + "\x00" + c.me.ID + "\x00" + kind + "\x00" + id
}

func cachedLoopEmoji[T any](ctx context.Context, c *client, name string, decode func(io.Reader) (T, error)) (T, error) {
	return tui.CachedImage("loop-tui", c.mediaKey("emoji", name), 24*time.Hour, decode, func() ([]byte, error) {
		response, err := c.emojiImage(ctx, name)
		if err != nil {
			return nil, err
		}
		defer func() { _ = response.Body.Close() }()

		return tui.ReadImageData(response.Body)
	})
}

func (c *client) loadInlineImage(ctx context.Context, key string) *tui.InlineImage {
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()

	kind, name, _ := strings.Cut(key, ":")
	if kind == "emoji" {
		image, _ := cachedLoopEmoji(ctx, c, name, func(r io.Reader) (*tui.InlineImage, error) {
			return tui.DecodeInlineImage(r, 1, 1)
		})

		return image
	}

	if kind == "avatar" {
		image, _ := tui.CachedImage("loop-tui", c.mediaKey(kind, name), 24*time.Hour,
			tui.DecodeInlineAvatar,
			func() ([]byte, error) {
				response, err := c.response(ctx, c.http, http.MethodGet, "/users/"+url.PathEscape(name)+"/image", nil)
				if err != nil {
					return nil, err
				}
				defer func() { _ = response.Body.Close() }()

				return tui.ReadImageData(response.Body)
			})

		return image
	}

	path := "/files/" + url.PathEscape(name)
	verify := kind == "unknown-file"

	for _, suffix := range []string{"/thumbnail", ""} {
		image, err := tui.CachedImage("loop-tui", c.mediaKey("thumbnail", name), 0,
			func(r io.Reader) (*tui.InlineImage, error) { return tui.DecodeInlineImage(r, 12, 4) },
			func() ([]byte, error) {
				if verify {
					var file fileInfo
					if err := c.request(ctx, http.MethodGet, path+"/info", nil, &file); err != nil {
						return nil, err
					}

					if !tui.InlineImageFile(file.Name, file.MIME) {
						return nil, errUnsupportedInlineImage
					}

					verify = false
				}

				response, err := c.response(ctx, c.http, http.MethodGet, path+suffix, nil)
				if err != nil {
					return nil, err
				}
				defer func() { _ = response.Body.Close() }()

				return tui.ReadImageData(response.Body)
			})
		if err == nil || errors.Is(err, errUnsupportedInlineImage) {
			return image
		}
	}

	return nil
}

var errUnsupportedInlineImage = errors.New("attachment is not a supported inline image")

func (m *app) loadVisibleImages() tea.Cmd {
	if m.inlineImages == nil || m.inlineLoading || m.width < 24 || m.height < 9 || m.active == "" ||
		m.updateDialog != nil || m.filtering || m.messageSearch != nil || m.settingsOpen || m.deleteTarget.ID != "" || m.reactionAuthors != nil || m.attachmentPicker != nil || m.textAttachmentPrompt != nil || m.resourceMode != "" || len(m.resources) != 0 {
		return nil
	}

	if m.reader.ID != "" {
		if key := m.requestMessageEmoji(m.reader); key != "" {
			return m.requestInlineImage(key, key)
		}

		return m.requestAvatar(m.reader)
	}

	layout := m.layout()

	views := []*conversation{}
	if layout.thread > 0 && m.focus == threadPane {
		views = append(views, &m.discussion)
	}

	if layout.channel > 0 {
		views = append(views, &m.timeline)
	}

	if layout.thread > 0 && m.focus != threadPane {
		views = append(views, &m.discussion)
	}

	room := max(1, min(12, (m.height-2)/3))
	for _, view := range views {
		for _, p := range view.posts[view.selected:min(len(view.posts), view.selected+room)] {
			if id := inlineAttachmentID(p); id != "" && m.inlineImages.Start("file:"+id) {
				loadKey := "file:" + id
				if len(p.Metadata.Files) == 0 {
					loadKey = "unknown-file:" + id
				}

				return m.requestInlineImage("file:"+id, loadKey)
			}

			if key := m.requestMessageEmoji(p); key != "" {
				return m.requestInlineImage(key, key)
			}

			for _, react := range p.Metadata.Reactions {
				name := react.EmojiName
				if name != "" && reactionGlyph(name) == ":"+name+":" && m.inlineImages.Start("emoji:"+name) {
					return m.requestInlineImage("emoji:"+name, "emoji:"+name)
				}
			}
		}
	}

	for _, view := range views {
		for _, p := range view.posts[view.selected:min(len(view.posts), view.selected+room)] {
			if cmd := m.requestAvatar(p); cmd != nil {
				return cmd
			}
		}
	}

	return nil
}

func (m *app) messageAvatar(p post) *tui.InlineImage {
	if m.preferences.HideAvatars {
		return nil
	}

	return m.inlineImages.Get("avatar:" + p.UserID)
}

func (m *app) requestAvatar(p post) tea.Cmd {
	if !m.preferences.HideAvatars && p.UserID != "" && p.Author != "" {
		key := "avatar:" + p.UserID
		if m.inlineImages.Start(key) {
			return m.requestInlineImage(key, key)
		}
	}

	return nil
}

func (m *app) requestInlineImage(key, loadKey string) tea.Cmd {
	m.inlineLoading = true

	return func() tea.Msg {
		return inlineImageResult{key: key, image: m.api.loadInlineImage(m.ctx, loadKey)}
	}
}

func (m *app) inlineReactionLine(items []reaction, width int) string {
	var badges []tui.ReactionBadge

	index := make(map[string]int)
	hasImages := false

	for _, react := range items {
		if react.EmojiName == "" {
			continue
		}

		label := reactionGlyph(react.EmojiName)
		if i, ok := index[label]; ok {
			badges[i].Count++
			continue
		}

		image := m.inlineImages.Get("emoji:" + react.EmojiName)
		index[label] = len(badges)
		badges = append(badges, tui.ReactionBadge{Label: label, Count: 1, Image: image})
		hasImages = hasImages || image != nil
	}

	if !hasImages {
		return tui.ReactionLine(reactionSummary(items), width, false)
	}

	return tui.ReactionBadges("↳ ", badges, width)
}

type inlineGraphicsRefreshed struct{}

func (m *app) refreshInlineGraphics(force bool) tea.Cmd {
	if m.inlineImages == nil || m.inlineGraphicsChecking || !force && m.inlineImages.Enabled() {
		return nil
	}

	m.inlineGraphicsChecking = true
	output := m.inlineImages

	return func() tea.Msg { output.Refresh(); return inlineGraphicsRefreshed{} }
}
