package main

import (
	"strings"

	"github.com/yuin/goldmark/ast"
	tableast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

func markdownProseLines(body string, width int, format messageFormatting, tableDetails bool) []string {
	if !strings.Contains(body, "#") && (!strings.Contains(body, "\n") || !strings.ContainsAny(body, "|=-")) {
		return format.proseLines(body, width)
	}

	source := []byte(body)
	document := linkParser.Parse(text.NewReader(source))
	var lines []string
	at := 0
	for node := document.FirstChild(); node != nil; node = node.NextSibling() {
		var start, end int
		var rendered []string
		switch node := node.(type) {
		case *tableast.Table:
			start = node.FirstChild().Pos()
			end = nextMarkdownLine(body, node.LastChild().Pos())
			if node.ChildCount() == 1 {
				// The delimiter has no AST node in a header-only table.
				end = nextMarkdownLine(body, end)
			}
			rendered = renderMarkdownTable(node, source, width, format, tableDetails)
		case *ast.Heading:
			start = strings.LastIndexByte(body[:node.Pos()], '\n') + 1
			end = nextMarkdownLine(body, start)
			if !strings.HasPrefix(strings.TrimSpace(body[start:end]), "#") && node.Lines().Len() > 0 {
				end = nextMarkdownLine(body, node.Lines().At(node.Lines().Len()-1).Start)
				end = nextMarkdownLine(body, end)
			}
			rendered = renderMarkdownHeading(node, source, width, format)
		default:
			continue
		}
		if start > at {
			lines = append(lines, format.proseLines(strings.TrimSuffix(body[at:start], "\n"), width)...)
		}
		lines = append(lines, rendered...)
		at = end
	}
	if at == 0 || at < len(body) {
		lines = append(lines, format.proseLines(body[at:], width)...)
	} else if strings.HasSuffix(body, "\n") {
		lines = append(lines, "")
	}
	return lines
}

func nextMarkdownLine(body string, start int) int {
	if newline := strings.IndexByte(body[start:], '\n'); newline >= 0 {
		return start + newline + 1
	}
	return len(body)
}

func renderMarkdownHeading(node *ast.Heading, source []byte, width int, format messageFormatting) []string {
	var title []string
	segments := node.Lines()
	for i := range segments.Len() {
		segment := segments.At(i)
		title = append(title, strings.TrimSpace(string(segment.Value(source))))
	}
	lines := wrapDisplaySpans(format.displaySpans(strings.Join(title, " ")), width, true)
	style := heading
	if node.Level >= 3 {
		style = accent.Bold(true)
	}
	for i, line := range lines {
		lines[i] = style.Render(line)
	}
	return lines
}
