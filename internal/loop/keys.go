package loop

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/danilrwx/telega/tui"
)

func commandKeyName(key tea.KeyMsg) string {
	if key.Type == tea.KeyRunes && !key.Paste && len(key.Runes) == 1 {
		if r := tui.RussianCommandRune(key.Runes[0]); r != key.Runes[0] {
			key.Runes = []rune{r}
		}
	}

	return key.String()
}
