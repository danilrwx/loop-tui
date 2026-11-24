package loop

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/danilrwx/telega/tui"
	"github.com/kyokomi/emoji/v2"
)

var messageInlineToken = regexp.MustCompile(`\\[^\n]|\x60+[^\x60\n]*\x60+|:([a-zA-Z0-9_+-]{1,64}):|@([\p{L}\p{N}\p{M}_.-]+)`)

func messageInlineMatches(text string) [][]int {
	if !strings.ContainsAny(text, ":@") {
		return nil
	}

	return messageInlineToken.FindAllStringSubmatchIndex(text, -1)
}

func expandMessageTokens(spans []displaySpan, images *tui.InlineImages, username string) []displaySpan {
	var result []displaySpan

	for _, span := range spans {
		at := 0

		for _, match := range messageInlineMatches(span.text) {
			start, end := match[0], match[1]

			piece := span
			switch {
			case match[2] >= 0:
				code := span.text[start:end]

				piece.text = emoji.CodeMap()[code]
				if piece.text == "" {
					piece.image = images.Get("emoji:" + span.text[match[2]:match[3]])
					if piece.image == nil {
						continue
					}

					piece.text = code
				}
			case match[4] >= 0 && username != "" && span.target == "":
				name := strings.TrimRight(span.text[match[4]:end], ".")

				previous, _ := utf8.DecodeLastRuneInString(span.text[:start])
				if !strings.EqualFold(name, username) || mentionRune(previous) || previous == '@' {
					continue
				}

				end = match[4] + len(name)
				piece.text, piece.ownMention = span.text[start:end], true
			default:
				continue
			}

			if at < start {
				prefix := span
				prefix.text = span.text[at:start]
				result = append(result, prefix)
			}

			result = append(result, piece)
			at = end
		}

		if at < len(span.text) {
			span.text = span.text[at:]
			result = append(result, span)
		}
	}

	return result
}

func (m *app) requestMessageEmoji(p post) string {
	if !strings.Contains(p.Text, ":") {
		return ""
	}

	for _, block := range tui.SplitCodeFences(p.Text) {
		if block.Code {
			continue
		}

		for _, match := range messageInlineMatches(block.Text) {
			if match[2] >= 0 && emoji.CodeMap()[block.Text[match[0]:match[1]]] == "" {
				key := "emoji:" + block.Text[match[2]:match[3]]
				if m.inlineImages.Start(key) {
					return key
				}
			}
		}
	}

	return ""
}
