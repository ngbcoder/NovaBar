package win

import (
	"fmt"
	"sort"
	"strings"
)

// HotkeyBinding is a parsed system-wide hotkey: a modifier mask plus a virtual key.
type HotkeyBinding struct {
	Modifiers uint32
	VK        uint32
	Spec      string
}

// String renders the binding back into its canonical config form.
func (b HotkeyBinding) String() string {
	parts := make([]string, 0, 5)
	if b.Modifiers&MOD_CONTROL != 0 {
		parts = append(parts, "Ctrl")
	}
	if b.Modifiers&MOD_ALT != 0 {
		parts = append(parts, "Alt")
	}
	if b.Modifiers&MOD_SHIFT != 0 {
		parts = append(parts, "Shift")
	}
	if b.Modifiers&MOD_WIN != 0 {
		parts = append(parts, "Win")
	}
	parts = append(parts, VirtualKeyName(b.VK))
	return strings.Join(parts, "+")
}

// vkNames maps virtual-key codes to the spelling used in config.toml.
var vkNames = map[uint32]string{
	VK_SPACE: "Space",
	VK_LEFT:  "Left",
	VK_RIGHT: "Right",
	VK_1:     "1",
	VK_2:     "2",
	VK_3:     "3",
	VK_4:     "4",
	VK_5:     "5",
	VK_6:     "6",
	VK_7:     "7",
	VK_8:     "8",
	VK_9:     "9",
	VK_R:     "R",
}

// VirtualKeyName returns the config spelling for a virtual-key code, or a hex form
// for codes outside the named set.
func VirtualKeyName(vk uint32) string {
	if name, ok := vkNames[vk]; ok {
		return name
	}
	// Letters and digits are accepted on the way in by virtualKeys, so they have to be
	// spelled on the way out too. Without this a perfectly good "Ctrl+Alt+S" comes back as
	// "Ctrl+Alt+0x53", and that is then what gets written to config.toml on the next edit.
	if c := rune(vk); (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
		return string(c)
	}
	return fmt.Sprintf("0x%02X", vk)
}

// virtualKeys is the reverse of vkNames, extended with A-Z and 0-9 ranges.
var virtualKeys = func() map[string]uint32 {
	m := make(map[string]uint32, len(vkNames)+36)
	for vk, name := range vkNames {
		m[strings.ToLower(name)] = vk
	}
	for c := 'A'; c <= 'Z'; c++ {
		m[strings.ToLower(string(c))] = uint32(c)
	}
	for c := '0'; c <= '9'; c++ {
		m[string(c)] = uint32(c)
	}
	return m
}()

// ParseHotkeySpec parses a config hotkey string such as "Ctrl+Alt+Shift+R",
// "Win+Space" or "Alt+Left" into a binding.
//
// Modifiers are order-independent and case-insensitive. At least one modifier is
// required: a bare key like "R" would swallow that key system-wide and make the
// desktop unusable, so it is rejected rather than silently accepted.
func ParseHotkeySpec(spec string) (HotkeyBinding, error) {
	trimmed := strings.TrimSpace(spec)
	if trimmed == "" {
		return HotkeyBinding{}, fmt.Errorf("hotkey spec is empty")
	}

	segments := strings.Split(trimmed, "+")
	var modifiers uint32
	keyPart := ""
	var keySeen bool

	for _, seg := range segments {
		name := strings.ToLower(strings.TrimSpace(seg))
		if name == "" {
			continue
		}
		switch name {
		case "ctrl", "control":
			modifiers |= MOD_CONTROL
		case "alt":
			modifiers |= MOD_ALT
		case "shift":
			modifiers |= MOD_SHIFT
		case "win", "meta", "super", "cmd":
			modifiers |= MOD_WIN
		default:
			if keySeen {
				return HotkeyBinding{}, fmt.Errorf("hotkey %q names more than one non-modifier key", spec)
			}
			keyPart = name
			keySeen = true
		}
	}

	if !keySeen {
		return HotkeyBinding{}, fmt.Errorf("hotkey %q names modifiers but no key", spec)
	}
	if modifiers == 0 {
		return HotkeyBinding{}, fmt.Errorf("hotkey %q has no modifier; refusing to bind a bare key system-wide", spec)
	}

	vk, ok := virtualKeys[keyPart]
	if !ok {
		return HotkeyBinding{}, fmt.Errorf("hotkey %q: unknown key %q", spec, keyPart)
	}

	return HotkeyBinding{Modifiers: modifiers | MOD_NOREPEAT, VK: vk, Spec: trimmed}, nil
}

// SortedHotkeyIDs returns registration ids in ascending order so that unregistration
// and logging are deterministic regardless of map iteration order.
func SortedHotkeyIDs(bindings map[uintptr]HotkeyBinding) []uintptr {
	ids := make([]uintptr, 0, len(bindings))
	for id := range bindings {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}
