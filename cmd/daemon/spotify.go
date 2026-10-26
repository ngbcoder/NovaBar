package daemon

import (
	"log"
	"strings"

	"github.com/ngbcoder/NovaBar/internal/config"
	"github.com/ngbcoder/NovaBar/internal/spotify"
)

// spotifyClient returns the current client, or nil when the integration is off.
func (d *daemon) spotifyClient() *spotify.Client {
	d.spotifyMu.Lock()
	defer d.spotifyMu.Unlock()
	return d.spotify
}

// spotifyClientID returns the client ID the live client was built with.
func (d *daemon) spotifyClientID() string {
	d.spotifyMu.Lock()
	defer d.spotifyMu.Unlock()
	return d.spotifyID
}

// applySpotifyConfig creates, replaces, or removes the Spotify client.
//
// Settings edits used to take effect only on the next start, so enabling Spotify or
// pasting a client ID left the Connect button talking to a daemon that still had no
// client. Rebuilding here means the button works the moment the value is saved.
func (d *daemon) applySpotifyConfig(cfg config.Config) {
	id := strings.TrimSpace(cfg.Spotify.ClientID)
	enabled := cfg.Spotify.Enabled && id != ""

	d.spotifyMu.Lock()
	if !enabled {
		d.spotify = nil
		d.spotifyID = ""
		d.spotifyMu.Unlock()
		return
	}
	if d.spotify != nil && d.spotifyID == id {
		d.spotifyMu.Unlock()
		return
	}
	// New loads the token file again, so switching client IDs cannot inherit a token
	// that was issued to a different application.
	d.spotify = spotify.New(id)
	d.spotifyID = id
	d.spotifyMu.Unlock()

	log.Println("[NovaBar] Spotify integration enabled")
}

// startSpotifyCallbackServer starts the OAuth redirect listener exactly once.
func (d *daemon) startSpotifyCallbackServer() {
	d.spotifyCallbackOnce.Do(func() {
		go d.spotifyCallbackServer()
	})
}
