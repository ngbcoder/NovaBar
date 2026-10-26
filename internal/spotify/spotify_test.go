package spotify

import (
	"os"
	"testing"
	"time"
)

// TestDisconnectForgetsToken covers the promise the settings page shows: a login is
// remembered on disk until the user explicitly disconnects.
func TestDisconnectForgetsToken(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())

	c := New("client")
	if c.IsConnected() {
		t.Fatal("a fresh client started connected")
	}

	c.mu.Lock()
	c.token = Token{
		AccessToken:  "access",
		RefreshToken: "refresh",
		TokenType:    "Bearer",
		ExpiresIn:    3600,
		Expiry:       time.Now().Add(time.Hour),
	}
	c.mu.Unlock()
	if err := c.saveToken(); err != nil {
		t.Fatalf("saveToken: %v", err)
	}

	reloaded := New("client")
	if !reloaded.IsConnected() {
		t.Fatal("token did not survive a restart")
	}

	if err := reloaded.Disconnect(); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}
	if reloaded.IsConnected() {
		t.Fatal("Disconnect left the token in memory")
	}

	path, err := TokenPath()
	if err != nil {
		t.Fatalf("TokenPath: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("token file still exists after Disconnect: %v", err)
	}

	// A third start has to come up logged out too.
	if New("client").IsConnected() {
		t.Fatal("deleted token was loaded again")
	}
}

// TestArtworkURLPicksAThumbnailSizedImage covers the choice the island depends on.
//
// Picking the widest image would download a 640px JPEG to draw a 24px square, and
// picking the smallest would look soft on a scaled display. The API lists images
// widest first, which is exactly the order that makes a naive "first" or "last"
// choice wrong.
func TestArtworkURLPicksAThumbnailSizedImage(t *testing.T) {
	images := []albumImage{
		{URL: "wide", Width: 640},
		{URL: "middle", Width: 300},
		{URL: "small", Width: 64},
	}

	if got := artworkURL(images); got != "middle" {
		t.Errorf("artworkURL = %q, want the 300px image", got)
	}

	// No image large enough: the widest available is still better than nothing.
	if got := artworkURL(images[1:]); got != "middle" {
		t.Errorf("artworkURL with only small images = %q, want %q", got, "middle")
	}

	if got := artworkURL(nil); got != "" {
		t.Errorf("artworkURL(nil) = %q, want empty", got)
	}
	if got := artworkURL([]albumImage{{URL: ""}}); got != "" {
		t.Errorf("artworkURL should skip entries with no URL, got %q", got)
	}
}
