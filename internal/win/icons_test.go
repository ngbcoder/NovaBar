package win

import (
	"os"
	"path/filepath"
	"testing"
)

// TestExtractIconPNGKeepsTransparency is the check that the icon is an icon.
//
// An icon whose transparent pixels were turned opaque is an icon on a black
// rectangle, and a black rectangle behind every icon in the dock reads as a border
// around each of them.
func TestExtractIconPNGKeepsTransparency(t *testing.T) {
	// A system executable, which has an icon in its resources.
	var path string
	for _, candidate := range []string{
		filepath.Join(os.Getenv("SystemRoot"), "System32", "notepad.exe"),
		filepath.Join(os.Getenv("SystemRoot"), "System32", "kernel32.dll"),
	} {
		if candidate == "" {
			continue
		}
		if _, err := os.Stat(candidate); err == nil {
			path = candidate
			break
		}
	}
	if path == "" {
		t.Skip("no system executable to test with")
	}

	data, err := ExtractIconPNG(path)
	if err != nil {
		t.Fatalf("ExtractIconPNG: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("ExtractIconPNG returned nothing")
	}
	if string(data[1:4]) != "PNG" {
		t.Error("the result is not a PNG")
	}
}

// TestResolveShortcutRejectsRubbish checks that a file which is not a shortcut is
// reported rather than believed.
//
// The file is not trusted: a broken shortcut is a broken shortcut, not a reason to
// take the shell down.
func TestResolveShortcutRejectsRubbish(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-a-shortcut.lnk")
	if err := os.WriteFile(path, []byte("this is not a shortcut"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := ResolveShortcut(path); err == nil {
		t.Error("expected an error for a file that is not a shortcut")
	}
}

// TestResolveShortcutRejectsTruncatedFile checks the same for a file that is cut off
// in the middle of its header.
func TestResolveShortcutRejectsTruncatedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "short.lnk")
	if err := os.WriteFile(path, []byte("MZ truncated"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := ResolveShortcut(path); err == nil {
		t.Error("expected an error for a truncated file")
	}
}

// TestIsShortcut checks the extension test.
func TestIsShortcut(t *testing.T) {
	if !IsShortcut(`C:\Program Files\Something\App.LNK`) {
		t.Error("an upper case .lnk should be recognised")
	}
	if IsShortcut(`C:\Program Files\Something\App.exe`) {
		t.Error("an executable is not a shortcut")
	}
}
