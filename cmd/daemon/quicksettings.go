package daemon

import (
	"log"
	"time"

	"github.com/ngbcoder/NovaBar/internal/bar"
	"github.com/ngbcoder/NovaBar/internal/overlay"
	"github.com/ngbcoder/NovaBar/internal/webview"
	"github.com/ngbcoder/NovaBar/internal/win"
)

const (
	// quickSettingsWidget is the widget directory the panel is loaded from.
	quickSettingsWidget = "quicksettings"

	// quickSettingsWidth and quickSettingsHeight are the panel's smallest surface size.
	//
	// They are floors, not the size: the panel reports its content size and grows to put
	// the Spotify queue beside the settings. A fixed size would either clip the queue or
	// leave an empty sheet over the desktop when nothing is queued.
	quickSettingsWidth     int32 = 320
	quickSettingsMaxWidth  int32 = 720
	quickSettingsHeight    int32 = 220
	quickSettingsMaxHeight int32 = 560

	// quickSettingsGap is the space between the bar and the panel.
	quickSettingsGap int32 = 8

	// quickSettingsCloseAnim is how long the panel's exit animation is given to
	// play before the window is destroyed. Must match the CSS drop-out duration.
	quickSettingsCloseAnim = 160 * time.Millisecond

	// quickSettingsFallbackTop is where the panel goes when no bar can be found.
	quickSettingsFallbackTop int32 = 48

	// quickSettingsReopenGuard is how long a quick-settings toggle is ignored after the
	// panel closed itself.
	//
	// Clicking the island while the panel is open has to close it, not close and
	// reopen it. The click first takes the foreground away from the panel, so the blur
	// handler closes it before the island's toggle message arrives. Without this guard
	// the toggle would find no panel and open a new one, so the island could never
	// dismiss the panel it opened.
	quickSettingsReopenGuard = 250 * time.Millisecond
)

// toggleQuickSettings opens the panel below the bar that asked for it, or closes it.
//
// The island lives inside the bar's WebView2 surface, which is only as tall as the bar.
// CSS inside that surface cannot draw below the Win32 window, so expanding the pill in
// place is clipped no matter what the stylesheet says. A separate overlay has its own
// window and can therefore sit underneath the bar.
//
// Runs on the pump thread: creating the overlay's window and its WebView2 controller
// both belong there.
func (d *daemon) toggleQuickSettings(anchor string) {
	if d.quickSettings != nil {
		d.requestCloseQuickSettings()
		return
	}
	if !quickSettingsMayOpen(d.quickSettingsDismissedAt, time.Now()) {
		// The click that closed the panel is still arriving. Treat it as the close it
		// already was instead of opening a fresh panel in its place.
		return
	}

	if d.widgets == "" {
		log.Println("[NovaBar] No widgets directory; cannot show quick settings")
		return
	}

	perms := d.widgetPermissions(quickSettingsWidget)
	if len(perms) == 0 {
		log.Println("[NovaBar] Quick settings declares no usable permissions; refusing to open it")
		return
	}

	secret, err := randomSecret()
	if err != nil {
		log.Printf("[NovaBar] Could not create a quick settings credential: %v\n", err)
		return
	}
	if err := d.rpcs.RegisterWidget(secret, quickSettingsWidget, perms); err != nil {
		log.Printf("[NovaBar] Quick settings credential rejected: %v\n", err)
		return
	}

	boot := webview.Bootstrap{
		Port:        d.rpcs.Port(),
		Token:       d.rpcs.Token(),
		WidgetToken: secret,
		Widget:      quickSettingsWidget,
		Permissions: perms,
	}
	js, err := boot.BootstrapJS()
	if err != nil {
		d.rpcs.RevokeWidget(secret)
		log.Printf("[NovaBar] Could not build the quick settings bootstrap: %v\n", err)
		return
	}

	rect := d.quickSettingsRect(anchor)
	ov, err := overlay.Show(overlay.Options{
		Widget:      quickSettingsWidget,
		Rect:        &rect,
		Transparent: true,
		Persistent:  true,
		// A popover that survives a click elsewhere is a window the user has to hunt
		// for. Losing the foreground is the click outside, so close on it.
		DismissOnBlur: true,
		OnDismiss: func() {
			d.forgetQuickSettings()
		},
		OnBlurDismiss: func() {
			d.requestCloseQuickSettings()
		},
		OnMessage: func(payload string) {
			if payload == overlay.DismissMessage {
				go func() {
					time.Sleep(10 * time.Millisecond)
					d.onPumpThread(d.requestCloseQuickSettings)
				}()
				return
			}
			d.handleWidgetMessage(quickSettingsWidget, payload)
		},
	})
	if err != nil {
		d.rpcs.RevokeWidget(secret)
		log.Printf("[NovaBar] Could not show quick settings: %v\n", err)
		return
	}

	d.quickSettings = ov
	d.quickSettingsSecret = secret
	d.quickSettingsBar = anchor

	ov.SetBootstrap(js)
	ov.Navigate(webview.WidgetURL(d.rpcs.Port(), quickSettingsWidget))
	d.focusWindow(ov.HWND())
	// Only now is losing the foreground the user clicking away, rather than part of
	// opening the panel.
	ov.ArmDismissOnBlur()
}

// closeQuickSettings closes the panel and revokes its credential. Safe when closed.
func (d *daemon) closeQuickSettings() {
	if d.quickSettings != nil {
		d.quickSettings.Close()
		d.quickSettings = nil
	}
	d.quickSettingsBar = ""
	d.revokeQuickSettingsSecret()
	d.quickSettingsDismissedAt = time.Now()
}

// requestCloseQuickSettings begins the panel's exit animation and destroys the
// window once it has had time to play. Safe when already closed or closing.
func (d *daemon) requestCloseQuickSettings() {
	if d.quickSettings == nil {
		return
	}
	if d.quickSettings.IsClosing() {
		return
	}
	d.quickSettings.BeginClose()
	d.quickSettingsDismissedAt = time.Now()
	go func() {
		time.Sleep(quickSettingsCloseAnim)
		d.onPumpThread(d.closeQuickSettings)
	}()
}

// forgetQuickSettings drops the daemon's reference after the panel closed itself.
//
// The overlay has already destroyed its window, so it must not be closed again; only
// the credential and the down-stream state are cleared here.
func (d *daemon) forgetQuickSettings() {
	d.quickSettings = nil
	d.quickSettingsBar = ""
	d.revokeQuickSettingsSecret()
	d.quickSettingsDismissedAt = time.Now()
}

// revokeQuickSettingsSecret invalidates the credential the panel's page presented.
func (d *daemon) revokeQuickSettingsSecret() {
	if d.quickSettingsSecret != "" && d.rpcs != nil {
		d.rpcs.RevokeWidget(d.quickSettingsSecret)
	}
	d.quickSettingsSecret = ""
}

// quickSettingsMayOpen reports whether a toggle request is a fresh open rather than the
// tail of the click that just dismissed the panel.
func quickSettingsMayOpen(dismissedAt, now time.Time) bool {
	return now.Sub(dismissedAt) >= quickSettingsReopenGuard
}

// quickSettingsRect places the panel next to the bar that owns the island.
//
// The bar is preferred to a raw screen coordinate: a bar can be on any edge, and on a
// multi-monitor desktop its rectangle is the only reliable way to land under the island
// the user actually pressed.
func (d *daemon) quickSettingsRect(anchor string) win.RECT {
	width, height := d.quickSettingsOpenSize()
	return d.quickSettingsRectFor(anchor, width, height)
}

// quickSettingsOpenSize is the size a panel is opened at.
//
// The panel measures its own content and reports the size a moment later, so opening at
// the floor size left the window visibly growing into the queue column. Remembering the
// last report means every open after the first starts at the size the panel will ask
// for. The values are already clamped by the time they are recorded.
func (d *daemon) quickSettingsOpenSize() (int32, int32) {
	if d.lastQuickSettingsWidth > 0 && d.lastQuickSettingsHeight > 0 {
		return d.lastQuickSettingsWidth, d.lastQuickSettingsHeight
	}
	return quickSettingsWidth, quickSettingsHeight
}

// noteQuickSettingsSize records the panel's content size for the next open.
//
// Must run on the pump thread: it is written beside the open panel and read when the
// next one is created, and both happen there.
func (d *daemon) noteQuickSettingsSize(width, height int32) {
	d.lastQuickSettingsWidth = width
	d.lastQuickSettingsHeight = height
}

// quickSettingsRectFor places the panel at a requested content size.
func (d *daemon) quickSettingsRectFor(anchor string, width, height int32) win.RECT {
	width = quickSettingsClampWidth(width)
	height = quickSettingsClampHeight(height)
	info, ok := d.quickSettingsAnchor(anchor)
	if !ok {
		screen, err := win.PrimaryMonitor()
		if err != nil || screen.Width() <= 0 || screen.Height() <= 0 {
			screen = win.RECT{Left: 0, Top: 0, Right: 1920, Bottom: 1080}
		}
		left := screen.Left + (screen.Width()-width)/2
		return win.RECT{
			Left:   left,
			Top:    screen.Top + quickSettingsFallbackTop,
			Right:  left + width,
			Bottom: screen.Top + quickSettingsFallbackTop + height,
		}
	}

	b := info.Bounds
	return quickSettingsPlacement(info.Edge, b, width, height)
}

// resizeQuickSettings follows the size the panel reports.
//
// The panel is only as large as it needs to be: no queue means the original narrow
// popover, and a queue widens it to the right of the settings. The rectangle is derived
// from the owning bar again so a panel under a top bar opens downward while one above a
// bottom bar grows upward.
func (d *daemon) resizeQuickSettings(width, height int32) {
	if d.quickSettings == nil {
		return
	}

	d.onPumpThread(func() {
		if d.quickSettings == nil {
			return
		}
		// Remembered for the next open, so it starts at this size instead of growing
		// into it a frame after it appears.
		d.noteQuickSettingsSize(quickSettingsClampWidth(width), quickSettingsClampHeight(height))
		rect := d.quickSettingsRectFor(d.quickSettingsBar, width, height)
		if err := d.quickSettings.ResizeTo(rect); err != nil {
			log.Printf("[NovaBar] Could not resize quick settings: %v\n", err)
		}
	})
}

// quickSettingsClampWidth keeps a reported content width usable.
func quickSettingsClampWidth(width int32) int32 {
	if width < quickSettingsWidth {
		return quickSettingsWidth
	}
	if width > quickSettingsMaxWidth {
		return quickSettingsMaxWidth
	}
	return width
}

// quickSettingsClampHeight keeps a reported content height usable.
func quickSettingsClampHeight(height int32) int32 {
	if height < quickSettingsHeight {
		return quickSettingsHeight
	}
	if height > quickSettingsMaxHeight {
		return quickSettingsMaxHeight
	}
	return height
}

// quickSettingsPlacement returns the panel rectangle for a bar on the given edge.
func quickSettingsPlacement(edge string, b bar.Rect, width, height int32) win.RECT {
	width = quickSettingsClampWidth(width)
	height = quickSettingsClampHeight(height)
	var left, top int32
	switch edge {
	case "bottom":
		left = b.Left + (b.Width()-width)/2
		top = b.Top - quickSettingsGap - height
	case "left":
		left = b.Right + quickSettingsGap
		top = b.Top + (b.Height()-height)/2
	case "right":
		left = b.Left - quickSettingsGap - width
		top = b.Top + (b.Height()-height)/2
	default: // top
		left = b.Left + (b.Width()-width)/2
		top = b.Bottom + quickSettingsGap
	}

	return win.RECT{
		Left:   left,
		Top:    top,
		Right:  left + width,
		Bottom: top + height,
	}
}

// quickSettingsAnchor finds the bar the request came from, then falls back to the top
// bar and finally to any bar.
func (d *daemon) quickSettingsAnchor(name string) (bar.Info, bool) {
	infos := d.bars.Infos()

	for _, info := range infos {
		if info.Name == name {
			return info, true
		}
	}
	for _, info := range infos {
		if info.Edge == "top" {
			return info, true
		}
	}
	if len(infos) > 0 {
		return infos[0], true
	}
	return bar.Info{}, false
}
