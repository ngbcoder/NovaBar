package win

import (
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// ProcessName returns the executable file name for a pid, e.g. "notepad.exe".
//
// Returns "" when the process cannot be opened, which is normal: elevated and
// session-0 processes are not readable from an unelevated daemon, and the rule
// evaluator treats an unknown process name as "matches nothing" rather than failing.
func ProcessName(pid uint32) string {
	path := ProcessImagePath(pid)
	if path == "" {
		return ""
	}
	return filepath.Base(path)
}

// ProcessImagePath returns the full executable path for a pid.
func ProcessImagePath(pid uint32) string {
	if pid == 0 {
		return ""
	}

	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)

	// QueryFullProcessImageNameW needs a buffer; 32768 is the documented max path.
	buf := make([]uint16, 32768)
	n := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err != nil {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}

// ShortProcessName returns the process name without the .exe suffix, lowercased.
// This is the form used for rule matching so config reads naturally
// (process = "taskmgr" rather than "Taskmgr.exe").
func ShortProcessName(pid uint32) string {
	name := ProcessName(pid)
	if name == "" {
		return ""
	}
	return strings.ToLower(strings.TrimSuffix(name, filepath.Ext(name)))
}
