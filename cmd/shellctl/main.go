package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/novabar-shell/novabar/internal/win"
)

func main() {
	rescueFlag := flag.Bool("rescue", false, "Emergency restore native Windows taskbar and work areas")
	flag.Parse()

	if *rescueFlag {
		fmt.Println("[NovaBar+ Rescue] Restoring native taskbars...")
		win.ForceRestoreTaskbar()
		fmt.Println("[NovaBar+ Rescue] Restoration signal sent.")
		os.Exit(0)
	}

	fmt.Println("NovaBar+ Shell Control CLI (shellctl)")
	fmt.Println("Usage:")
	fmt.Println("  shellctl --rescue    Emergency restore native Windows taskbars")
}
