package onboarding

import (
	"fmt"
	"log"
	"unsafe"

	"github.com/novabar-shell/novabar/internal/config"
	"golang.org/x/sys/windows"
)

// ShowWelcomeNotice displays an informative, non-elevated welcome modal and console banner.
func ShowWelcomeNotice(cfg *config.Config) {
	banner := `
============================================================
              Welcome to NovaBar+ Desktop Shell             
============================================================
  NovaBar+ is a lightweight desktop shell and window manager
  designed for Windows 10 & 11.

  KEY FEATURES:
  * Automatic Window Tiling (BSP, Columns, Rows, Monocle)
  * Custom AppBar & Workspace overlays
  * Modular HTML/CSS/JS Widget host
  * Zero CGo / Zero UAC elevation required

  SAFETY & RESCUE:
  * Your native Windows taskbar is safe.
  * If you ever need to restore everything immediately,
    run: .\bin\shellctl.exe --rescue
  * Default rescue hotkey: %s
============================================================
`
	fmt.Printf(banner, cfg.General.RescueKey)

	// Show native MessageBoxW (No UAC, fully standard user-space Win32 notification)
	title, _ := windows.UTF16PtrFromString("Welcome to NovaBar+")
	text, _ := windows.UTF16PtrFromString(fmt.Sprintf(
		"Welcome to NovaBar+!\n\n"+
			"NovaBar+ is now managing your desktop layout.\n\n"+
			"Safety Notice:\n"+
			"• NovaBar+ runs completely without UAC elevation.\n"+
			"• If you ever want to restore your native Windows taskbar,\n"+
			"  run 'shellctl --rescue' or press %s.\n\n"+
			"Would you like to continue?", cfg.General.RescueKey))

	modUser32 := windows.NewLazySystemDLL("user32.dll")
	procMessageBoxW := modUser32.NewProc("MessageBoxW")

	const MB_ICONINFORMATION = 0x00000040
	const MB_OK = 0x00000000

	_, _, _ = procMessageBoxW.Call(
		0,
		uintptr(unsafe.Pointer(text)),
		uintptr(unsafe.Pointer(title)),
		uintptr(MB_ICONINFORMATION|MB_OK),
	)

	// Mark FirstRun as complete and persist
	cfg.General.FirstRun = false
	if err := config.Save(*cfg); err != nil {
		log.Printf("[NovaBar+] Notice saving onboarding status: %v\n", err)
	}
}
