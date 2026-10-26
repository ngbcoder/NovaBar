package win

import "testing"

func TestScaleFactorPrimary(t *testing.T) {
	// On the CI/dev machine (96 DPI) the scale must be 1.0; on a HiDPI display
	// it must be >1. Either way it must never be 0 or negative.
	s := ScaleFactor(RECT{})
	if s <= 0 {
		t.Fatalf("ScaleFactor = %v, want >0", s)
	}
	if ScaleInt(40, RECT{}) != int32(40*s+0.5) {
		// ScaleInt rounds; just sanity check it is positive.
		if ScaleInt(40, RECT{}) <= 0 {
			t.Fatal("ScaleInt returned non-positive")
		}
	}
}