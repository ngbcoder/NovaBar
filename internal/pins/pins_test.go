package pins

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "pins.json"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

func mustAdd(t *testing.T, s *Store, target, name string) {
	t.Helper()
	if err := s.Add(Pin{Target: target, Name: name}); err != nil {
		t.Fatalf("Add(%q): %v", target, err)
	}
}

func targets(s *Store) []string {
	var out []string
	for _, p := range s.List() {
		out = append(out, p.Target)
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestRoundTripPersistsOrder pins the two things a dock needs: the pins come back, and
// they come back in the order the user arranged them rather than sorted.
func TestRoundTripPersistsOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pins.json")

	first, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	for _, p := range []Pin{
		{Target: `C:\apps\zed.exe`, Name: "Zed"},
		{Target: `C:\apps\aaa.exe`, Name: "Aaa"},
		{Target: `C:\apps\mmm.exe`, Name: "Mmm"},
	} {
		if err := first.Add(p); err != nil {
			t.Fatalf("Add(%q): %v", p.Target, err)
		}
	}

	second, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}

	got := targets(second)
	want := []string{`C:\apps\zed.exe`, `C:\apps\aaa.exe`, `C:\apps\mmm.exe`}
	if !equal(got, want) {
		t.Errorf("order after reload = %v, want %v (dock order is the user's arrangement)", got, want)
	}
}

// TestAddIsIdempotentCaseInsensitively covers the double-click case and the fact that
// Windows paths are case-insensitive, so the same app must not appear twice.
func TestAddIsIdempotentCaseInsensitively(t *testing.T) {
	s := newTestStore(t)

	mustAdd(t, s, `C:\Apps\Code.exe`, "Code")
	mustAdd(t, s, `c:\apps\code.exe`, "code")
	mustAdd(t, s, `C:\APPS\CODE.EXE`, "CODE")

	if got := len(s.List()); got != 1 {
		t.Fatalf("got %d pins, want 1; the same target was stored three times", got)
	}
	if !s.IsPinned(`C:\Apps\Code.exe`) {
		t.Error("IsPinned failed for a differently-cased spelling of a stored target")
	}
}

// TestAddKeepsFirstCasing guards the detail that makes case-insensitive matching safe:
// matching folds case, but the stored path must stay as the user wrote it, or a launch
// can target a differently-cased file.
func TestAddKeepsFirstCasing(t *testing.T) {
	s := newTestStore(t)

	mustAdd(t, s, `C:\Apps\Code.exe`, "Code")
	mustAdd(t, s, `c:\apps\code.exe`, "code")

	if got := s.List()[0].Target; got != `C:\Apps\Code.exe` {
		t.Errorf("stored target = %q, want the first spelling to survive", got)
	}
}

// TestCorruptFileDegradesToEmpty is the important one: a stray byte in the dock file
// must not stop the shell starting.
func TestCorruptFileDegradesToEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pins.json")
	if err := os.WriteFile(path, []byte("{not json at all"), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open returned an error for a corrupt file: %v", err)
	}
	if len(s.List()) != 0 {
		t.Errorf("got %d pins from a corrupt file, want 0", len(s.List()))
	}
	if s.Degraded == "" {
		t.Error("Degraded is empty; the user would see an empty dock with no explanation")
	}
}

// TestOneInvalidPinDoesNotLoseTheRest checks the partial-failure path.
func TestOneInvalidPinDoesNotLoseTheRest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pins.json")
	raw := `{"version":1,"pins":[
		{"target":"","name":"No target"},
		{"target":"C:\\ok.exe","name":"Ok"}
	]}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	got := targets(s)
	if !equal(got, []string{"C:\\ok.exe"}) {
		t.Errorf("pins = %v, want the valid one kept and the invalid one dropped", got)
	}
	if s.Degraded == "" {
		t.Error("dropping a pin should be reported in Degraded")
	}
}

// TestValidateRejectsUnusableRows covers what must never reach the dock.
func TestValidateRejectsUnusableRows(t *testing.T) {
	s := newTestStore(t)

	bad := []Pin{
		{Target: "", Name: "Empty"},
		{Target: "C:\\x.exe", Name: ""},
		{Target: "C:\\x.exe", Name: "   "},
		{Target: "C:\\x\x00.exe", Name: "Nul"},  // NUL truncates at the Win32 call
		{Target: "C:\\x.exe", Name: "Bell\x07"}, // renders as garbage
		{Target: strings.Repeat("a", MaxTargetLen+1), Name: "Long"},
		{Target: "C:\\x.exe", Name: strings.Repeat("b", MaxNameLen+1)},
	}

	for _, p := range bad {
		if err := s.Add(p); err == nil {
			t.Errorf("Add accepted an invalid pin: target=%q name=%q", p.Target, p.Name)
		}
	}
	if got := len(s.List()); got != 0 {
		t.Errorf("store accepted %d invalid pins", got)
	}
}

// TestAddDoesNotPersistRejectedRow confirms a refused pin is not written, so a later
// successful save does not smuggle it in.
func TestAddDoesNotPersistRejectedRow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pins.json")

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Add(Pin{Target: "", Name: "Empty"}); err == nil {
		t.Fatal("expected an error")
	}
	mustAdd(t, s, `C:\good.exe`, "Good")

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if !equal(targets(reopened), []string{`C:\good.exe`}) {
		t.Errorf("pins = %v, want only the valid one on disk", targets(reopened))
	}
}

// TestCountIsBounded stops a runaway widget filling the disk.
func TestCountIsBounded(t *testing.T) {
	s := newTestStore(t)

	for i := 0; i < MaxPinCount; i++ {
		mustAdd(t, s, `C:\apps\app`+string(rune('a'+i%26))+string(rune('a'+i/26))+`.exe`, "App")
	}
	if got := len(s.List()); got != MaxPinCount {
		t.Fatalf("store holds %d, want %d", got, MaxPinCount)
	}

	err := s.Add(Pin{Target: `C:\apps\toomany.exe`, Name: "Too Many"})
	if err == nil {
		t.Error("Add succeeded past MaxPinCount")
	}
}

// TestToggleReportsNewState is what a pin button depends on: one round trip tells the
// caller what it just did, without re-reading the list.
func TestToggleReportsNewState(t *testing.T) {
	s := newTestStore(t)
	p := Pin{Target: `C:\apps\code.exe`, Name: "Code"}

	pinned, err := s.Toggle(p)
	if err != nil || !pinned {
		t.Fatalf("first Toggle = (%v, %v), want (true, nil)", pinned, err)
	}

	pinned, err = s.Toggle(p)
	if err != nil || pinned {
		t.Fatalf("second Toggle = (%v, %v), want (false, nil)", pinned, err)
	}

	if s.IsPinned(p.Target) {
		t.Error("still pinned after a toggle that reported unpinned")
	}
}

// TestToggleDoesNotLoseCasing covers the bug a folded lookup key would introduce.
func TestToggleDoesNotLoseCasing(t *testing.T) {
	s := newTestStore(t)

	if _, err := s.Toggle(Pin{Target: `C:\Apps\Code.exe`, Name: "Code"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Toggle(Pin{Target: `c:\apps\code.exe`, Name: "Code"}); err != nil {
		t.Fatal(err)
	}

	// Re-add and confirm the original spelling survived the unpin/re-pin round trip.
	if _, err := s.Toggle(Pin{Target: `C:\Apps\Code.exe`, Name: "Code"}); err != nil {
		t.Fatal(err)
	}
	if got := s.List()[0].Target; got != `C:\Apps\Code.exe` {
		t.Errorf("target = %q, want the original casing to survive toggling", got)
	}
}

// TestRemoveUnknownIsNoOp keeps a stale UI from surfacing a failure.
func TestRemoveUnknownIsNoOp(t *testing.T) {
	s := newTestStore(t)
	mustAdd(t, s, `C:\apps\a.exe`, "A")

	if err := s.Remove(`C:\apps\never-pinned.exe`); err != nil {
		t.Errorf("Remove of an unpinned target returned %v, want nil", err)
	}
	if got := len(s.List()); got != 1 {
		t.Errorf("pins = %d, want the existing pin untouched", got)
	}
}

// TestMoveClampsOutOfRange: a drag that overshoots should still land somewhere.
func TestMoveClampsOutOfRange(t *testing.T) {
	s := newTestStore(t)
	for _, n := range []string{"a", "b", "c"} {
		mustAdd(t, s, `C:\apps\`+n+`.exe`, n)
	}

	if err := s.Move(`C:\apps\c.exe`, 99); err != nil {
		t.Fatalf("Move to an out-of-range index: %v", err)
	}
	if !equal(targets(s), []string{`C:\apps\a.exe`, `C:\apps\b.exe`, `C:\apps\c.exe`}) {
		t.Errorf("after clamping to the end: %v", targets(s))
	}

	if err := s.Move(`C:\apps\a.exe`, -5); err != nil {
		t.Fatalf("Move to a negative index: %v", err)
	}
	if !equal(targets(s), []string{`C:\apps\a.exe`, `C:\apps\b.exe`, `C:\apps\c.exe`}) {
		t.Errorf("after clamping to the front: %v", targets(s))
	}

	if err := s.Move(`C:\apps\b.exe`, 0); err != nil {
		t.Fatal(err)
	}
	if !equal(targets(s), []string{`C:\apps\b.exe`, `C:\apps\a.exe`, `C:\apps\c.exe`}) {
		t.Errorf("after an in-range move: %v", targets(s))
	}

	if err := s.Move(`C:\apps\missing.exe`, 0); err == nil {
		t.Error("moving an unpinned target should report an error")
	}
}

// TestDeriveProcName pins the agreement between what the daemon stores and what the dock
// widget computes in JavaScript. If these drift, a pinned app stops showing as running.
func TestDeriveProcName(t *testing.T) {
	cases := []struct{ target, want string }{
		{`C:\Program Files\App\Thing.exe`, "thing"},
		{`C:/apps/thing`, "thing"},
		{`C:\apps\thing.EXE`, "thing"},
		{`  C:\apps\thing.exe  `, "thing"},
		// An AppUserModelID has no file component to take a name from.
		{`Microsoft.WindowsCalculator_8wekyb3d8bbwe!App`, ""},
		{``, ""},
	}
	for _, tc := range cases {
		if got := DeriveProcName(tc.target); got != tc.want {
			t.Errorf("DeriveProcName(%q) = %q, want %q", tc.target, got, tc.want)
		}
	}
}

// TestNormalizeFillsProcName checks Add derives the field the dock matches on.
func TestNormalizeFillsProcName(t *testing.T) {
	s := newTestStore(t)
	mustAdd(t, s, `C:\apps\Thing.exe`, "Thing")

	if got := s.List()[0].ProcName; got != "thing" {
		t.Errorf("ProcName = %q, want %q", got, "thing")
	}
}

// TestConcurrentAccess runs the store under -race. The daemon serves pins from the RPC
// thread while widgets subscribe, so this is the real access pattern.
func TestConcurrentAccess(t *testing.T) {
	s := newTestStore(t)

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			target := `C:\apps\app` + string(rune('a'+i)) + `.exe`
			for j := 0; j < 20; j++ {
				_ = s.Add(Pin{Target: target, Name: "App"})
				_ = s.IsPinned(target)
				_ = s.List()
				_, _ = s.Toggle(Pin{Target: target, Name: "App"})
			}
		}(i)
	}
	wg.Wait()

	if got := len(s.List()); got > MaxPinCount {
		t.Errorf("store grew past the limit to %d", got)
	}
}

// TestMissingFileIsFirstRunNotAnError: an absent file is the normal first launch.
func TestMissingFileIsFirstRunNotAnError(t *testing.T) {
	s := newTestStore(t)
	if got := len(s.List()); got != 0 {
		t.Errorf("a new store has %d pins", got)
	}
	if s.Degraded != "" {
		t.Errorf("a new store reports Degraded = %q, want empty", s.Degraded)
	}
}

// TestWriteIsAtomicInPractice checks a save leaves no partial file and no temp debris.
func TestWriteIsAtomicInPractice(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pins.json")

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	mustAdd(t, s, `C:\apps\a.exe`, "A")

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "pins-") {
			t.Errorf("a temporary file was left behind: %s", e.Name())
		}
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "a.exe") {
		t.Errorf("written file does not contain the pin: %s", raw)
	}
}

// TestEmptyPathIsRejected catches a store wired up with no destination, which would
// otherwise fail silently on first save.
func TestEmptyPathIsRejected(t *testing.T) {
	if _, err := Open(""); err == nil {
		t.Error("Open(\"\") returned no error")
	}
}
