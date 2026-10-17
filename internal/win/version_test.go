package win

import "testing"

// TestOsVersionSupportGate pins the Windows 11 boundary.
func TestOsVersionSupportGate(t *testing.T) {
	cases := []struct {
		build     uint32
		supported bool
	}{
		{19041, false}, // Windows 10 20H2
		{19045, false}, // Windows 10 22H2, the last Windows 10 build
		{21999, false}, // just below the threshold
		{22000, true},  // Windows 11 21H2, the first
		{22621, true},  // Windows 11 22H2
		{26100, true},  // Windows 11 24H2
		{28000, true},  // future build
	}

	for _, tc := range cases {
		v := OsVersion{Major: 10, Minor: 0, Build: tc.build}
		if got := v.IsSupported(); got != tc.supported {
			t.Errorf("build %d: IsSupported() = %v, want %v", tc.build, got, tc.supported)
		}

		reason := v.UnsupportedReason()
		if tc.supported && reason != "" {
			t.Errorf("build %d: expected no reason, got %q", tc.build, reason)
		}
		if !tc.supported && reason == "" {
			t.Errorf("build %d: expected an explanation", tc.build)
		}
	}
}

// TestWindows10ReasonIsSpecific checks that a Windows 10 user is told what they are on
// rather than just "unsupported".
func TestWindows10ReasonIsSpecific(t *testing.T) {
	v := OsVersion{Major: 10, Build: 19045}
	reason := v.UnsupportedReason()

	if reason == "" {
		t.Fatal("expected a reason")
	}
	if !contains(reason, "Windows 10") {
		t.Errorf("reason should name Windows 10, got: %q", reason)
	}
	if !contains(reason, "19045") {
		t.Errorf("reason should include the detected build, got: %q", reason)
	}
}

func TestOsVersionString(t *testing.T) {
	v := OsVersion{Major: 10, Minor: 0, Build: 22631}
	if got := v.String(); got != "10.0.22631" {
		t.Errorf("String() = %q, want %q", got, "10.0.22631")
	}
}

// TestGetOsVersionOnThisMachine is the only test that touches the real OS. It confirms
// the RtlGetVersion binding is wired correctly rather than returning zeros, which would
// make every supported machine look unsupported.
func TestGetOsVersionOnThisMachine(t *testing.T) {
	v, err := GetOsVersion()
	if err != nil {
		t.Fatalf("GetOsVersion: %v", err)
	}
	if v.Build == 0 {
		t.Fatal("build number is 0; the RtlGetVersion binding is not returning real data")
	}
	if v.Major != 10 {
		t.Errorf("MajorVersion = %d, want 10 for any modern Windows", v.Major)
	}
	t.Logf("running on Windows %s (supported: %v)", v, v.IsSupported())
}

func contains(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) &&
		indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
