package launcher

import "testing"

// newTestIndex builds an index from literal items, bypassing the filesystem scan.
func newTestIndex(items []AppItem) *Indexer {
	return &Indexer{items: items}
}

func TestSearchRanksExactFirst(t *testing.T) {
	idx := newTestIndex([]AppItem{
		{Name: "Notepad++", Target: "notepad++.exe"},
		{Name: "Notepad", Target: "notepad.exe"},
		{Name: "Notepads", Target: "notepads.exe"},
	})

	got := idx.Search("notepad", 10)
	if len(got) == 0 {
		t.Fatal("expected matches")
	}
	if got[0].Name != "Notepad" {
		t.Errorf("first result = %q, want %q (exact match must rank first)", got[0].Name, "Notepad")
	}
}

func TestSearchPrefixBeforeSubstring(t *testing.T) {
	idx := newTestIndex([]AppItem{
		{Name: "My Custom Notepad", Target: "mycustomnotepad.exe"},
		{Name: "Notepad", Target: "notepad.exe"},
	})

	got := idx.Search("notep", 10)
	if len(got) < 2 {
		t.Fatalf("expected 2 matches, got %d", len(got))
	}
	if got[0].Name != "Notepad" {
		t.Errorf("first result = %q, want the prefix match %q", got[0].Name, "Notepad")
	}
}

func TestSearchAcronym(t *testing.T) {
	idx := newTestIndex([]AppItem{
		{Name: "Visual Studio Code", Target: "code.exe"},
		{Name: "Calculator", Target: "calc.exe"},
	})

	got := idx.Search("vsc", 10)
	if len(got) != 1 {
		t.Fatalf("expected 1 acronym match, got %d", len(got))
	}
	if got[0].Name != "Visual Studio Code" {
		t.Errorf("got %q, want %q", got[0].Name, "Visual Studio Code")
	}
}

func TestSearchLastWord(t *testing.T) {
	idx := newTestIndex([]AppItem{
		{Name: "Visual Studio Code", Target: "code.exe"},
	})

	if got := idx.Search("code", 10); len(got) != 1 {
		t.Errorf("expected a last-word match, got %d results", len(got))
	}
}

func TestSearchSubsequence(t *testing.T) {
	idx := newTestIndex([]AppItem{
		{Name: "Windows Terminal", Target: "wt.exe"},
	})

	// "wt" is not a prefix or contiguous substring of "windows terminal" in name
	// order, but the initials match.
	if got := idx.Search("wt", 10); len(got) == 0 {
		t.Error("expected the initials to match")
	}
}

func TestSearchNoMatchReturnsEmpty(t *testing.T) {
	idx := newTestIndex([]AppItem{{Name: "Notepad", Target: "notepad.exe"}})

	if got := idx.Search("zzzzqqq", 10); len(got) != 0 {
		t.Errorf("expected no matches, got %d", len(got))
	}
}

func TestSearchEmptyQueryReturnsAllUpToLimit(t *testing.T) {
	idx := newTestIndex([]AppItem{
		{Name: "A", Target: "a.exe"},
		{Name: "B", Target: "b.exe"},
		{Name: "C", Target: "c.exe"},
	})

	got := idx.Search("", 2)
	if len(got) != 2 {
		t.Errorf("expected the limit to apply, got %d", len(got))
	}
}

func TestSearchZeroLimitFallsBackToDefault(t *testing.T) {
	idx := newTestIndex([]AppItem{
		{Name: "A", Target: "a.exe"},
		{Name: "B", Target: "b.exe"},
	})

	// A limit of 0 must not mean "return nothing"; the launcher passes 0 when the
	// caller omits it.
	if got := idx.Search("a", 0); len(got) != 1 {
		t.Errorf("expected 1 result, got %d", len(got))
	}
}

func TestSearchIsDeterministic(t *testing.T) {
	idx := newTestIndex([]AppItem{
		{Name: "Alpha Editor", Target: "a.exe"},
		{Name: "Alpha Viewer", Target: "b.exe"},
		{Name: "Alpha Writer", Target: "c.exe"},
	})

	first := idx.Search("alpha", 10)
	for i := 0; i < 20; i++ {
		got := idx.Search("alpha", 10)
		for j := range got {
			if got[j].Name != first[j].Name {
				t.Fatalf("result order changed between calls: %v vs %v", first, got)
			}
		}
	}
}

// TestValidateTargetRejectsInjection is the security regression: the original launcher
// passed RPC-supplied paths straight to `cmd /c start`, where "&" and "|" are command
// separators.
func TestValidateTargetRejectsInjection(t *testing.T) {
	malicious := []string{
		`calc.exe & calc.exe`,
		`calc.exe | calc.exe`,
		`calc.exe && calc.exe`,
		`calc.exe > out.txt`,
		`calc.exe < in.txt`,
		`calc.exe ^& calc.exe`,
		"calc.exe\ncalc.exe",
		`calc.exe %PATH%`,
		`"calc.exe"`,
		"calc`whoami`",
	}

	for _, target := range malicious {
		if err := ValidateTarget(target); err == nil {
			t.Errorf("ValidateTarget(%q) accepted a shell injection payload", target)
		}
	}
}

func TestValidateTargetAcceptsLegitimate(t *testing.T) {
	legit := []string{
		`C:\Program Files\App\app.exe`,
		`C:\Users\me\AppData\Roaming\Microsoft\Windows\Start Menu\Programs\App.lnk`,
		`notepad.exe`,
		`chrome`,
		`shell:AppsFolder`,
		`Microsoft.WindowsCalculator_8wekyb3d8bbwe!App`,
		`ms-settings:`,
	}

	for _, target := range legit {
		if err := ValidateTarget(target); err != nil {
			t.Errorf("ValidateTarget(%q) rejected a legitimate target: %v", target, err)
		}
	}
}

func TestValidateTargetRejectsEmpty(t *testing.T) {
	if err := ValidateTarget("   "); err == nil {
		t.Error("expected an empty target to be rejected")
	}
}

func TestValidateTargetRejectsOverlong(t *testing.T) {
	long := ""
	for i := 0; i < 300; i++ {
		long += "a"
	}
	if err := ValidateTarget(long); err == nil {
		t.Error("expected an over-long target to be rejected")
	}
}

func TestValidateTargetRejectsUnsupportedExtension(t *testing.T) {
	if err := ValidateTarget(`C:\Windows\System32\evil.bat`); err == nil {
		t.Error("expected a .bat target to be rejected: it is a script, not an app")
	}
}

func TestIsURL(t *testing.T) {
	cases := map[string]bool{
		"http://example.com":  true,
		"https://example.com": true,
		"HTTPS://EXAMPLE.COM": true,
		"example.com":         false,
		"notepad":             false,
	}
	for in, want := range cases {
		if got := IsURL(in); got != want {
			t.Errorf("IsURL(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestDeduplicatePrefersFirst(t *testing.T) {
	in := []AppItem{
		{Name: "From Start Menu", Target: `C:\a\app.exe`, Source: "start-menu-user"},
		{Name: "From Path", Target: `C:\a\app.exe`, Source: "path"},
	}
	got := deduplicate(in)

	if len(got) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(got))
	}
	if got[0].Source != "start-menu-user" {
		t.Errorf("kept source = %q, want the Start Menu entry to win", got[0].Source)
	}
}

func TestDeduplicateIsCaseInsensitive(t *testing.T) {
	in := []AppItem{
		{Target: `C:\a\App.exe`, Name: "First"},
		{Target: `C:\a\app.exe`, Name: "Second"},
	}
	if got := deduplicate(in); len(got) != 1 {
		t.Errorf("expected case-insensitive dedup, got %d entries", len(got))
	}
}