package daemon

import (
	"testing"

	"github.com/ngbcoder/NovaBar/internal/config"
)

// TestApplySpotifyConfigRebuildsClient pins the behaviour the settings page relies on:
// enabling the integration and pasting a client ID must reach the daemon without a
// restart, and an unchanged pair must not drop a login that is in progress.
func TestApplySpotifyConfigRebuildsClient(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	d := &daemon{}

	d.applySpotifyConfig(config.Config{
		Spotify: config.SpotifyConfig{Enabled: true, ClientID: "client-a"},
	})
	first := d.spotifyClient()
	if first == nil {
		t.Fatal("enabled config produced no client")
	}

	d.applySpotifyConfig(config.Config{
		Spotify: config.SpotifyConfig{Enabled: true, ClientID: "client-a"},
	})
	if got := d.spotifyClient(); got != first {
		t.Fatal("unchanged config replaced the client and lost its pending login state")
	}

	d.applySpotifyConfig(config.Config{
		Spotify: config.SpotifyConfig{Enabled: true, ClientID: "client-b"},
	})
	second := d.spotifyClient()
	if second == nil || second == first {
		t.Fatal("changed client ID did not rebuild the client")
	}
	if id := d.spotifyClientID(); id != "client-b" {
		t.Fatalf("spotifyClientID() = %q, want client-b", id)
	}

	d.applySpotifyConfig(config.Config{
		Spotify: config.SpotifyConfig{Enabled: false, ClientID: "client-b"},
	})
	if d.spotifyClient() != nil {
		t.Fatal("disabling Spotify left a client behind")
	}
}
