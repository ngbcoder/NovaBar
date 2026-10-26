package launcher

import (
	"path/filepath"
	"strings"
)

// Search ranks index entries against a query.
//
// Ranking, best first:
//
//  1. exact name match
//  2. name prefix match
//  3. acronym match (initials: "vscode" for "Visual Studio Code")
//  4. contiguous substring
//  5. scattered subsequence (letters in order, not adjacent)
//
// Ties break on name length then alphabetically, so results are stable run to run.
func (idx *Indexer) Search(query string, limit int) []AppItem {
	idx.mu.RLock()
	items := idx.items
	idx.mu.RUnlock()

	if limit <= 0 {
		limit = 10
	}

	q := strings.ToLower(strings.TrimSpace(query))

	ranked := make([]rankedItem, 0, len(items))
	for _, it := range items {
		score, ok := scoreItem(it, q)
		if !ok {
			continue
		}
		ranked = append(ranked, rankedItem{item: it, score: score})
	}

	sortRanked(ranked)

	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	out := make([]AppItem, 0, len(ranked))
	for _, r := range ranked {
		out = append(out, r.item)
	}
	return out
}

// rankedItem pairs an item with its relevance score.
type rankedItem struct {
	item  AppItem
	score int
}

// Score tiers. Lower is better.
const (
	scoreExact       = 0
	scorePrefix      = 10
	scoreAcronym     = 20
	scoreSubstring   = 30
	scoreSubsequence = 40
	scoreLastWord    = 35
)

func scoreItem(it AppItem, q string) (int, bool) {
	if q == "" {
		return 100, true
	}

	name := strings.ToLower(it.Name)
	words := strings.Fields(name)

	switch {
	case name == q:
		return scoreExact, true
	case strings.HasPrefix(name, q):
		return scorePrefix + len(name) - len(q), true
	}

	// Match against the last word so "code" finds "Visual Studio Code".
	if len(words) > 1 {
		last := words[len(words)-1]
		switch {
		case last == q:
			return scoreLastWord, true
		case strings.HasPrefix(last, q):
			return scoreLastWord + 5, true
		}
	}

	if acr := acronym(words); acr != "" && strings.HasPrefix(acr, q) {
		return scoreAcronym, true
	}

	if idx := strings.Index(name, q); idx >= 0 {
		return scoreSubstring + idx, true
	}

	if strings.Index(acronym(words), q) >= 0 {
		return scoreSubsequence, true
	}

	if isSubsequence(q, name) {
		return scoreSubsequence, true
	}

	return 0, false
}

func acronym(words []string) string {
	var b strings.Builder
	for _, w := range words {
		if w != "" {
			b.WriteByte(w[0])
		}
	}
	return b.String()
}

// isSubsequence reports whether every rune of q appears in s in order.
func isSubsequence(q, s string) bool {
	if q == "" {
		return true
	}
	qi := 0
	for i := 0; i < len(s) && qi < len(q); i++ {
		if s[i] == q[qi] {
			qi++
		}
	}
	return qi == len(q)
}

func sortRanked(s []rankedItem) {
	// Insertion sort: result sets are small (tens), and this avoids pulling in
	// sort.Slice's closure overhead on a path the launcher calls per keystroke.
	for i := 1; i < len(s); i++ {
		for j := i; j > 0; j-- {
			a, b := s[j-1], s[j]
			if better(b, a) {
				s[j-1], s[j] = b, a
				continue
			}
			break
		}
	}
}

func better(a, b rankedItem) bool {
	if a.score != b.score {
		return a.score < b.score
	}
	an, bn := strings.ToLower(a.item.Name), strings.ToLower(b.item.Name)
	if len(an) != len(bn) {
		return len(an) < len(bn)
	}
	if an != bn {
		return an < bn
	}
	return a.item.Target < b.item.Target
}

// DefaultCommands are always available in the launcher, even with an empty index.
var DefaultCommands = []AppItem{
	{Name: "Command Prompt", Target: "cmd.exe", Kind: KindCommand, Source: "builtin"},
	{Name: "PowerShell", Target: "powershell.exe", Kind: KindCommand, Source: "builtin"},
	{Name: "File Explorer", Target: "explorer.exe", Kind: KindCommand, Source: "builtin"},
	{Name: "Task Manager", Target: "taskmgr.exe", Kind: KindCommand, Source: "builtin"},
	{Name: "Settings", Target: "ms-settings:", Kind: KindCommand, Source: "builtin"},
	{Name: "Control Panel", Target: "control.exe", Kind: KindCommand, Source: "builtin"},
}

// IsShellMetacharacter reports whether s contains a character cmd.exe treats as a
// command separator or quote.
//
// This is the guard against command injection: an RPC caller supplying
// "calc.exe & calc.exe" to a `cmd /c start` target would otherwise run the second
// command.
func IsShellMetacharacter(s string) bool {
	const dangerous = "&|<>^`\n\r\"%"
	for _, r := range s {
		if strings.ContainsRune(dangerous, r) {
			return true
		}
	}
	// Also reject the percent-escape forms cmd expands.
	return strings.Contains(s, "%")
}

// ValidateTarget checks a launcher target before it reaches cmd.exe.
//
// Accepts: an existing .exe or .lnk under a known safe root, a bare executable name
// resolvable on PATH, a shell: URI, and the builtin UWP AppUserModelID form
// (package!app).
func ValidateTarget(target string) error {
	target = strings.TrimSpace(target)
	if target == "" {
		return errEmptyTarget
	}
	if len(target) > 260 {
		return errTargetTooLong
	}
	if IsShellMetacharacter(target) {
		return errMetacharacter
	}

	// shell: URIs are opened by the shell, not by cmd.exe.
	if strings.HasPrefix(strings.ToLower(target), "shell:") {
		return nil
	}
	// Any other protocol URI (ms-settings:, http:, mailto:) is shell-owned too.
	if isProtocolURI(target) {
		return nil
	}
	// UWP AppUserModelID: Company.Product!Application
	if strings.Contains(target, "!") {
		return nil
	}

	ext := strings.ToLower(filepath.Ext(target))
	if ext == ".exe" || ext == ".lnk" || ext == "" {
		return nil
	}
	return errUnsupportedExtension
}

// DefaultCommandsFor returns the built-in command entries for direct merging into an
// index.
func DefaultCommandsFor() []AppItem {
	out := make([]AppItem, len(DefaultCommands))
	copy(out, DefaultCommands)
	return out
}
