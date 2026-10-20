package daemon

import (
	"log"

	"github.com/ngbcoder/NovaBar/internal/config"
	"github.com/ngbcoder/NovaBar/internal/overlay"
	"github.com/ngbcoder/NovaBar/internal/rpc"
	"github.com/ngbcoder/NovaBar/internal/webview"
)

// dockWidgetName is the widget directory the floating dock is loaded from.
const dockWidgetName = "dock"

// dockHeight is the window height for the floating dock.
//
// Taller than the icons so the magnification has somewhere to grow into and so the hover
// labels have room. The window is transparent; only the rounded panel inside it is
// painted, which is what makes the dock look like it floats.
const dockHeight = 180

// dockProvisionalWidth is the width the dock starts at.
//
// The dock reports the width of its own panel once it has rendered, and the overlay is
// sized to match. Until then something has to be assumed, and this is generous enough
// that the icons are never clipped while the real width is worked out.
const dockProvisionalWidth = 720

// dockWidthMargin is the space left around the dock's panel inside the overlay.
//
// The magnification grows the icons outwards when the pointer comes near, and the
// hover labels sit above them. Both need room the panel itself does not have, and
// without it they would be cut off at the edge of the window.
const dockWidthMargin = 40

// showDock creates the floating dock, or does nothing if it is disabled or already open.
//
// Not an AppBar: the dock reserves no work area, so windows underneath stay fully usable.
// That is the macOS behaviour and it is why this is an overlay rather than a [[bars]]
// entry. Modelling it as a bar would reserve a strip and then draw a pill inside it, which
// both wastes space and lies to the tiling engine about what is usable.
func (d *daemon) showDock() {
	if !d.cfg.Dock.Enabled || d.dock != nil || d.widgets == "" {
		return
	}

	perms := d.widgetPermissions(dockWidgetName)
	if len(perms) == 0 {
		log.Println("[NovaBar] Dock declares no usable permissions; not showing it")
		return
	}

	secret, err := randomSecret()
	if err != nil {
		log.Printf("[NovaBar] Could not create a dock credential: %v\n", err)
		return
	}
	if err := d.rpcs.RegisterWidget(secret, dockWidgetName, perms); err != nil {
		log.Printf("[NovaBar] Dock credential rejected: %v\n", err)
		return
	}

	boot := webview.Bootstrap{
		Port:        d.rpcs.Port(),
		Token:       d.rpcs.Token(),
		WidgetToken: secret,
		Widget:      dockWidgetName,
		Permissions: perms,
	}
	js, err := boot.BootstrapJS()
	if err != nil {
		d.rpcs.RevokeWidget(secret)
		log.Printf("[NovaBar] Could not build the dock bootstrap: %v\n", err)
		return
	}

	ov, err := overlay.Show(overlay.Options{
		Widget:       dockWidgetName,
		Position:     "bottom",
		BottomMargin: int32(d.cfg.Dock.BottomMargin),
		Width:        dockProvisionalWidth,
		Height:       dockHeight,
		Transparent:  true,
		Persistent:   true,
		OnMessage: func(payload string) {
			d.handleWidgetMessage(dockWidgetName, payload)
		},
	})
	if err != nil {
		d.rpcs.RevokeWidget(secret)
		log.Printf("[NovaBar] Could not show the dock: %v\n", err)
		return
	}

	d.dock = ov
	d.dockSecret = secret

	ov.SetBootstrap(js)
	ov.Navigate(webview.WidgetURL(d.rpcs.Port(), dockWidgetName))

	log.Printf("[NovaBar] Dock floating at the bottom, %dpx icons, margin %d\n",
		d.cfg.Dock.Size, d.cfg.Dock.BottomMargin)
}

// resizeDock sizes the dock's overlay to the panel the widget reported.
//
// The overlay is sized to the dock rather than to the screen, so that the rest of the
// bottom strip stays clickable. A window that covers the screen is a window that
// takes the clicks: the user could not press a button in an application along the
// bottom edge, because the dock's own invisible surface was in the way.
//
// The width the widget reports is the panel's natural width, not the magnified one,
// so the overlay does not grow and shrink as the pointer moves.
func (d *daemon) resizeDock(width int32) {
	if d.dock == nil || width <= 0 {
		return
	}

	target := width + 2*dockWidthMargin
	d.onPumpThread(func() {
		if err := d.dock.Resize(target, dockHeight); err != nil {
			log.Printf("[NovaBar] Could not resize the dock: %v\n", err)
		}
	})
}

// hideDock closes the floating dock and drops its credential.
func (d *daemon) hideDock() {
	if d.dock == nil {
		return
	}
	d.dock.Close()
	d.dock = nil
	if d.dockSecret != "" && d.rpcs != nil {
		d.rpcs.RevokeWidget(d.dockSecret)
	}
	d.dockSecret = ""
}

// syncDock applies a config change to the floating dock.
//
// Size and margin are pushed into the page rather than recreating the window: tearing down
// and rebuilding a WebView2 surface on every slider drag would flicker badly, and the
// values are already available to the page through config.schema.
func (d *daemon) syncDock(cfg config.Config) {
	if !cfg.Dock.Enabled {
		d.hideDock()
		return
	}
	if d.dock == nil {
		d.showDock()
		return
	}
	if d.rpcs != nil {
		d.rpcs.Broadcast(rpc.TopicSystemStats, map[string]any{
			"dock": map[string]any{
				"size":          cfg.Dock.Size,
				"bottom_margin": cfg.Dock.BottomMargin,
			},
		})
	}
}
