package loop

import (
	"bytes"
	"errors"
	"sync"

	"github.com/danilrwx/telega/tui"
)

// Bubble Tea writes one complete alternate-screen update from the cursor home.
// Keep text and the transformed Sixel payload in one synchronized terminal frame.
type terminalOutput struct {
	*tui.InlineImages
	frame sync.Mutex
}

func (out *terminalOutput) Write(data []byte) (n int, err error) {
	out.frame.Lock()

	defer out.frame.Unlock()

	if !bytes.HasPrefix(data, []byte("\x1b[H")) && !bytes.HasPrefix(data, []byte("\x1b[1;1H")) {
		return out.InlineImages.Write(data)
	}

	if _, err = out.WriteString("\x1b[?2026h"); err != nil {
		return 0, err
	}
	defer func() {
		_, endErr := out.WriteString("\x1b[?2026l")
		err = errors.Join(err, endErr)
	}()

	return out.InlineImages.Write(data)
}
