package daemon

import (
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/ngbcoder/NovaBar/internal/island"
	"github.com/ngbcoder/NovaBar/internal/notifications"
	"github.com/ngbcoder/NovaBar/internal/overlay"
	"github.com/ngbcoder/NovaBar/internal/rpc"
	"github.com/ngbcoder/NovaBar/internal/webview"
	"github.com/ngbcoder/NovaBar/internal/win"
)

// registerIslandMethods exposes the interruptible island state over RPC.
func (d *daemon) registerIslandMethods() {
	r := d.rpcs
	if r == nil {
		return
	}

	r.RegisterMethod(rpc.MethodIslandGet, func(json.RawMessage) (interface{}, error) {
		if d.island == nil {
			return nil, nil
		}
		return d.island.Current(), nil
	})

	r.RegisterMethod(rpc.MethodSettingsOpen, func(json.RawMessage) (interface{}, error) {
		d.onPumpThread(func() { d.toggleSettings() })
		return map[string]any{"ok": true}, nil
	})

	r.RegisterMethod(rpc.MethodIslandExpand, func(params json.RawMessage) (interface{}, error) {
		var req struct {
			Expanded bool    `json:"expanded"`
			AnchorX  float64 `json:"anchor_x"`
		}
		if len(params) > 0 {
			if err := json.Unmarshal(params, &req); err != nil {
				return nil, err
			}
		}
		d.setIslandExpanded(req.Expanded, req.AnchorX)
		return map[string]any{"ok": true, "expanded": req.Expanded}, nil
	})

	r.RegisterMethod(rpc.MethodIslandDismiss, func(params json.RawMessage) (interface{}, error) {
		var req struct {
			ID string `json:"id"`
		}
		if len(params) > 0 {
			if err := json.Unmarshal(params, &req); err != nil {
				return nil, err
			}
		}
		if d.island == nil {
			return nil, fmt.Errorf("island not enabled")
		}
		ok := d.island.Dismiss(req.ID)
		// When a higher-priority item is dismissed, fall back to the ambient
		// media state if the island would otherwise be empty.
		if d.island.Current() == nil {
			d.presentMediaItem()
		}
		d.broadcastIsland()
		return map[string]any{"ok": ok}, nil
	})
}

func (d *daemon) broadcastIsland() {
	if d.island == nil || d.rpcs == nil {
		return
	}
	d.rpcs.Broadcast(rpc.TopicIslandState, map[string]any{
		"item": d.island.Current(),
	})
}

// presentMediaItem reflects the current media session as the island's
// lowest-priority content. It is a no-op while a higher-priority item owns the
// island; the media will be picked up again once that item is dismissed.
func (d *daemon) presentMediaItem() {
	if d.island == nil || d.media == nil {
		return
	}
	session := d.media.GetCurrentSession()
	if session.Source == "none" || session.Title == "" {
		// Nothing playing: drop the ambient media item if it is the one shown.
		// Higher-priority items are left untouched.
		if cur := d.island.Current(); cur != nil && cur.ID == "media" {
			d.island.Dismiss("media")
		}
		return
	}
	item := island.Item{
		ID:       "media",
		Priority: island.PriorityMedia,
		Kind:     island.KindMedia,
		Title:    session.Title,
		Body:     session.Artist,
		Source:   session.Source,
		Payload: map[string]any{
			"media": session,
		},
	}
	d.island.Present(item)
}

// presentNotificationItem surfaces a notification on the island.
func (d *daemon) presentNotificationItem(n notifications.Notification) {
	if d.island == nil {
		return
	}
	item := island.Item{
		ID:       n.ID,
		Priority: island.PriorityNotification,
		Kind:     island.KindNotification,
		Title:    n.Title,
		Body:     n.Body,
		Source:   n.AppName,
	}
	d.island.Present(item)
}



// setIslandExpanded shows or hides a floating panel below the top bar.
func (d *daemon) setIslandExpanded(expanded bool, anchorX float64) {
	d.onPumpThread(func() {
		if !expanded {
			if d.islandExpanded != nil {
				d.islandExpanded.Close()
				d.islandExpanded = nil
				if d.islandExpandedSecret != "" && d.rpcs != nil {
					d.rpcs.RevokeWidget(d.islandExpandedSecret)
				}
				d.islandExpandedSecret = ""
			}
			return
		}
		if d.islandExpanded != nil {
			return
		}
		if d.widgets == "" {
			log.Println("[NovaBar] No widgets directory; cannot show island panel")
			return
		}
		secret, err := randomSecret()
		if err != nil {
			log.Printf("[NovaBar] Could not create island panel credential: %v\n", err)
			return
		}
		perms := d.widgetPermissions("islandexpanded")
		if len(perms) == 0 {
			perms = []string{"windows.read", "media.control", "config.read"}
		}
		if err := d.rpcs.RegisterWidget(secret, "islandexpanded", perms); err != nil {
			log.Printf("[NovaBar] Island panel credential rejected: %v\n", err)
			return
		}
		// Position below the top bar at the requested horizontal anchor, clamped to the
		// bar width so the panel never runs off the screen.
		if anchorX <= 0 || anchorX >= 1 {
			anchorX = 0.5
		}
		var rect *win.RECT
		if ab := d.bars.Get(win.ABE_TOP); ab != nil {
			b := ab.Bounds()
			w := int32(400)
			if b.Width() < w {
				w = b.Width()
			}
			if w < 200 {
				w = 200
			}
			h := int32(180)
			left := b.Left + int32(float64(b.Width())*anchorX) - w/2
			if left < b.Left {
				left = b.Left
			}
			if left+w > b.Right {
				left = b.Right - w
			}
			top := b.Bottom + 8
			r := win.RECT{Left: left, Top: top, Right: left + w, Bottom: top + h}
			rect = &r
		}
		ov, err := overlay.Show(overlay.Options{
			Widget:      "islandexpanded",
			Width:       400,
			Height:      180,
			Transparent: true,
			Rect:        rect,
			OnMessage: func(payload string) {
				if payload == overlay.DismissMessage {
					go func() {
						time.Sleep(10 * time.Millisecond)
						d.onPumpThread(func() { d.setIslandExpanded(false, 0.5) })
					}()
				}
			},
		})
		if err != nil {
			d.rpcs.RevokeWidget(secret)
			log.Printf("[NovaBar] Could not show island panel: %v\n", err)
			return
		}
		d.islandExpanded = ov
		d.islandExpandedSecret = secret
		boot := webview.Bootstrap{
			Port:        d.rpcs.Port(),
			Token:       d.rpcs.Token(),
			WidgetToken: secret,
			Widget:      "islandexpanded",
			Permissions: perms,
		}
		js, err := boot.BootstrapJS()
		if err != nil {
			log.Printf("[NovaBar] Island panel bootstrap failed: %v\n", err)
			return
		}
		ov.SetBootstrap(js)
		ov.Navigate(webview.WidgetURL(d.rpcs.Port(), "islandexpanded"))
	})
}
