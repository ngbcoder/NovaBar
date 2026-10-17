package bar

import "testing"

// TestInflateTowardEdge covers the placement primitive for each edge.
func TestInflateTowardEdge(t *testing.T) {
	area := Rect{Left: 0, Top: 0, Right: 1920, Bottom: 1080}

	cases := []struct {
		name      string
		edge      uint32
		thickness int32
		want      Rect
	}{
		{"top", EdgeTop, 40, Rect{Left: 0, Top: 0, Right: 1920, Bottom: 40}},
		{"bottom", EdgeBottom, 8, Rect{Left: 0, Top: 1072, Right: 1920, Bottom: 1080}},
		{"left", EdgeLeft, 64, Rect{Left: 0, Top: 0, Right: 64, Bottom: 1080}},
		{"right", EdgeRight, 10, Rect{Left: 1910, Top: 0, Right: 1920, Bottom: 1080}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := inflateTowardEdge(area, tc.edge, tc.thickness); got != tc.want {
				t.Errorf("inflateTowardEdge(%s, %d) = %+v, want %+v",
					tc.name, tc.thickness, got, tc.want)
			}
		})
	}
}

// TestInflateTowardEdgeRespectsArea makes sure a bar cannot grow past a work area that
// has already been trimmed by a perpendicular bar.
func TestInflateTowardEdgeRespectsArea(t *testing.T) {
	// Area already trimmed to start below a top bar.
	area := Rect{Left: 0, Top: 40, Right: 1920, Bottom: 1080}

	got := inflateTowardEdge(area, EdgeLeft, 64)
	want := Rect{Left: 0, Top: 40, Right: 64, Bottom: 1080}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// TestInflateTowardEdgeClampsToArea covers a bar thicker than the space left for it,
// which must collapse rather than invert.
func TestInflateTowardEdgeClampsToArea(t *testing.T) {
	area := Rect{Left: 0, Top: 0, Right: 1920, Bottom: 30}

	got := inflateTowardEdge(area, EdgeTop, 200)
	if got.Top > got.Bottom {
		t.Errorf("bar thicker than the area produced an inverted rect: %+v", got)
	}
	if got.Bottom != 30 {
		t.Errorf("Bottom = %d, want it clamped to the area's edge (30)", got.Bottom)
	}
}

// TestIsPerpendicular documents which edge pairs meet at a corner.
func TestIsPerpendicular(t *testing.T) {
	if !isPerpendicular(EdgeTop, EdgeLeft) {
		t.Error("top and left meet at a corner")
	}
	if !isPerpendicular(EdgeBottom, EdgeRight) {
		t.Error("bottom and right meet at a corner")
	}
	if isPerpendicular(EdgeTop, EdgeBottom) {
		t.Error("top and bottom are parallel, not perpendicular")
	}
	if isPerpendicular(EdgeLeft, EdgeRight) {
		t.Error("left and right are parallel, not perpendicular")
	}
}

// TestSubtractEdge covers the area arithmetic the work area depends on.
func TestSubtractEdge(t *testing.T) {
	screen := Rect{Left: 0, Top: 0, Right: 1920, Bottom: 1080}

	top := subtractEdge(screen, Rect{Left: 0, Top: 0, Right: 1920, Bottom: 40})
	if top != (Rect{Left: 0, Top: 40, Right: 1920, Bottom: 1080}) {
		t.Errorf("subtract top bar = %+v", top)
	}

	left := subtractEdge(screen, Rect{Left: 0, Top: 0, Right: 64, Bottom: 1080})
	if left != (Rect{Left: 64, Top: 0, Right: 1920, Bottom: 1080}) {
		t.Errorf("subtract left bar = %+v", left)
	}

	// Both, applied in sequence: the corner is removed by the first one only.
	both := subtractEdge(top, Rect{Left: 0, Top: 40, Right: 64, Bottom: 1080})
	if both != (Rect{Left: 64, Top: 40, Right: 1920, Bottom: 1080}) {
		t.Errorf("subtract both = %+v", both)
	}

	// A bar that does not overlap must leave the area alone.
	none := subtractEdge(screen, Rect{Left: 5000, Top: 5000, Right: 5100, Bottom: 5100})
	if none != screen {
		t.Errorf("a non-overlapping bar changed the area: %+v", none)
	}
}
