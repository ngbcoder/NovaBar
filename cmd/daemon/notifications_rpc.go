package daemon

import (
	"encoding/json"
	"fmt"

	"github.com/ngbcoder/NovaBar/internal/rpc"
)

func (d *daemon) registerNotificationMethods() {
	r := d.rpcs

	r.RegisterMethod(rpc.MethodNotificationsList, func(json.RawMessage) (interface{}, error) {
		if d.notifications == nil {
			return []any{}, nil
		}
		return map[string]any{
			"notifications": d.notifications.List(),
			"dnd":           d.notifications.DND(),
		}, nil
	})

	r.RegisterMethod(rpc.MethodNotificationsDismiss, func(params json.RawMessage) (interface{}, error) {
		if d.notifications == nil {
			return nil, fmt.Errorf("notifications not enabled")
		}
		var req struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, err
		}
		ok := d.notifications.Dismiss(req.ID)
		d.broadcastNotifications()
		return map[string]any{"ok": ok}, nil
	})

	r.RegisterMethod(rpc.MethodNotificationsClear, func(json.RawMessage) (interface{}, error) {
		if d.notifications == nil {
			return nil, fmt.Errorf("notifications not enabled")
		}
		d.notifications.Clear()
		d.broadcastNotifications()
		return map[string]any{"ok": true}, nil
	})

	r.RegisterMethod(rpc.MethodNotificationsDNDSet, func(params json.RawMessage) (interface{}, error) {
		if d.notifications == nil {
			return nil, fmt.Errorf("notifications not enabled")
		}
		var req struct {
			DND bool `json:"dnd"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, err
		}
		d.notifications.SetDND(req.DND)
		d.broadcastNotifications()
		return map[string]any{"ok": true, "dnd": req.DND}, nil
	})
}

func (d *daemon) broadcastNotifications() {
	if d.notifications == nil || d.rpcs == nil {
		return
	}
	d.rpcs.Broadcast(rpc.TopicNotificationsUpdated, map[string]any{
		"notifications": d.notifications.List(),
		"dnd":           d.notifications.DND(),
	})
}
