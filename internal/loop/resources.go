package loop

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/danilrwx/telega/tui"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
)

type fileInfo struct {
	ID, Name string
	MIME     string `json:"mime_type"`
}

type resource struct {
	label, link, fileID, localPath string
}

type resourceResult struct{ err error }

var linkParser = goldmark.New(goldmark.WithExtensions(extension.Linkify, extension.Table)).Parser()

func postAttachments(p post) []resource {
	items := make([]resource, 0, len(p.Files))
	for i, id := range p.Files {
		label := fmt.Sprintf("Attachment %d", i+1)

		if index := slices.IndexFunc(p.Metadata.Files, func(info fileInfo) bool { return info.ID == id }); index >= 0 {
			if name := p.Metadata.Files[index].Name; name != "" {
				label = name
			}
		}

		items = append(items, resource{label: label, fileID: id})
	}

	return items
}

func messageResources(p post, server string) []resource {
	items := postAttachments(p)
	source := []byte(p.Text)
	base, _ := url.Parse(server)
	seen := make(map[resource]bool)
	_ = ast.Walk(linkParser.Parse(text.NewReader(source)), func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}

		var (
			raw   []byte
			label string
		)

		switch node := node.(type) {
		case *ast.Link:
			raw, label = node.Destination, linkLabel(node, source)
		case *ast.Image:
			raw, label = node.Destination, linkLabel(node, source)
		case *ast.AutoLink:
			raw = node.URL(source)
		default:
			return ast.WalkContinue, nil
		}

		target, _ := url.Parse(string(raw))
		if target == nil {
			return ast.WalkContinue, nil
		}

		if base != nil && strings.HasPrefix(target.Path, "/") && !target.IsAbs() && target.Host == "" {
			target = base.ResolveReference(target)
		}

		address := target.String()

		label = strings.TrimSpace(label)
		if label == "" {
			label = address
		}

		item := resource{label: label, link: address}
		if (target.Scheme == "http" || target.Scheme == "https") && target.Hostname() != "" &&
			target.User == nil && !seen[item] {
			seen[item] = true
			items = append(items, item)
		}

		return ast.WalkContinue, nil
	})

	return items
}

func linkLabel(link ast.Node, source []byte) string {
	var label strings.Builder

	_ = ast.Walk(link, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}

		switch node := node.(type) {
		case *ast.Text:
			label.Write(node.Value(source))

			if node.SoftLineBreak() {
				label.WriteByte('\n')
			}
		case *ast.String:
			label.Write(node.Value)
		}

		return ast.WalkContinue, nil
	})

	return label.String()
}

func (c *client) download(ctx context.Context, id string) (string, error) {
	endpoint := "/files/" + url.PathEscape(id)

	var info fileInfo
	if err := c.request(ctx, http.MethodGet, endpoint+"/info", nil, &info); err != nil {
		return "", err
	}

	name := tui.AttachmentName(info.Name)
	switch strings.ToLower(filepath.Ext(name)) {
	case ".desktop", ".lnk", ".url", ".webloc", ".appimage", ".exe", ".com", ".bat", ".cmd", ".command", ".sh":
		return "", errors.New("open this executable attachment from Loop in the browser")
	}

	switch info.MIME {
	case "application/x-desktop", "application/x-executable", "application/x-msdownload", "application/x-msdos-program":
		return "", errors.New("open this executable attachment from Loop in the browser")
	}

	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}

	sum := sha256.Sum256([]byte(c.baseURL + "\x00" + c.me.ID + "\x00" + id))

	dir := filepath.Join(cache, "loop-tui", "attachments", fmt.Sprintf("%x", sum))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}

	target := filepath.Join(dir, name)
	if stat, err := os.Lstat(target); err == nil && stat.Mode().IsRegular() {
		return target, nil
	}

	client := *c.http
	client.Timeout = 3 * time.Minute

	resp, err := c.response(ctx, &client, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	file, err := os.CreateTemp(dir, ".download-*")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(file.Name()) }()

	_, copyErr := io.Copy(file, resp.Body)

	closeErr := file.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return "", err
	}

	if err := os.Rename(file.Name(), target); err != nil {
		return "", err
	}

	return target, nil
}

func (m *app) showResources(p post) {
	m.resources = messageResources(p, m.api.baseURL)
	m.resourceIndex = 0

	m.status = ""
	if len(m.resources) == 0 {
		m.status = "No links or attachments in this message"
	}
}

func (m *app) openPostResource(p post) tea.Cmd {
	if m.resourceOpening {
		return nil
	}

	if tui.ImageGraphics() {
		id := inlineAttachmentID(p)
		if id != "" && (len(p.Metadata.Files) != 0 || m.inlineImages.Get("file:"+id) != nil) {
			for _, item := range postAttachments(p) {
				if item.fileID == id {
					m.resourceOpening = true
					m.status = "Opening image…"

					return m.openResource(item, true)
				}
			}
		}
	}

	m.showResources(p)

	return nil
}

func (m *app) openResource(item resource, previewOnly bool) tea.Cmd {
	return func() tea.Msg {
		target := item.link
		if item.localPath != "" {
			if _, err := tui.AttachmentStat(item.localPath); err != nil {
				return resourceResult{err}
			}

			target = item.localPath
		}

		if item.fileID != "" {
			var err error

			target, err = m.api.download(m.ctx, item.fileID)
			if err != nil {
				return resourceResult{err}
			}
		}

		if (item.fileID != "" || item.localPath != "") && tui.ImageGraphics() {
			img, err := tui.LoadTerminalImage(target)
			if err == nil {
				return imagePreviewResult{image: img, title: item.label, path: target}
			}

			if previewOnly || tui.InlineImageFile(target, "") {
				return resourceResult{err}
			}
		}

		return resourceResult{err: launchResource(target)}
	}
}

func launchResource(target string) error {
	opener := "xdg-open"
	if runtime.GOOS == "darwin" {
		opener = "open"
	}

	cmd := exec.Command(opener, target)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%s: %w", opener, err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("%s: %w", opener, err)
		}
	case <-time.After(150 * time.Millisecond):
	}

	return nil
}
