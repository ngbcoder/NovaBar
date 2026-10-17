package launcher

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// Validation errors surfaced to RPC callers.
var (
	errEmptyTarget          = errors.New("launch target is empty")
	errTargetTooLong        = errors.New("launch target exceeds the Windows MAX_PATH limit")
	errMetacharacter        = errors.New("launch target contains shell metacharacters")
	errUnsupportedExtension = errors.New("launch target has an unsupported extension")
)

// Launch runs an application by target.
//
// The target is validated before anything is executed. cmd.exe is used only to
// resolve .lnk shortcuts and shell: URIs, which have no direct CreateProcess path; for
// a plain .exe the process is started directly so no command interpreter ever parses
// the string.
func Launch(target string) error {
	if err := ValidateTarget(target); err != nil {
		return err
	}

	lower := strings.ToLower(target)

	// shell: URIs and UWP AppUserModelIDs need the shell, not CreateProcess.
	if strings.HasPrefix(lower, "shell:") || strings.Contains(target, "!") {
		return launchViaShell(target)
	}

	// Bare name: resolve through PATH so we never hand it to cmd.
	if !strings.ContainsAny(target, `:\/`) {
		resolved, err := exec.LookPath(target)
		if err != nil {
			return fmt.Errorf("could not resolve %q on PATH: %w", target, err)
		}
		target = resolved
	}

	if strings.EqualFold(filepath.Ext(target), ".lnk") {
		return launchViaShell(target)
	}

	if ext := strings.ToLower(filepath.Ext(target)); ext == ".exe" {
		if _, err := os.Stat(target); err != nil {
			return fmt.Errorf("cannot launch %q: %w", target, err)
		}
	}

	return launchDirect(target)
}

// launchDirect starts an executable with no command interpreter involved.
func launchDirect(target string) error {
	cmd := exec.Command(target)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow: true,
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	// Release the handle; the launched process outlives us.
	go func() { _ = cmd.Wait() }()
	return nil
}

// launchViaShell delegates to cmd.exe for .lnk and shell: URIs.
//
// Only called after ValidateTarget, which rejects every character cmd treats as a
// separator, so the interpreter has nothing to misparse.
func launchViaShell(target string) error {
	cmd := exec.Command("cmd", "/c", "start", "", target)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow: true,
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// LaunchArgs executes a command with arguments, detached from the daemon.
func LaunchArgs(target string, args []string) error {
	if err := ValidateTarget(target); err != nil {
		return err
	}
	for _, a := range args {
		if IsShellMetacharacter(a) {
			return fmt.Errorf("argument %q contains shell metacharacters", a)
		}
	}

	cmd := exec.Command(target, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// IsURL reports whether a search string is an HTTP or HTTPS URL.
func IsURL(s string) bool {
	lower := strings.ToLower(s)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}
