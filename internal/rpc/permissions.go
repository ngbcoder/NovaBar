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
	// PermWindowsFocus brings an existing window to the front.
	//
	// Split out from PermWindowsControl because focusing is what nearly every widget
	// wants and is almost harmless, while moving and closing a window the user did not
	// target is destructive and not. Bundling them meant the launcher, whose only window
	// operation is focusing an already-running app, had to be granted the ability to
	// close windows.
	PermWindowsFocus = "windows.focus"
	// PermWindowsControl moves and closes windows.
	PermWindowsControl = "windows.control"
	// PermWorkspaceManage switches workspaces.
	PermWorkspaceManage = "workspaces.manage"
	// PermLauncherUse searches and launches applications.
	PermLauncherUse = "launcher.use"
	// PermPinsManage changes the dock's pinned applications.
	//
	// Separate from PermLauncherUse because pinning writes to disk and outlives the
	// session, while launching does not. A widget that may start programs has no
	// business rearranging what the user keeps.
	PermPinsManage = "pins.manage"
	// PermMediaControl drives media transport.
	PermMediaControl = "media.control"
	// PermAudioRead observes per-application audio sessions.
	PermAudioRead = "audio.read"
	// PermAudioControl changes per-application volume or mute.
	//
	// Separate from PermAudioRead because raising an application's volume is audible to
	// everyone at the desk, which observing a session is not.
	PermAudioControl = "audio.control"
	// PermConfigRead reads the shell configuration.
	PermConfigRead = "config.read"
	// PermConfigWrite changes the shell configuration.
	//
	// Distinct from PermConfigRead because writing config edits the shell itself: bar
	// geometry, the taskbar, the hotkey that gets you out. A widget that draws a
	// diagnostics page has no business rearranging the desktop because the user opened it.
	PermConfigWrite = "config.write"
)

// AllPermissions is every permission a widget may declare, for validation.
var AllPermissions = []string{
	PermAudioControl,
	PermAudioRead,
	PermConfigRead,
	PermConfigWrite,
	PermLauncherUse,
	PermMediaControl,
	PermPinsManage,
	PermWindowsControl,
	PermWindowsFocus,
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
	MethodWindowFocus:     PermWindowsFocus,
	MethodWindowMove:      PermWindowsControl,
	MethodWindowClose:     PermWindowsControl,
	MethodWorkspaceGet:    PermWindowsRead,
	MethodWorkspaceSwitch: PermWorkspaceManage,
	MethodLauncherSearch:  PermLauncherUse,
	MethodLauncherLaunch:  PermLauncherUse,
	MethodPinsList:        PermLauncherUse,
	MethodPinsAdd:         PermPinsManage,
	MethodPinsRemove:      PermPinsManage,
	MethodPinsToggle:      PermPinsManage,
	MethodPinsMove:        PermPinsManage,
	MethodMediaGet:        PermWindowsRead,
	MethodMediaCommand:    PermMediaControl,
	MethodSpotifyStatus:   PermWindowsRead,
	MethodSpotifyQueue:    PermWindowsRead,
	MethodSpotifyPlay:     PermMediaControl,
	MethodSpotifyPause:    PermMediaControl,
	MethodSpotifyNext:     PermMediaControl,
	MethodSpotifyPrevious: PermMediaControl,
	MethodSpotifyConnect:  PermMediaControl,
	MethodVolumeList:      PermAudioRead,
	MethodVolumeSet:       PermAudioControl,
	MethodVolumeMute:      PermAudioControl,
	MethodConfigGet:       PermConfigRead,
	MethodConfigSchema:    PermConfigRead,
	MethodConfigSet:       PermConfigWrite,
	MethodHotkeyCheck:     PermConfigWrite,
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
	// Pin changes are only interesting to a widget that can already launch things.
	TopicPinsChanged: PermLauncherUse,
	// Audio state is only pushed to widgets already allowed to observe audio.
	TopicAudioUpdated: PermAudioRead,
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
