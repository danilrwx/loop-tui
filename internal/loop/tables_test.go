package loop

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestMarkdownTableAlignsColumnsAndRetainsLinks(t *testing.T) {
	p := post{Text: "### Test results\n\n" +
		"| :dvp: Cluster | ✅ Passed | ⏩ Skipped | ❌ Failed | 📊 Total | 📈 Success Rate |\n" +
		"| --- | ---: | --- | --- | --- | --- |\n" +
		"| [redos (replicated)](https://example.org/results) | 189 | 36 | 11 | 236 | 94.50% |\n" +
		"| alt (local) | 176 | 42 | 18 | 236 | 90.72% |\n\n" +
		"Finished.", Files: []string{"report"}}

	lines := postLines(p, 100, messageFormatting{}, true)
	output := strings.Join(lines, "\n")

	plain := ansi.Strip(output)
	for _, want := range []string{
		"Test results\n\n┌", "┬", "┼", "↗ redos (replicated)", "│       189 │",
		"90.72% │", "┘\n\nFinished.\n[1 attachment · o]",
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("missing %q from:\n%s", want, plain)
		}
	}

	if !strings.Contains(output, ansi.SetHyperlink("https://example.org/results")) ||
		!strings.Contains(output, ansi.ResetHyperlink()) {
		t.Fatal("table link should retain its terminal hyperlink")
	}

	borders := 0

	for _, line := range lines {
		if strings.HasPrefix(ansi.Strip(line), "┌") {
			borders = ansi.StringWidth(line)
		}

		if borders != 0 && strings.ContainsAny(ansi.Strip(line), "│┼┘") && ansi.StringWidth(line) != borders {
			t.Errorf("row width %d does not match border width %d: %q", ansi.StringWidth(line), borders, line)
		}
	}

	narrow := postLines(p, 25, messageFormatting{}, true)
	for _, line := range narrow {
		if ansi.StringWidth(line) > 25 {
			t.Errorf("narrow row overflows: %q", line)
		}
	}

	if got := ansi.Strip(strings.Join(narrow, "\n")); !strings.Contains(got, "Success Rate  94.50%") &&
		!strings.Contains(got, "📈 Success Rate  94.50%") {
		t.Fatalf("narrow table should keep the percentage legible:\n%s", got)
	}
}

func TestHeaderOnlyTableAndCodeFence(t *testing.T) {
	message := "| Name | Count |\n| --- | ---: |\n\nNext block.\n\n" +
		"```text\n| Name | Count |\n| --- | ---: |\n```"

	got := ansi.Strip(strings.Join(postLines(post{Text: message}, 60, messageFormatting{}, true), "\n"))
	if strings.Count(got, "┌") != 1 || strings.Count(got, "| --- | ---: |") != 1 ||
		!strings.Contains(got, "┘\n\nNext block.") {
		t.Fatalf("header-only table should consume its delimiter, and code should stay literal:\n%s", got)
	}
}

func TestVerboseCellsStayInsideTheTableWhenFoldedAndExpanded(t *testing.T) {
	reason := "Expected success, but got an error:                controller failed to reconcile. " +
		strings.Repeat("The virtual machine remained in the terminating phase. ", 7) + "FINAL_DIAGNOSTIC"
	body := "| Tests | Reason |\n| --- | --- |\n| PeeringMigration | " + reason + " |\n" +
		"| AfterSuite | Controller logs contain an error. |"

	collapsed := postLines(post{Text: body}, 64, messageFormatting{}, false)

	expanded := postLines(post{Text: body}, 64, messageFormatting{}, true)
	if len(collapsed) >= len(expanded) || ansi.Strip(collapsed[0]) != ansi.Strip(expanded[0]) {
		t.Fatal("expanding the cell must keep the table and column widths")
	}

	for _, lines := range [][]string{collapsed, expanded} {
		plain := ansi.Strip(strings.Join(lines, "\n"))
		for _, fragment := range []string{"┌", "┬", "Tests", "Reason", "PeeringMigration", "AfterSuite", "┴", "┘"} {
			if !strings.Contains(plain, fragment) {
				t.Fatalf("missing %q from the table:\n%s", fragment, plain)
			}
		}

		for _, line := range lines {
			if ansi.StringWidth(line) != 64 {
				t.Fatalf("table row does not match its border: %q", line)
			}
		}
	}

	preview := ansi.Strip(strings.Join(collapsed, "\n"))
	if !strings.Contains(preview, "[x]") || strings.Contains(preview, "FINAL_DIAGNOSTIC") {
		t.Fatalf("expected the long value to be folded inside its cell:\n%s", preview)
	}

	var words []string

	for _, line := range expanded[3:] {
		cells := strings.SplitN(ansi.Strip(line), "│", 4)
		if len(cells) != 4 {
			break
		}

		words = append(words, strings.Fields(cells[2])...)
	}

	if got := strings.Join(words, " "); got != strings.Join(strings.Fields(reason), " ") {
		t.Fatalf("word wrapping split words or lost cell text: %s", got)
	}
}
