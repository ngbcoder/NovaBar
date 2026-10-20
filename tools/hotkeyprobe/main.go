// Command hotkeyprobe reports which hotkeys Windows already owns.
//
// Registering a hotkey that another application already holds fails, so the
// proposed keymap has to be checked against what Windows has taken for itself
// before it can be written down.
package main

import (
	"fmt"

	"github.com/ngbcoder/NovaBar/internal/win"
)

func main() {
	specs := []string{
		"Win+H", "Win+J", "Win+K", "Win+L",
		"Win+Shift+H", "Win+Ctrl+H",
		"Win+1", "Win+2", "Win+9",
		"Ctrl+Win+1", "Ctrl+Win+9",
		"Alt+1", "Alt+9",
		"Ctrl+Shift+1", "Ctrl+Shift+9",
		"Win+Space", "Ctrl+Alt+Space",
		"Win+Alt+H", "Win+Alt+J", "Win+Alt+K", "Win+Alt+L",
		"Win+Shift+J", "Win+Shift+K", "Win+Shift+L",
		"Ctrl+Alt+H", "Ctrl+Alt+J", "Ctrl+Alt+K", "Ctrl+Alt+L",
		"Win+F1", "Win+Shift+F1", "Win+Ctrl+F1",
		"Alt+Shift+1", "Alt+Shift+9",
	}

	for i, spec := range specs {
		binding, err := win.ParseHotkeySpec(spec)
		if err != nil {
			fmt.Printf("%-18s invalid: %v\n", spec, err)
			continue
		}

		id := uintptr(0xB000 + i)
		if err := win.RegisterHotKey(id, binding.Modifiers, binding.VK); err != nil {
			fmt.Printf("%-18s taken by something else\n", spec)
			continue
		}
		win.UnregisterHotKeys([]uintptr{id})
		fmt.Printf("%-18s free\n", spec)
	}
}
