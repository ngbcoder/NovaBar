package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ngbcoder/NovaBar/internal/rpc"
	"github.com/ngbcoder/NovaBar/internal/win"
)

// controlTimeout bounds how long the CLI waits for the daemon to answer.
const controlTimeout = 5 * time.Second

func main() {
	var (
		rescue   = flag.Bool("rescue", false, "Restore the native Windows taskbar and shut the shell down")
		status   = flag.Bool("status", false, "Print the running shell's status")
		shutdown = flag.Bool("shutdown", false, "Shut the shell down and restore the taskbar")
		rawJSON  = flag.Bool("json", false, "Emit machine-readable JSON (with --status)")
		timeout  = flag.Duration("timeout", controlTimeout, "How long to wait for the daemon to respond")
	)
	flag.Usage = usage

	// Split verbs out of argv before parsing.
	//
	// Go's flag package stops at the first non-flag argument, so `shellctl status
	// --json` would leave "--json" sitting unparsed in Args() and silently ignore it.
	// Since the verbs are the primary form and users will type flags after them, they
	// have to be removed first for the flags to be seen at all.
	verbs, flags := splitVerbs(os.Args[1:])
	if err := flag.CommandLine.Parse(flags); err != nil {
		os.Exit(2)
	}

	// Accept bare verbs as well as flags. `shellctl status` is what people type;
	// requiring `shellctl --status` is a papercut in a tool whose whole job is to be
	// reachable under stress.
	for _, v := range verbs {
		switch strings.ToLower(v) {
		case "rescue":
			*rescue = true
		case "status":
			*status = true
		case "shutdown":
			*shutdown = true
		case "help":
			usage()
			os.Exit(0)
		}
	}

	switch {
	case *rescue:
		os.Exit(runRescue(*timeout))
	case *shutdown:
		os.Exit(runShutdown(*timeout))
	case *status:
		os.Exit(runStatus(*timeout, *rawJSON))
	default:
		usage()
		os.Exit(2)
	}
}

// verbs are the bare command words recognized without a leading dash.
var verbs = []string{"rescue", "status", "shutdown", "help"}

// splitVerbs separates bare command words from flags, preserving order.
func splitVerbs(args []string) (found []string, rest []string) {
	for _, arg := range args {
		matched := false
		for _, v := range verbs {
			if strings.EqualFold(arg, v) {
				found = append(found, v)
				matched = true
				break
			}
		}
		if !matched {
			rest = append(rest, arg)
		}
	}
	return found, rest
}

func usage() {
	fmt.Print(`NovaBar shell control CLI

Usage:
  shellctl --rescue        restore the native taskbar and shut the shell down
  shellctl --shutdown      shut the shell down and restore the taskbar
  shellctl --status        print shell status
  shellctl --status --json print shell status as JSON

Flags:
  --timeout DURATION      how long to wait for the daemon (default 5s)

Rescue tries the running daemon first, because that is the only path that also
releases the reserved AppBar work area and stops the daemon re-hiding the taskbar.
It falls back to un-hiding the taskbars directly if no daemon is reachable.
`)
}

// runRescue asks the daemon to restore the desktop and exit, falling back to a direct
// restore.
func runRescue(timeout time.Duration) int {
	if tryControl(rpc.CmdRescue, timeout) {
		return 0
	}

	fmt.Println("No running daemon found; restoring taskbars directly.")
	win.ForceRestoreTaskbar()
	fmt.Println("Taskbars restored.")
	return 0
}

// runShutdown is a quieter rescue: same effect, no emergency framing.
func runShutdown(timeout time.Duration) int {
	if tryControl(rpc.CmdShutdown, timeout) {
		return 0
	}
	fmt.Println("No running daemon found; nothing to shut down.")
	return 0
}

// tryControl sends one command to a live daemon. Returns true on success.
func tryControl(command string, timeout time.Duration) bool {
	desc, err := rpc.ReadDescriptor()
	if err != nil {
		return false
	}
	if !rpc.IsDaemonAlive(desc) {
		// A descriptor left behind by a crashed daemon. Remove it so the next run does
		// not keep reporting a dead one.
		_ = os.Remove(rpc.IPCDescriptorPath())
		return false
	}

	resp, err := rpc.SendControlCommand(desc, command, timeout)
	if err != nil {
		return false
	}
	if !resp.OK {
		fmt.Fprintf(os.Stderr, "Daemon refused %q: %s\n", command, resp.Error)
		return false
	}

	switch command {
	case rpc.CmdRescue:
		fmt.Println("Daemon restored the desktop and is shutting down.")
	default:
		fmt.Println("Daemon acknowledged.")
	}
	return true
}

// runStatus prints the daemon's status.
func runStatus(timeout time.Duration, asJSON bool) int {
	desc, err := rpc.ReadDescriptor()
	if err != nil {
		fmt.Println("NovaBar is not running (no discovery file).")
		return 1
	}
	if !rpc.IsDaemonAlive(desc) {
		_ = os.Remove(rpc.IPCDescriptorPath())
		fmt.Println("NovaBar is not running (stale discovery file removed).")
		return 1
	}

	resp, err := rpc.SendControlCommand(desc, rpc.CmdStatus, timeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not reach the daemon: %v\n", err)
		return 1
	}
	if !resp.OK {
		fmt.Fprintf(os.Stderr, "Daemon reported an error: %s\n", resp.Error)
		return 1
	}

	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(resp.Data)
		return 0
	}

	printHuman(resp.Data)
	return 0
}

// printHuman renders the status document for a terminal.
func printHuman(data any) {
	m, ok := data.(map[string]any)
	if !ok {
		fmt.Printf("%+v\n", data)
		return
	}

	fmt.Println("NovaBar status")
	fmt.Println(strings.Repeat("-", 40))
	fmt.Printf("  PID               %v\n", m["pid"])
	fmt.Printf("  Config            %v\n", m["config_path"])
	fmt.Printf("  Taskbar hidden    %v\n", m["taskbar_hidden"])
	fmt.Printf("  RPC port          %v\n", m["rpc_port"])
	fmt.Printf("  Watchdog PID      %v\n", m["watchdog_pid"])
	fmt.Printf("  Windows tracked   %v\n", m["windows_tracked"])
	fmt.Printf("  Launcher items    %v\n", m["launcher_items"])

	if tiling, ok := m["tiling"].(map[string]any); ok {
		fmt.Printf("  Tiling            %v (layout: %v)\n", tiling["enabled"], tiling["layout"])
	}

	if appbar, ok := m["appbar"].(map[string]any); ok {
		fmt.Printf("  Bars              %v active\n", appbar["count"])
		if bars, ok := appbar["bars"].([]any); ok {
			for _, raw := range bars {
				b, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				// Report what the shell actually reserved, not what was asked for. A bar
				// can be registered and still invisible.
				granted := b["granted"]
				note := ""
				if g, _ := granted.(float64); g == 0 {
					note = "  <-- INVISIBLE: no space reserved"
				}
				fmt.Printf("    %-10v %-6v requested=%-4v granted=%-4v%s\n",
					b["name"], b["edge"], b["size"], granted, note)
			}
		}
	}
	if ws, ok := m["workspace"].(map[string]any); ok {
		fmt.Printf("  Active workspace  %v\n", ws["active"])
	}

	if monitors, ok := m["monitors"].([]any); ok {
		fmt.Printf("  Monitors          %d\n", len(monitors))
		for _, raw := range monitors {
			mon, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			primary := ""
			if p, _ := mon["primary"].(bool); p {
				primary = " (primary)"
			}
			wa, _ := mon["work_area"].(map[string]any)
			fmt.Printf("    #%v%v work area %vx%v at (%v,%v)\n",
				mon["index"], primary, wa["width"], wa["height"], wa["x"], wa["y"])
		}
	}

	if hotkeys, ok := m["hotkeys"].([]any); ok && len(hotkeys) > 0 {
		fmt.Println("  Hotkeys")
		for _, h := range hotkeys {
			fmt.Printf("    %v\n", h)
		}
	} else if len(m["hotkeys"].([]any)) == 0 {
		fmt.Println("  Hotkeys           none bound")
	}
}
