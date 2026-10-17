package rpc

import (
	"fmt"
	"sort"
)

// Permissions a widget may declare in its manifest.json.
//
// The daemon enforces these on every call. A manifest is a request, not a grant: the
// mapping from widget to permission set is established by the daemon when it attaches a
// surface, and a widget cannot widen it by editing its own file.
const (
	// PermWindowsRead observes tracked windows.
	PermWindowsRead = "windows.read"
	// PermWindowsControl focuses, moves and closes windows.
	PermWindowsControl = "windows.control"
	// PermWorkspaceManage switches workspaces.
	PermWorkspaceManage = "workspaces.manage"
	// PermLauncherUse searches and launches applications.
	PermLauncherUse = "launcher.use"
	// PermMediaControl drives media transport.
	PermMediaControl = "media.control"
	// PermConfigRead reads the shell configuration.
	PermConfigRead = "config.read"
)

// AllPermissions is every permission a widget may declare, for validation.
var AllPermissions = []string{
	PermConfigRead,
	PermLauncherUse,
	PermMediaControl,
	PermWindowsControl,
	PermWindowsRead,
	PermWorkspaceManage,
}

// ValidPermission reports whether a declared permission is one this daemon understands.
func ValidPermission(p string) bool {
	for _, known := range AllPermissions {
		if known == p {
			return true
		}
	}
	return false
}

// methodPermissions maps each RPC method to the permission it requires.
//
// A method with no entry is not callable by widgets at all. That is deliberate: the
// safe default for a new method added later is to be inaccessible until someone decides
// what it should require, rather than to silently inherit the session token's access.
var methodPermissions = map[string]string{
	MethodWindowsGet:      PermWindowsRead,
	MethodWindowFocus:     PermWindowsControl,
	MethodWindowMove:      PermWindowsControl,
	MethodWindowClose:     PermWindowsControl,
	MethodWorkspaceGet:    PermWindowsRead,
	MethodWorkspaceSwitch: PermWorkspaceManage,
	MethodLauncherSearch:  PermLauncherUse,
	MethodLauncherLaunch:  PermLauncherUse,
	MethodMediaGet:        PermWindowsRead,
	MethodMediaCommand:    PermMediaControl,
	MethodConfigGet:       PermConfigRead,
}

// MethodPermissions returns the methods that widgets may call and what each needs.
func MethodPermissions() map[string]string {
	out := make(map[string]string, len(methodPermissions))
	for method, perm := range methodPermissions {
		out[method] = perm
	}
	return out
}

// sortedPermissions returns a sorted copy, for deterministic log and status output.
func sortedPermissions(perms []string) []string {
	out := append([]string(nil), perms...)
	sort.Strings(out)
	return out
}

// topicPermissions maps each pub/sub topic to the permission needed to subscribe.
//
// Without this, a widget with no permissions at all could still subscribe and be
// pushed every window title and the focused window on the desktop.
var topicPermissions = map[string]string{
	TopicWindowsUpdated:  PermWindowsRead,
	TopicWorkspaceChange: PermWindowsRead,
	TopicMediaState:      PermWindowsRead,
	TopicSystemStats:     PermWindowsRead,
}

// Client has access to this topic.
func (c *Client) hasTopic(topic string) bool {
	required, known := topicPermissions[topic]
	if !known {
		return false
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.permissions[required]
}

// errUnauthorized builds the error returned when a method is called without permission.
//
// Deliberately says which permission is missing and how to get it. A widget author
// reading this knows to add it to manifest.json, which is the intended workflow.
func errUnauthorized(method, required string) error {
	return fmt.Errorf(
		"widget does not have permission %q, required by %q; add it to the widget's manifest.json",
		required, method)
}
