package media

import (
	"errors"
	"sync"
)

// PlaybackState describes the active media playback state.
type PlaybackState string

const (
	StateUnknown PlaybackState = "unknown"
	StateClosed  PlaybackState = "closed"
	StateOpened  PlaybackState = "opened"
	StatePaused  PlaybackState = "paused"
	StatePlaying PlaybackState = "playing"
	StateStopped PlaybackState = "stopped"
)

// TransportCommand is a media transport action.
type TransportCommand string

const (
	CommandPlay       TransportCommand = "play"
	CommandPause      TransportCommand = "pause"
	CommandTogglePlay TransportCommand = "toggle"
	CommandStop       TransportCommand = "stop"
	CommandNext       TransportCommand = "next"
	CommandPrevious   TransportCommand = "previous"
)

// MediaSession is the metadata for one media session.
type MediaSession struct {
	// AppID identifies the owning application, e.g. "Spotify.exe".
	AppID string `json:"app_id"`
	// Title, Artist and Album are metadata as reported by the session.
	Title  string `json:"title"`
	Artist string `json:"artist"`
	Album  string `json:"album"`

	// Artwork is a URL to album art, when the source can provide one. Sources that
	// cannot leave it empty and the UI draws a placeholder.
	Artwork string `json:"artwork,omitempty"`

	Playback PlaybackState `json:"playback"`

	// Timeline position and duration in seconds.
	Position float64 `json:"position"`
	Duration float64 `json:"duration"`

	CanPlay     bool `json:"can_play"`
	CanPause    bool `json:"can_pause"`
	CanNext     bool `json:"can_next"`
	CanPrevious bool `json:"can_previous"`

	// Source is "gsmtc" when reported by a real Windows media session, or "none"
	// when the shell has nothing to report.
	Source string `json:"source"`
}

// Controller holds the current media session and applies transport commands.
//
// The transport itself is delegated to the Windows GSMTC session manager through the
// bridge in gsmtc_windows.go. When that bridge cannot be initialized, the controller
// reports Source "none" and refuses transport commands rather than pretending to
// control playback it cannot reach.
type Controller struct {
	mu      sync.RWMutex
	current MediaSession

	bridge Bridge
}

// NewController creates a media session controller and attempts to attach to the
// system GSMTC session manager.
func NewController() *Controller {
	c := &Controller{
		current: MediaSession{Playback: StateUnknown, Source: "none"},
	}

	bridge, err := NewBridge()
	if err != nil {
		return c
	}

	// The bridge is kept even when nothing is playing. "Nothing is playing" is a
	// normal state, and dropping the bridge because of it would mean the shell
	// never noticed the first song that started.
	c.bridge = bridge

	if session, err := bridge.Current(); err == nil {
		c.current = session
	}

	return c
}

// Attached reports whether a real GSMTC session manager is reachable.
func (c *Controller) Attached() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.bridge != nil && c.bridge.Available()
}

// Reason explains why media data is not available, or "" when it is.
//
// The reason is what makes the difference between "the media area is empty" and
// "the media area is empty because this Windows build has no media session stack".
func (c *Controller) Reason() string {
	c.mu.RLock()
	bridge := c.bridge
	c.mu.RUnlock()

	if bridge == nil {
		return errNoBridge.Error()
	}
	return bridge.Reason()
}

// GetCurrentSession returns the active media metadata.
func (c *Controller) GetCurrentSession() MediaSession {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.current
}

// Refresh re-reads the current session from the bridge.
func (c *Controller) Refresh() error {
	c.mu.RLock()
	bridge := c.bridge
	c.mu.RUnlock()

	if bridge == nil {
		return errNoBridge
	}

	session, err := bridge.Current()
	if err != nil {
		// Nothing playing is not a failure of the shell. It does mean the media
		// area has to go away rather than keep showing a track that finished.
		if errors.Is(err, errNoSession) {
			c.mu.Lock()
			c.current = MediaSession{Playback: StateUnknown, Source: "none"}
			c.mu.Unlock()
		}
		return err
	}

	c.mu.Lock()
	c.current = session
	c.mu.Unlock()
	return nil
}

// Command applies a transport command to the current session.
func (c *Controller) Command(cmd TransportCommand) error {
	c.mu.RLock()
	bridge := c.bridge
	c.mu.RUnlock()

	if bridge == nil {
		return errNoBridge
	}

	if err := bridge.Command(cmd); err != nil {
		return err
	}

	// Reflect the change locally so the UI updates without waiting for the next
	// system media session change notification. The poll corrects it if the player
	// disagreed.
	c.mu.Lock()
	switch cmd {
	case CommandPlay:
		c.current.Playback = StatePlaying
	case CommandPause:
		c.current.Playback = StatePaused
	case CommandStop:
		c.current.Playback = StateStopped
	case CommandTogglePlay:
		if c.current.Playback == StatePlaying {
			c.current.Playback = StatePaused
		} else {
			c.current.Playback = StatePlaying
		}
	}
	c.mu.Unlock()

	return nil
}

// SetSession installs a session directly. Used by the bridge when the system reports
// a change, and by tests.
func (c *Controller) SetSession(session MediaSession) {
	c.mu.Lock()
	c.current = session
	c.mu.Unlock()
}
