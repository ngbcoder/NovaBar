package bar

import "testing"

func TestEdgeForName(t *testing.T) {
	cases := map[string]uint32{
		"top":    EdgeTop,
		"bottom": EdgeBottom,
		"left":   EdgeLeft,
		"right":  EdgeRight,
	}

	for name, want := range cases {
		got, ok := EdgeForName(name)
		if !ok {
			t.Errorf("EdgeForName(%q) reported unknown", name)
			continue
		}
		if got != want {
			t.Errorf("EdgeForName(%q) = %d, want %d", name, got, want)
		}
	}

	if _, ok := EdgeForName("diagonal"); ok {
		t.Error("EdgeForName should reject an unknown edge")
	}
}

func TestEdgeName(t *testing.T) {
	cases := map[uint32]string{
		EdgeTop:    "top",
		EdgeBottom: "bottom",
		EdgeLeft:   "left",
		EdgeRight:  "right",
		99:         "unknown",
	}

	for edge, want := range cases {
		if got := EdgeName(edge); got != want {
			t.Errorf("EdgeName(%d) = %q, want %q", edge, got, want)
		}
	}
}

// TestManagerStartsEmpty covers the pre-Apply state used before any config is read.
func TestManagerStartsEmpty(t *testing.T) {
	m := NewManager()
	if m.Count() != 0 {
		t.Errorf("a new manager should have no bars, got %d", m.Count())
	}
	if len(m.Describe()) != 0 {
		t.Error("Describe should be empty for a new manager")
	}
	if m.Get(EdgeTop) != nil {
		t.Error("Get on an unmanaged edge should return nil")
	}
}

// TestRegistryPreventsDoubleRegistration covers the second line of defence against
// ABM_NEW being sent twice for one HWND.
//
// ABM_NEW is not reference counted: registering the same window twice and removing it
// once leaves the shell with a reserved strip that nobody owns. The per-instance flag in
// AppBarManager catches the obvious case; this catches anything that bypasses it.
func TestRegistryPreventsDoubleRegistration(t *testing.T) {
	// Use an arbitrary non-zero value; the registry never dereferences the handle.
	const fake = 0x1234

	if registerWithShell(fake, "top") != true {
		t.Fatal("first registration should succeed")
	}
	if registerWithShell(fake, "top") != false {
		t.Error("second registration of the same HWND should be refused")
	}

	if got := len(RegisteredEdges()); got != 1 {
		t.Errorf("registry should hold 1 entry, got %d", got)
	}

	unregisterFromShell(fake)
	if got := len(RegisteredEdges()); got != 0 {
		t.Errorf("registry should be empty after unregister, got %d", got)
	}

	// After unregistering, registration must be possible again.
	if registerWithShell(fake, "left") != true {
		t.Error("registration should succeed again after unregister")
	}
	unregisterFromShell(fake)
}

// TestRegistryDistinguishesHandles makes sure two genuinely different windows can both
// register, which the single-bar implementation never exercised.
func TestRegistryDistinguishesHandles(t *testing.T) {
	const a, b = 0xAAAA, 0xBBBB

	if !registerWithShell(a, "top") {
		t.Fatal("first should register")
	}
	if !registerWithShell(b, "bottom") {
		t.Fatal("a different HWND should register independently")
	}

	edges := RegisteredEdges()
	if len(edges) != 2 {
		t.Fatalf("expected 2 registered bars, got %d", len(edges))
	}
	if edges[a] != "top" || edges[b] != "bottom" {
		t.Errorf("edges = %v", edges)
	}

	unregisterFromShell(a)
	unregisterFromShell(b)
}
