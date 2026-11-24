package loop

import (
	"net/url"
	"regexp"
	"strings"
	"unicode"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/danilrwx/telega/tui"
	"github.com/rivo/uniseg"
)

var displayLink = regexp.MustCompile(`!?\[([^\]\r\n]+)\]\((?:<([^>\r\n]+)>|([^\s()<>\r\n]+(?:\([^()\s]*\)[^\s()<>\r\n]*)*))(?:[ \t]+"[^"\r\n]*")?\)|<(https?://[^\s<>]+)>|(https?://[^\s<>]+)`)

type displaySpan struct {
	text, target string
	image        *tui.InlineImage
	bold, italic bool
	ownMention   bool
}

type messageFormatting struct {
	allowed  map[string]bool
	images   *tui.InlineImages
	username string
	server   string
}

func (m *app) messageFormatting() messageFormatting {
	format := messageFormatting{images: m.inlineImages, username: m.username()}
	if m.api != nil {
		format.server = m.api.baseURL
	}

	return format
}

func postLines(p post, width int, format messageFormatting, tableDetails bool) []string {
	body := strings.ReplaceAll(strings.TrimRightFunc(p.Text, unicode.IsSpace), "\t", "    ")
	if strings.Contains(body, "://") || strings.Contains(body, "](") {
		format.allowed = make(map[string]bool)
		for _, item := range messageResources(p, format.server) {
			if item.link != "" {
				format.allowed[item.link] = true
			}
		}
	}

	var lines []string

	for _, block := range tui.SplitCodeFences(body) {
		if block.Code {
			for _, line := range tui.CodeLines(block, width) {
				lines = append(lines, dateColor.Render(line))
			}
		} else {
			if block.Text != "" || len(p.Files) == 0 {
				lines = append(lines, markdownProseLines(block.Text, width, format, tableDetails)...)
			}
		}
	}

	if attachment := postAttachmentLabel(p); attachment != "" {
		lines = append(lines, tui.Wrap(attachment, width, 0)...)
	}

	return lines
}

func (f messageFormatting) proseLines(body string, width int) []string {
	if len(f.allowed) == 0 && !strings.ContainsAny(body, ":*_@") {
		return tui.Wrap(body, width, 0)
	}

	return wrapDisplaySpans(f.displaySpans(body), width, false)
}

func (f messageFormatting) displaySpans(body string) []displaySpan {
	return expandMessageTokens(emphasizeDisplaySpans(displaySpans(body, f.allowed, f.server)), f.images, f.username)
}

func displaySpans(body string, allowed map[string]bool, server string) []displaySpan {
	if len(allowed) == 0 {
		return []displaySpan{{text: body}}
	}

	var spans []displaySpan

	base, _ := url.Parse(server)
	at := 0

	for _, indices := range displayLink.FindAllStringSubmatchIndex(body, -1) {
		part := func(index int) string {
			start, end := indices[index*2], indices[index*2+1]
			if start < 0 {
				return ""
			}

			return body[start:end]
		}

		label, target := part(1), part(2)
		if target == "" {
			target = part(3)
		}

		if target == "" {
			target = part(4)
		}

		end := indices[1]

		if raw := part(5); raw != "" {
			target = trimURLPunctuation(raw)
			end -= len(raw) - len(target)
		}

		address, err := url.Parse(target)
		if err != nil {
			continue
		}

		webURL := (address.Scheme == "http" || address.Scheme == "https") && address.Hostname() != ""

		local := address.Scheme == "" && address.Host == "" && strings.HasPrefix(address.Path, "/")
		if address.User != nil || !webURL && !local {
			continue
		}

		resolved := address.String()
		if local && base != nil {
			resolved = base.ResolveReference(address).String()
		}

		if !allowed[resolved] {
			continue
		}

		if label == "" || label == target {
			label = shortLink(address)
		}

		spans = append(spans, displaySpan{text: body[at:indices[0]]}, displaySpan{text: "↗ " + label, target: resolved})
		at = end
	}

	return append(spans, displaySpan{text: body[at:]})
}

func trimURLPunctuation(raw string) string {
	raw = strings.TrimRight(raw, ".,!?;:")
	for _, pair := range [][2]string{{"(", ")"}, {"[", "]"}, {"{", "}"}} {
		for strings.HasSuffix(raw, pair[1]) && strings.Count(raw, pair[1]) > strings.Count(raw, pair[0]) {
			raw = strings.TrimSuffix(raw, pair[1])
		}
	}

	return raw
}

func shortLink(address *url.URL) string {
	label := address.Host + address.EscapedPath()
	if uniseg.StringWidth(label) <= 48 {
		return label
	}

	parts := strings.Split(strings.Trim(address.EscapedPath(), "/"), "/")
	if len(parts) >= 2 && address.Host != "" {
		return address.Host + "/…/" + strings.Join(parts[len(parts)-2:], "/")
	}

	return label
}

func wrapDisplaySpans(spans []displaySpan, width int, clickable bool) []string {
	return renderDisplaySpans(spans, width, clickable, false)
}

func renderDisplaySpans(spans []displaySpan, width int, clickable, words bool) []string {
	width = max(1, width)

	var (
		lines    []string
		row, run strings.Builder
	)

	cells := 0
	previousSpace := false

	for _, span := range spans {
		flush := func() {
			if run.Len() != 0 {
				value := run.String()

				style := lipgloss.NewStyle()
				if span.target != "" {
					style = dateColor.Underline(true)
				} else if span.ownMention {
					style = unreadColor
				}

				value = style.Bold(span.bold || span.ownMention).Italic(span.italic).Render(value)
				if clickable && span.target != "" {
					value = ansi.SetHyperlink(span.target) + value + ansi.ResetHyperlink()
				}

				row.WriteString(value)
				run.Reset()
			}
		}

		if image := span.image; image != nil {
			if cells+image.Width > width {
				lines = append(lines, row.String())
				row.Reset()

				cells = 0
			}

			row.WriteString(image.Rows[0])
			cells += image.Width
			previousSpace = false

			continue
		}

		chars := uniseg.NewGraphemes(span.text)
		for chars.Next() {
			glyph, size := chars.Str(), chars.Width()

			newline := glyph == "\n" || glyph == "\r\n"
			if !newline && strings.ContainsFunc(glyph, unicode.IsControl) {
				glyph, size = " ", 1
			}

			if size > width {
				glyph, size = "…", 1
			}

			wordBreak := false

			if words && !newline {
				space := glyph == " "
				if space && (previousSpace || cells == 0) {
					continue
				}

				if !space && previousSpace && cells > 0 {
					start, _ := chars.Positions()

					word := span.text[start:]
					if end := strings.IndexFunc(word, unicode.IsSpace); end >= 0 {
						word = word[:end]
					}

					wordBreak = cells+uniseg.StringWidth(word) > width
				}

				previousSpace = space
			}

			if newline || wordBreak || cells+size > width {
				flush()

				lines = append(lines, row.String())
				row.Reset()

				cells = 0
			}

			if newline {
				previousSpace = false
			}

			if !newline && (!words || glyph != " " || cells > 0) {
				run.WriteString(glyph)

				cells += size
			}
		}

		flush()
	}

	return append(lines, row.String())
}
