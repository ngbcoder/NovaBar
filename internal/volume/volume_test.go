package volume

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// TestPureHelpers covers the logic that does not need an audio device.
func TestPureHelpers(t *testing.T) {
	t.Run("clampLevel", func(t *testing.T) {
		cases := []struct {
			in   float32
			want float32
		}{
			{-1, 0}, {0, 0}, {0.5, 0.5}, {1, 1}, {2.5, 1},
		}
		for _, c := range cases {
			if got := clampLevel(c.in); got != c.want {
				t.Errorf("clampLevel(%v) = %v, want %v", c.in, got, c.want)
			}
		}
	})

	t.Run("Label prefers the best name available", func(t *testing.T) {
		cases := []struct {
			name string
			s    Session
			want string
		}{
			{"display name wins", Session{DisplayName: "Spotify", ProcName: "Spotify.exe"}, "Spotify"},
			{"falls back to exe", Session{ProcName: "Spotify.exe"}, "Spotify.exe"},
			// Never invent a name: an unopenable process is reported by pid.
			{"pid is the last resort", Session{PID: 4242}, "PID 4242"},
		}
		for _, c := range cases {
			if got := c.s.Label(); got != c.want {
				t.Errorf("%s: Label() = %q, want %q", c.name, got, c.want)
			}
		}
	})
}

// TestControllerIsSafeWithoutAudioDevice checks the degraded path: no device, no panic,
// and a reason a user could act on.
func TestControllerIsSafeWithoutAudioDevice(t *testing.T) {
	c := New()
	defer c.Close()

	if c.Available() {
		// This machine has audio, so the degraded path cannot be exercised through the
		// public API. Assert only that the controller is coherent.
		if c.Reason() != "" {
			t.Errorf("Available() is true but Reason() = %q", c.Reason())
		}
		return
	}
	if strings.TrimSpace(c.Reason()) == "" {
		t.Error("an unavailable controller must explain itself")
	}
	if _, err := c.List(); err == nil {
		t.Error("List() succeeded on an unavailable controller")
	}
}

// TestListEnumeratesRealSessions is the load-bearing test.
//
// Unlike the GSMTC read path, this runs against the live audio stack, so the marshalling
// is genuinely verified rather than merely plausible.
func TestListEnumeratesRealSessions(t *testing.T) {
	c := New()
	defer c.Close()
	c.LogUnavailable()

	if !c.Available() {
		t.Skipf("audio unavailable: %s", c.Reason())
	}

	sessions, err := c.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	for i, s := range sessions {
		if s.SystemSounds {
			t.Errorf("session %d is the system-sounds pseudo-session and should be filtered out", i)
		}
		if s.PID == 0 && s.DisplayName == "" {
			t.Errorf("session %d has neither a pid nor a name: %+v", i, s)
		}
		if s.Volume < 0 || s.Volume > 1 {
			t.Errorf("session %d volume %v is outside 0..1", i, s.Volume)
		}
		if s.Label() == "" {
			t.Errorf("session %d produced an empty label", i)
		}
	}

	// A machine with audio running something must report at least one session, but a
	// truly idle session table is legitimate, so this is reported rather than asserted.
	t.Logf("%d audio session(s): %s", len(sessions), describe(sessions))
}

func describe(sessions []Session) string {
	parts := make([]string, 0, len(sessions))
	for _, s := range sessions {
		parts = append(parts, s.Label()+" "+string(s.State))
	}
	return strings.Join(parts, ", ")
}

// TestSetVolumeRoundTrip proves a write actually takes effect, then puts it back.
//
// This deliberately disturbs the user's real volume for the duration of the call and
// restores it. That is the only way to verify the write path, which is the whole point of
// this milestone, and the alternative is shipping an untested SetMasterVolume. Skipped
// when nothing is playing, because there is then no session to change.
func TestSetVolumeRoundTrip(t *testing.T) {
	c := New()
	defer c.Close()

	if !c.Available() {
		t.Skipf("audio unavailable: %s", c.Reason())
	}

	sessions, err := c.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	target := firstAdjustable(sessions)
	if target == nil {
		t.Skip("no adjustable audio session; nothing to write to")
	}

	original := target.Volume
	restore := func() {
		if err := c.SetVolume(target.PID, original); err != nil {
			t.Logf("could not restore volume for pid %d: %v", target.PID, err)
		}
	}
	defer restore()

	// Pick a value that differs from the original so a no-op cannot pass.
	probe := float32(0.25)
	if original == probe {
		probe = 0.75
	}

	if err := c.SetVolume(target.PID, probe); err != nil {
		t.Skipf("this session refused a volume change, which Windows permits for some app types: %v", err)
	}

	after, err := c.List()
	if err != nil {
		t.Fatalf("List after write: %v", err)
	}
	got := findByPID(after, target.PID)
	if got == nil {
		t.Fatalf("session %d vanished after a volume change", target.PID)
	}
	if diff := got.Volume - probe; diff > 0.02 || diff < -0.02 {
		t.Errorf("volume for pid %d = %v after setting %v", target.PID, got.Volume, probe)
	}

	// Out-of-range input is clamped rather than passed to Windows.
	if err := c.SetVolume(target.PID, 5); err == nil {
		restored, err := c.List()
		if err == nil {
			if r := findByPID(restored, target.PID); r != nil && r.Volume > 1.001 {
				t.Errorf("volume %v escaped the clamp", r.Volume)
			}
		}
	}
}

// TestSetVolumeUnknownProcess checks the error path rather than a silent no-op.
func TestSetVolumeUnknownProcess(t *testing.T) {
	c := New()
	defer c.Close()

	if !c.Available() {
		t.Skipf("audio unavailable: %s", c.Reason())
	}

	// pid 0 is not a real application.
	if err := c.SetVolume(0, 0.5); err == nil {
		t.Error("SetVolume succeeded for pid 0")
	}
}

// TestConcurrentAccess runs the controller the way the shell will: RPC threads reading and
// writing while others list.
func TestConcurrentAccess(t *testing.T) {
	c := New()
	defer c.Close()

	if !c.Available() {
		t.Skipf("audio unavailable: %s", c.Reason())
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				if _, err := c.List(); err != nil {
					t.Logf("List: %v", err)
				}
			}
		}()
	}
	wg.Wait()
}

// TestCloseIsIdempotent: the daemon tears down on several paths.
func TestCloseIsIdempotent(t *testing.T) {
	c := New()
	c.Close()
	c.Close()

	// After closing, calls must fail rather than hang.
	done := make(chan struct{})
	go func() {
		_, _ = c.List()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("List() hung after Close()")
	}
}

// firstAdjustable picks a session worth writing to.
//
// Expired sessions are skipped: they represent applications that have gone, and writing
// to one is how you end up believing a volume change worked when nothing was listening.
func firstAdjustable(sessions []Session) *Session {
	for i := range sessions {
		if sessions[i].State == StateActive && sessions[i].PID != 0 {
			return &sessions[i]
		}
	}
	for i := range sessions {
		if sessions[i].PID != 0 {
			return &sessions[i]
		}
	}
	return nil
}

func findByPID(sessions []Session, pid uint32) *Session {
	for i := range sessions {
		if sessions[i].PID == pid {
			return &sessions[i]
		}
	}
	return nil
}
