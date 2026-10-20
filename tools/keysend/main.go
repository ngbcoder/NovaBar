// Command keysend presses a key combination.
//
// It exists to test the shell's hotkeys the way a person would use them, rather
// than by calling the handler directly: the point of a global hotkey is that the
// system delivers it, and a test that skips the system is not testing that.
package main

import (
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

// Virtual key codes the probe needs.
const (
	vkControl = 0x11
	vkAlt     = 0x12
	vkShift   = 0x10
	vkWin     = 0x5B
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: keysend <spec>   e.g. keysend Ctrl+Alt+H")
		os.Exit(2)
	}

	mods, vk, ok := parse(os.Args[1])
	if !ok {
		fmt.Printf("unknown key %q\n", os.Args[1])
		os.Exit(2)
	}

	for _, m := range mods {
		key(m, true)
	}
	key(vk, true)
	time.Sleep(30 * time.Millisecond)
	key(vk, false)
	for i := len(mods) - 1; i >= 0; i-- {
		key(mods[i], false)
	}
}

// keyEventKeyUp is the flag that marks a key as released.
const keyEventKeyUp = 0x0002

var procKeybdEvent = windows.NewLazySystemDLL("user32.dll").NewProc("keybd_event")

// key presses or releases a virtual key.
func key(vk uint32, down bool) {
	flags := uintptr(0)
	if !down {
		flags = keyEventKeyUp
	}
	procKeybdEvent.Call(uintptr(vk), 0, flags, 0)
}

// parse turns a spec like "Ctrl+Alt+H" into modifiers and a virtual key.
func parse(spec string) ([]uint32, uint32, bool) {
	var mods []uint32
	var vk uint32

	parts := splitPlus(spec)
	for i, p := range parts {
		switch lower(p) {
		case "ctrl", "control":
			mods = append(mods, vkControl)
		case "alt":
			mods = append(mods, vkAlt)
		case "shift":
			mods = append(mods, vkShift)
		case "win":
			mods = append(mods, vkWin)
		default:
			if i != len(parts)-1 {
				return nil, 0, false
			}
			code, ok := keyCode(p)
			if !ok {
				return nil, 0, false
			}
			vk = code
		}
	}

	if vk == 0 {
		return nil, 0, false
	}
	return mods, vk, true
}

// keyCode maps a key name to a virtual key code.
func keyCode(name string) (uint32, bool) {
	if len(name) == 1 {
		c := name[0]
		if c >= 'a' && c <= 'z' {
			return uint32(c), true
		}
		if c >= 'A' && c <= 'Z' {
			return uint32(c), true
		}
		if c >= '0' && c <= '9' {
			return uint32(c), true
		}
	}
	return 0, false
}

// splitPlus splits a spec on plus signs.
func splitPlus(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '+' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}

// lower makes a string lower case.
func lower(s string) string {
	out := []byte(s)
	for i, c := range out {
		if c >= 'A' && c <= 'Z' {
			out[i] = c + 32
		}
	}
	return string(out)
}
