package loop

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/danilrwx/telega/tui"
	"github.com/rivo/uniseg"
	tableast "github.com/yuin/goldmark/extension/ast"
)

const tablePreviewLines = 2

type tableCell struct {
	spans []displaySpan
	plain string
	width int
}

func renderMarkdownTable(table *tableast.Table, source []byte, width int, format messageFormatting, tableDetails bool) []string {
	columns := len(table.Alignments)

	var rows [][]tableCell

	detailed := false

	natural := make([]int, columns)
	for row := table.FirstChild(); row != nil; row = row.NextSibling() {
		cells := make([]tableCell, columns)
		column := 0

		for cell := row.FirstChild(); cell != nil; cell = cell.NextSibling() {
			raw := string(cell.Lines().Value(source))
			spans := format.displaySpans(strings.ReplaceAll(raw, "\\|", "|"))

			var plain strings.Builder

			cellWidth := 0

			for _, span := range spans {
				plain.WriteString(span.text)

				if span.image != nil {
					cellWidth += span.image.Width
				} else {
					cellWidth += uniseg.StringWidth(span.text)
				}
			}

			cells[column] = tableCell{spans: spans, plain: plain.String(), width: cellWidth}
			natural[column] = max(natural[column], cellWidth, 1)
			detailed = detailed || cellWidth > 2*width
			column++
		}

		rows = append(rows, cells)
	}

	available := width - 1 - 3*columns

	alignments := append([]tableast.Alignment(nil), table.Alignments...)

	widths := make([]int, columns)
	for column := range widths {
		widths[column] = min(natural[column], 2)
		if digits := numericColumnWidth(rows[1:], column); digits > 0 {
			widths[column] = max(widths[column], digits)
			if alignments[column] == tableast.AlignNone {
				alignments[column] = tableast.AlignRight
			}
		}

		available -= widths[column]
	}

	if available < 0 {
		return stackedTable(rows, width)
	}

	for available > 0 {
		before := available

		for i := range widths {
			step := 1
			if natural[i] > 2*width {
				step = 2
			}

			growth := max(0, min(step, natural[i]-widths[i], available))
			widths[i] += growth
			available -= growth
		}

		if available == before {
			break
		}
	}

	lines := []string{tableBorder(widths, "┌", "┬", "┐")}
	rail := muted.Render("│")

	for index, row := range rows {
		cells := make([][]string, columns)
		height := 1

		for column, cell := range row {
			cellWidth := widths[column]
			wordWrap := index > 0 && cell.width > 2*cellWidth

			value := renderDisplaySpans(cell.spans, cellWidth, true, wordWrap)
			if index > 0 && !tableDetails && len(value) > tablePreviewLines+1 {
				value = append(value[:tablePreviewLines], tableContinuation(len(value)-tablePreviewLines, cellWidth))
			}

			cells[column] = value
			height = max(height, len(value))
		}

		for line := range height {
			var text strings.Builder
			text.WriteString(rail)

			for column, cell := range cells {
				value := ""
				if line < len(cell) {
					value = cell[line]
				}

				padding := max(0, widths[column]-ansi.StringWidth(value))
				left := 0

				switch alignments[column] {
				case tableast.AlignRight:
					left = padding
				case tableast.AlignCenter:
					left = padding / 2
				}

				if index == 0 {
					value = heading.Render(value)
				}

				text.WriteString(strings.Repeat(" ", left+1) + value + strings.Repeat(" ", padding-left+1) + rail)
			}

			lines = append(lines, text.String())
		}

		if index+1 < len(rows) && (index == 0 || detailed) {
			lines = append(lines, tableBorder(widths, "├", "┼", "┤"))
		}
	}

	return append(lines, tableBorder(widths, "└", "┴", "┘"))
}

func numericColumnWidth(rows [][]tableCell, column int) int {
	width := 0

	for _, row := range rows {
		value := strings.TrimSpace(row[column].plain)
		if value == "" {
			continue
		}

		if _, err := strconv.ParseFloat(strings.TrimSuffix(value, "%"), 64); err != nil {
			return 0
		}

		width = max(width, uniseg.StringWidth(value))
	}

	return width
}

func tableBorder(widths []int, left, middle, right string) string {
	cells := make([]string, len(widths))
	for i, width := range widths {
		cells[i] = strings.Repeat("─", width+2)
	}

	return muted.Render(left + strings.Join(cells, middle) + right)
}

func stackedTable(rows [][]tableCell, width int) []string {
	var lines []string

	if len(rows) == 1 {
		for _, cell := range rows[0] {
			lines = append(lines, wrapDisplaySpans(cell.spans, width, true)...)
		}

		return lines
	}

	for index, row := range rows[1:] {
		if index > 0 {
			lines = append(lines, "")
		}

		for column, cell := range row {
			if cell.plain == "" {
				continue
			}

			spans := cell.spans

			if label := rows[0][column].plain; label != "" {
				label = tui.Clip(label, max(1, width-4))
				spans = append([]displaySpan{{text: label + "  "}}, spans...)
			}

			lines = append(lines, wrapDisplaySpans(spans, width, true)...)
		}
	}

	return lines
}

func tableContinuation(hidden, width int) string {
	unit := "lines"
	if hidden == 1 {
		unit = "line"
	}

	label := fmt.Sprintf("… %d more %s  [x]", hidden, unit)
	if uniseg.StringWidth(label) > width {
		label = fmt.Sprintf("+%d [x]", hidden)
		if uniseg.StringWidth(label) > width {
			label = "[x]"
		}
	}

	return muted.Render(tui.Clip(label, width))
}
