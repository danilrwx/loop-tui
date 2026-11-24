package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

func TestMarkdownHeadingsKeepOnlyTheHeadingText(t *testing.T) {
	text := "Before #tag\n\n## Report ##\n\n### [Results](https://example.org)\n\n" +
		"A setext title\n-----\n\n```text\n## literal\n```"
	result := strings.Join(postLines(post{Text: text}, 80, messageFormatting{}, true), "\n")
	plain := ansi.Strip(result)
	for _, part := range []string{"Before #tag\n\nReport\n\n↗ Results", "A setext title\n\n╭", "│ ## literal"} {
		if !strings.Contains(plain, part) {
			t.Fatalf("missing %q from:\n%s", part, plain)
		}
	}
	if strings.Contains(plain, "## Report") || strings.Contains(plain, "###") || strings.Contains(plain, "-----") ||
		!strings.Contains(result, ansi.SetHyperlink("https://example.org")) {
		t.Fatalf("unexpected heading formatting: %q", plain)
	}
}

func TestMessageEmojiKeepsShortcodesInCode(t *testing.T) {
	body := "### :space_invader: Report\n\n| :warning: Status |\n| --- |\n| :white_check_mark: OK |\n\n" +
		"Use ` :space_invader: ` or :unavailable_custom:.\n\n```text\n:space_invader:\n```"
	rendered := strings.Join(postLines(post{Text: body}, 80, messageFormatting{}, true), "\n")
	for _, want := range []string{"👾 Report", "⚠", "✅ OK", "` :space_invader: `", "\n│ :space_invader:\n", ":unavailable_custom:"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("missing %q in %q", want, rendered)
		}
	}
}

func TestMarkdownBoldSurvivesWrappingAndLinks(t *testing.T) {
	profile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(profile) })

	body := "Карточка: **[vMOP] длинный заголовок для проверки переноса**\n" +
		"Доска: __virtualization__\n" +
		"**[Kaiten](https://example.org)** и `**код**`"
	lines := postLines(post{Text: body}, 32, messageFormatting{}, true)
	plain := ansi.Strip(strings.Join(lines, "\n"))
	for _, part := range []string{"Карточка: [vMOP] длинный заголов", "Доска: virtualization", "↗ Kaiten и `**код**`"} {
		if !strings.Contains(plain, part) {
			t.Fatalf("missing %q in %q", part, plain)
		}
	}
	if strings.Count(plain, "**") != 2 || strings.Contains(plain, "__") ||
		!strings.Contains(lines[0], "\x1b[1m[vMOP]") ||
		!strings.HasPrefix(lines[1], "\x1b[1m") ||
		!strings.Contains(lines[2], "\x1b[1mvirtualization") {
		t.Fatalf("expected bold markup to be styled across wrapped lines: %q", lines)
	}
}

func TestNestedEmphasisUsesTheMatchedDelimiters(t *testing.T) {
	got := emphasizeDisplaySpans([]displaySpan{{text: "***both*** **outer *inner* outer**"}})
	var plain strings.Builder
	for _, span := range got {
		plain.WriteString(span.text)
		if (span.text == "both" || span.text == "inner") && (!span.bold || !span.italic) {
			t.Fatalf("nested emphasis lost its style: %+v", span)
		}
	}
	if plain.String() != "both outer inner outer" {
		t.Fatalf("unexpected emphasis text: %q", plain.String())
	}
}

func TestOwnMentionHighlightInMessage(t *testing.T) {
	profile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(profile) })

	body := "## @mine\n\n| Person |\n| --- |\n| @mine |\n\n" +
		"(@MINE), @mine. @other @mine.team user@mine \\@mine `@mine` [site](https://example.org/@mine)\n\n" +
		"```text\n@mine\n```"
	rendered := strings.Join(postLines(post{Text: body}, 100, messageFormatting{username: "mine"}, true), "\n")
	if got := strings.Count(rendered, unreadColor.Render("@mine")); got != 3 ||
		!strings.Contains(rendered, unreadColor.Render("@MINE")) {
		t.Fatalf("expected own mentions highlighted in heading, table and prose: %q", rendered)
	}
}
