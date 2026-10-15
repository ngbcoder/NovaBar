package launcher

import (
	"os/exec"
	"strings"
	"syscall"
)

// Launch runs the specified file, application, or shortcut without UAC elevation.
func Launch(path string) error {
	cmd := exec.Command("cmd", "/c", "start", "", path)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow: true,
	}
	return cmd.Start()
}

// LaunchWithArgs executes a command with arguments detached from the caller.
func LaunchWithArgs(target string, args ...string) error {
	fullCmd := append([]string{"/c", "start", "", target}, args...)
	cmd := exec.Command("cmd", fullCmd...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow: true,
	}
	return cmd.Start()
}

// IsURL checks if a search string is an HTTP/HTTPS URL.
func IsURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}
