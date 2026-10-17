package rpc

import (
	"os"
	"path/filepath"
	"testing"
)

// TestTestServerDoesNotTouchTheRealDescriptor is a regression guard.
//
// A Server defaults its discovery descriptor to %APPDATA%\NovaBar\ipc.json. If a test
// starts one without redirecting it, the test writes the user's live discovery file and
// Stop then deletes it.
//
// That descriptor is how shellctl and every widget find a running daemon, so running the
// test suite against a live shell would break its IPC discovery. For a project whose
// central promise is that it cannot strand the user, a test that can knock out the rescue
// path is not acceptable collateral.
func TestTestServerDoesNotTouchTheRealDescriptor(t *testing.T) {
	realDir := filepath.Dir(IPCDescriptorPath())

	s := startTestServer(t)
	got := s.DescriptorPath()

	if sameOrUnder(got, realDir) {
		t.Errorf("a test server writes to the real descriptor directory %s (got %s)", realDir, got)
	}

	// The descriptor must exist while running, so the tests that read it are testing
	// something real rather than passing on a missing file.
	if _, err := os.Stat(got); err != nil {
		t.Errorf("a started server did not write its descriptor: %v", err)
	}

	s.Stop()

	// And it must clean up after itself, but only its own file.
	if _, err := os.Stat(got); !os.IsNotExist(err) {
		t.Errorf("a stopped server left its descriptor behind at %s", got)
	}
}

// TestGlobalDescriptorUntouchedByServerLifecycle checks the property directly rather than
// by inspecting the helper: start and stop a test server, and confirm whatever was at the
// real path before is still there afterwards.
func TestGlobalDescriptorUntouchedByServerLifecycle(t *testing.T) {
	realPath := IPCDescriptorPath()

	before, beforeErr := os.Stat(realPath)

	s := startTestServer(t)
	s.Stop()

	after, afterErr := os.Stat(realPath)

	switch {
	case beforeErr != nil && afterErr != nil:
		// Nothing there before, nothing after. Correct.
	case beforeErr == nil && afterErr == nil:
		if before.Size() != after.Size() || before.ModTime() != after.ModTime() {
			t.Errorf("a test server modified the real descriptor at %s", realPath)
		}
	default:
		t.Errorf("a test server changed the real descriptor at %s: before=%v after=%v",
			realPath, beforeErr, afterErr)
	}
}

// sameOrUnder reports whether path is dir or lives beneath it.
func sameOrUnder(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel == "." || (len(rel) > 1 && rel[:2] != "..")
}
