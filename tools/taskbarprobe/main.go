// Command taskbarprobe reports what the native taskbar windows are doing.
//
// It exists because hiding the taskbar on Windows 11 is a fight with the shell, and
// the shell wins silently: the window comes back and nothing says so.
//
// Usage:
//
//	taskbarprobe            report the current state
//	taskbarprobe hide       hide them and report
//	taskbarprobe restore    put them back and report
//	taskbarprobe normal     put the desktop back the way Windows ships it
//	taskbarprobe guard      hide them every time they come back, and count
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/ngbcoder/NovaBar/internal/win"
	"golang.org/x/sys/windows"
)

func main() {
	if len(os.Args) < 2 {
		report("current state")
		return
	}

	switch os.Args[1] {
	case "hide":
		ts := win.NewTaskbarState()
		if err := ts.Hide(); err != nil {
			fmt.Println("hide failed:", err)
		}
		report("after hide")
	case "restore":
		win.ForceRestoreTaskbar()
		report("after restore")
	case "normal":
		normal()
		report("after normal")
	case "guard":
		guard()
	case "watch":
		watch()
	default:
		report("current state")
	}
}

// watch polls the taskbar's visibility quickly and prints the pattern.
//
// It exists to measure the one thing that matters: when the shell reveals the
// taskbar, how long is it on screen before the guard puts it away.
func watch() {
	deadline := time.Now().Add(5 * time.Second)
	last := ""
	changes := 0

	for time.Now().Before(deadline) {
		state := "hidden"
		if visibleTaskbars() > 0 {
			state = "visible"
		}
		if state != last {
			fmt.Printf("[%s] %s\n", time.Now().Format("15:04:05.000"), state)
			last = state
			changes++
		}
		time.Sleep(10 * time.Millisecond)
	}
	fmt.Printf("watch finished: %d changes in 5s\n", changes)
}

// visibleTaskbars counts the taskbar windows that are currently visible.
func visibleTaskbars() int {
	count := 0
	win.EnumWindowsWith(func(hwnd windows.HWND, lParam uintptr) uintptr {
		name, err := win.GetClassName(hwnd)
		if err != nil || (name != "Shell_TrayWnd" && name != "Shell_SecondaryTrayWnd") {
			return 1
		}
		if win.IsWindowVisible(hwnd) {
			count++
		}
		return 1
	}, 0)
	return count
}

// report prints the geometry and visibility of every taskbar window.
func report(label string) {
	fmt.Printf("== %s\n", label)

	for _, class := range []string{"Shell_TrayWnd", "Shell_SecondaryTrayWnd"} {
		found := false

		win.EnumWindowsWith(func(hwnd windows.HWND, lParam uintptr) uintptr {
			name, err := win.GetClassName(hwnd)
			if err != nil || name != class {
				return 1
			}
			found = true
			rect, _ := win.GetWindowRect(hwnd)
			fmt.Printf("  %-22s hwnd=%d visible=%v rect=%d,%d %dx%d\n",
				name, hwnd, win.IsWindowVisible(hwnd),
				rect.Left, rect.Top, rect.Width(), rect.Height())
			return 1
		}, 0)

		if !found {
			fmt.Printf("  %-22s not found\n", class)
		}
	}
}

// normal puts the desktop back exactly the way Windows ships it: the taskbar window
// sitting at the bottom of the screen and the work area excluding the strip it
// occupies.
//
// The taskbar has to be moved as well as shown, because the shell places it relative
// to the work area, and the work area it computes depends on where the taskbar is.
// The two have to be set together or they chase each other.
func normal() {
	screenW := int32(win.GetSystemMetrics(0))
	screenH := int32(win.GetSystemMetrics(1))
	height := int32(48)

	win.EnumWindowsWith(func(hwnd windows.HWND, lParam uintptr) uintptr {
		name, err := win.GetClassName(hwnd)
		if err != nil || (name != "Shell_TrayWnd" && name != "Shell_SecondaryTrayWnd") {
			return 1
		}
		win.ShowWindow(hwnd, win.SW_SHOW)
		rect, _ := win.GetWindowRect(hwnd)
		left := rect.Left
		if name == "Shell_TrayWnd" {
			left = 0
		}
		win.SetWindowPos(hwnd, 0, left, screenH-height, screenW, height,
			win.SWP_NOZORDER|win.SWP_NOACTIVATE)
		return 1
	}, 0)

	work := win.RECT{Left: 0, Top: 0, Right: screenW, Bottom: screenH - height}
	if err := win.SetWorkArea(work, false); err != nil {
		fmt.Println("could not set the work area:", err)
	}

	wa, _ := win.PrimaryWorkArea()
	fmt.Printf("   work area height: %d\n", wa)
}

// guard hides the taskbars every time they come back, and reports how often that
// happens. It is the experiment that decides whether a re-hide loop is a viable fix
// or whether the shell wins every time.
func guard() {
	ts := win.NewTaskbarState()
	deadline := time.Now().Add(30 * time.Second)
	acts := 0

	for time.Now().Before(deadline) {
		if ts.HideWindows() {
			acts++
			fmt.Printf("[%s] taskbar back: re-hid without touching the work area (%d)\n",
				time.Now().Format("15:04:05"), acts)
		}
		time.Sleep(500 * time.Millisecond)
	}

	fmt.Printf("guard finished: %d re-hides in 30s\n", acts)
}
