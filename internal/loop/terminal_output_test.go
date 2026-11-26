package loop

import (
	"os"
	"testing"

	"github.com/danilrwx/telega/tui"
)

func TestTerminalOutputSynchronizesOnlyFrames(t *testing.T) {
	t.Setenv("TUI_GRAPHICS", "off")

	for _, frame := range []string{"\x1b[Hhello", "\x1b[1;1Hhello", "\x1b[?25l"} {
		t.Run(frame, func(t *testing.T) {
			file, err := os.CreateTemp(t.TempDir(), "terminal")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = file.Close() }()

			out := terminalOutput{InlineImages: tui.NewImageOutput(file)}

			n, err := out.Write([]byte(frame))
			if err != nil || n != len(frame) {
				t.Fatalf("write returned %d, %v", n, err)
			}

			got, err := os.ReadFile(file.Name())
			if err != nil {
				t.Fatal(err)
			}

			want := frame
			if frame != "\x1b[?25l" {
				want = "\x1b[?2026h" + frame + "\x1b[?2026l"
			}

			if string(got) != want {
				t.Fatalf("got %q, want %q", got, want)
			}
		})
	}
}
