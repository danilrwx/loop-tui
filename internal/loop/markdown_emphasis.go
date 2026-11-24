package loop

import (
	"cmp"
	"slices"
	"strings"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

type emphasisPosition struct {
	ast.Emphasis
	open, close int
}

type positionedEmphasisParser struct{ parser.InlineParser }

func (p positionedEmphasisParser) Parse(parent ast.Node, reader text.Reader, pc parser.Context) ast.Node {
	node := p.InlineParser.Parse(parent, reader, pc)
	if delimiter, ok := node.(*parser.Delimiter); ok {
		delimiter.Processor = &positionedDelimiter{DelimiterProcessor: delimiter.Processor}
	}

	return node
}

// Keep both delimiters so text can be styled without reflowing the Markdown blocks.
type positionedDelimiter struct {
	parser.DelimiterProcessor
	opener, closer *parser.Delimiter
}

func (d *positionedDelimiter) CanOpenCloser(opener, closer *parser.Delimiter) bool {
	d.opener, d.closer = opener, closer
	return d.DelimiterProcessor.CanOpenCloser(opener, closer)
}

func (d *positionedDelimiter) OnMatch(size int) ast.Node {
	return &emphasisPosition{
		Emphasis: *ast.NewEmphasis(size),
		open:     d.opener.Segment.Stop,
		close:    d.closer.Segment.Start + d.closer.OriginalLength - d.closer.Length - size,
	}
}

var emphasisParser = parser.NewParser(
	parser.WithBlockParsers(parser.DefaultBlockParsers()...),
	parser.WithInlineParsers(parser.DefaultInlineParsers()...),
	parser.WithInlineParsers(util.Prioritized(positionedEmphasisParser{parser.NewEmphasisParser()}, 490)),
)

func emphasizeDisplaySpans(spans []displaySpan) []displaySpan {
	var body strings.Builder
	for _, span := range spans {
		body.WriteString(span.text)
	}

	source := body.String()
	if !strings.ContainsAny(source, "*_") {
		return spans
	}

	type marker struct {
		at, size int
		delta    int
	}

	var markers []marker

	document := emphasisParser.Parse(text.NewReader([]byte(source)))
	_ = ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if emphasis, ok := node.(*emphasisPosition); entering && ok {
			size := emphasis.Level
			markers = append(markers, marker{emphasis.open, size, 1}, marker{emphasis.close, size, -1})
		}

		return ast.WalkContinue, nil
	})

	if len(markers) == 0 {
		return spans
	}

	slices.SortFunc(markers, func(a, b marker) int { return cmp.Compare(a.at, b.at) })

	var (
		styled                           []displaySpan
		offset, skip, bold, italic, next int
	)
	for _, span := range spans {
		end := offset + len(span.text)
		appendText := func(from, to int) {
			if from < to {
				piece := span
				piece.text = source[from:to]
				piece.bold, piece.italic = bold > 0, italic > 0
				styled = append(styled, piece)
			}
		}
		start := max(offset, skip)

		for next < len(markers) && markers[next].at < end {
			m := markers[next]
			appendText(start, m.at)

			if m.size == 2 {
				bold += m.delta
			} else {
				italic += m.delta
			}

			skip = m.at + m.size
			start = skip
			next++
		}

		appendText(start, end)
		offset = end
	}

	return styled
}
