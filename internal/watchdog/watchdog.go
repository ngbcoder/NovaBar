package watchdog

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/ngbcoder/NovaBar/internal/win"
)

// WatchdogFlag is the argument that turns a re-exec of the daemon binary into a
// watchdog. Keeping it in one place stops the spawner and the entrypoint drifting.
const WatchdogFlag = "--watchdog"

// gracePeriod is a short delay before acting, so the watchdog does not race a parent
// that has exited but is still mid-cleanup.
const gracePeriod = 250 * time.Millisecond

// Watchdog is a handle to a spawned watchdog process.
type Watchdog struct {
	pid uint32
}

// PID returns the watchdog process id.
func (w *Watchdog) PID() uint32 {
	if w == nil {
		return 0
	}
	return w.pid
}

// Start spawns a detached watchdog for the current process.
//
// Failure is returned rather than fatal: the daemon still runs, it just loses
// automatic recovery, and the caller decides whether that warrants aborting.
func Start() (*Watchdog, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("could not locate the daemon executable: %w", err)
	}

	cmd := exec.Command(exe, WatchdogFlag, strconv.Itoa(os.Getpid()))
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow: true,
		// DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP. Detached so the watchdog
		// outlives the daemon and is not killed by a console close or Ctrl+C aimed
		// at the daemon.
		CreationFlags: 0x00000008 | 0x00000200,
	}

	// Give the watchdog a log file rather than DevNull. It runs without a console and
	// long after the daemon's own log has been read, so if it ever fires and something
	// looks wrong, this file is the only evidence of what it did. Failing to open it is
	// not worth aborting the spawn for: fall back to DevNull and still get recovery.
	if logPath := LogPath(); logPath != "" {
		if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644); err == nil {
			cmd.Stdin = f
			cmd.Stdout = f
			cmd.Stderr = f
			defer f.Close()
		}
	} else {
		attachDevNull(cmd)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("could not start watchdog: %w", err)
	}

	pid := uint32(cmd.Process.Pid)
	go func() { _ = cmd.Wait() }()

	log.Printf("[NovaBar Watchdog] Started as PID %d, watching daemon PID %d\n", pid, os.Getpid())
	return &Watchdog{pid: pid}, nil
}

// attachDevNull redirects the child's stdio to the null device.
func attachDevNull(cmd *exec.Cmd) {
	f, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return
	}
	cmd.Stdin = f
	cmd.Stdout = f
	cmd.Stderr = f
}

// LogPath returns the watchdog's log file location, or "" if it cannot be determined.
func LogPath() string {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		appData = filepath.Join(home, "AppData", "Roaming")
	}
	dir := filepath.Join(appData, "NovaBar")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return ""
	}
	return filepath.Join(dir, "watchdog.log")
}

// Stop terminates the watchdog. Call on clean daemon exit so no stray process is left
// behind. Safe on a nil receiver.
func (w *Watchdog) Stop() {
	if w == nil || w.pid == 0 || !win.IsProcessAlive(w.pid) {
		return
	}
	h, err := syscall.OpenProcess(syscall.PROCESS_TERMINATE, false, w.pid)
	if err != nil {
		return
	}
	_ = syscall.TerminateProcess(h, 0)
	_ = syscall.CloseHandle(h)
	log.Printf("[NovaBar Watchdog] Stopped watchdog PID %d\n", w.pid)
}

// Run is the watchdog body: block until parentPID exits, then restore the desktop.
func Run(parentPID uint32) {
	log.SetFlags(log.Ltime)
	log.SetPrefix("[NovaBar Watchdog] ")

	log.Printf("Watching daemon PID %d", parentPID)

	exitCode, ok := win.WaitForProcess(parentPID)
	if !ok {
		// Could not open or wait on the parent: nothing to watch. Exit rather than
		// linger as an orphan that can never fire.
		log.Printf("Could not attach to daemon PID %d; exiting", parentPID)
		return
	}

	// A clean exit already ran the daemon's own cleanup, but there is no way to tell
	// for certain how the daemon died. ForceRestoreTaskbar is idempotent and cheap, so
	// run it unconditionally. Restoring the taskbar when it was never hidden is a
	// visible no-op; failing to restore it is a broken desktop.
	log.Printf("Daemon PID %d exited (code %d); restoring native taskbars", parentPID, exitCode)
	win.ForceRestoreTaskbar()
	log.Printf("Desktop restored; watchdog exiting")
}

// Entry is the root-package entrypoint. Returns the process exit code.
func Entry(parentPID uint32) int {
	time.Sleep(gracePeriod)
	Run(parentPID)
	return 0
}

// ParseInvocation reports whether these args request watchdog mode, and the parent pid.
func ParseInvocation(args []string) (uint32, bool) {
	for i, a := range args {
		if a != WatchdogFlag || i+1 >= len(args) {
			continue
		}
		pid, err := strconv.ParseUint(args[i+1], 10, 32)
		if err != nil || pid == 0 {
			continue
		}
		return uint32(pid), true
	}
	return 0, false
}

// ExecutableBase returns the file name portion of a path.
func ExecutableBase(p string) string {
	return filepath.Base(p)
}