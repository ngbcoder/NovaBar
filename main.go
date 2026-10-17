package main

import (
	"fmt"
	"os"

	daemon "github.com/ngbcoder/NovaBar/cmd/daemon"
	"github.com/ngbcoder/NovaBar/internal/watchdog"
)

// Version is the daemon version. Kept in one place so `--version` and the README
// cannot drift apart silently.
const Version = "0.2.0-m1.5"

func main() {
	// The daemon re-execs itself as a watchdog. Detect that before parsing any user
	// flags, so watchdog mode never touches the user's config or desktop state.
	if pid, ok := watchdog.ParseInvocation(os.Args[1:]); ok {
		os.Exit(watchdog.Entry(pid))
	}

	for _, arg := range os.Args[1:] {
		switch arg {
		case "--version", "-v":
			fmt.Printf("NovaBar %s\n", Version)
			return
		case "--help", "-h":
			usage()
			return
		}
	}

	daemon.Run()
}

func usage() {
	fmt.Print(`NovaBar - a lightweight desktop shell and tiling window manager for Windows

Usage:
  novabar.exe                run the shell daemon
  novabar.exe --version      print the version

Recovery:
  shellctl --rescue          restore the native taskbar and quit the shell

The rescue hotkey (default Ctrl+Alt+Shift+R) is registered at startup. A watchdog
sub-process restores the taskbar automatically if this process is killed.
`)
}
