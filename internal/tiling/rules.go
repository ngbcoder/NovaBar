package tiling

import (
	"regexp"
	"strings"
	"sync"

	"github.com/ngbcoder/NovaBar/internal/config"
)

// Action is what a matching rule does to a window.
type Action string

const (
	// ActionTile puts the window into the layout. This is the default.
	ActionTile Action = "tile"
	// ActionFloat leaves the window alone and positions it over the work area.
	ActionFloat Action = "float"
	// ActionIgnore removes the window from tracking and layout entirely.
	ActionIgnore Action = "ignore"
)

// Decision is the outcome of evaluating the rule set against one window.
type Decision struct {
	Action Action
	// RuleIndex is the index in the configured rule slice that matched, or -1 when
	// no rule matched and the default action applied.
	RuleIndex int
	// RuleDescription echoes the matching rule so logs explain the outcome.
	RuleDescription string
}

// compiledRule is a RuleConfig with its title pattern precompiled.
type compiledRule struct {
	cfg     config.RuleConfig
	pattern *regexp.Regexp
	// patternErr records a failed compile so it is reported once, at load, rather
	// than silently never matching.
	patternErr error
}

// RuleSet evaluates window-matching rules from config.
//
// Rules are evaluated in order; the first match wins. With no match the default action
// applies. A rule whose title fails to compile is skipped with a logged warning rather
// than aborting the whole set, so one bad regex cannot leave the daemon untileable.
type RuleSet struct {
	mu    sync.RWMutex
	rules []compiledRule
}

// NewRuleSet compiles a rule slice from config.
//
// A rule whose title regex fails to compile is dropped from the set and reported by
// Errors, so one bad pattern cannot leave the whole daemon unable to tile.
func NewRuleSet(rules []config.RuleConfig) *RuleSet {
	rs := &RuleSet{rules: make([]compiledRule, 0, len(rules))}
	for _, r := range rules {
		c := compiledRule{cfg: r}
		if r.Title != "" {
			c.pattern, c.patternErr = regexp.Compile(r.Title)
		}
		rs.rules = append(rs.rules, c)
	}
	return rs
}

// Errors returns compile failures encountered while building the set, for logging at
// load time.
func (rs *RuleSet) Errors() []string {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	errs := make([]string, 0)
	for _, r := range rs.rules {
		if r.patternErr != nil {
			errs = append(errs, r.cfg.Title)
		}
	}
	return errs
}

// WindowAttributes are the identity fields a rule can match on.
type WindowAttributes struct {
	// ProcName is the lowercased executable name without extension, e.g. "notepad".
	ProcName string
	// Class is the window class name, matched case-insensitively.
	Class string
	// Title is the window title, matched against the rule's regex.
	Title string
}

// normalizeProc reduces a process name to the canonical matching form: lowercase with
// no extension.
//
// Order matters: TrimSuffix is case-sensitive, so lowercasing must come first or
// "Taskmgr.EXE" would keep its extension and fail to match a rule written as
// "taskmgr".
func normalizeProc(name string) string {
	return strings.ToLower(strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".exe"))
}

// Evaluate returns the action for a window.
//
// A rule matches only when *every* field it specifies matches, so a rule with both
// `process` and `class` means "this process AND this class". OR semantics would make
// the default explorer rule float every Explorer window rather than just its status
// window, and would make rule ordering impossible to reason about.
//
// Matching is case-insensitive for process and class, and the ".exe" extension is
// optional on both sides. Title uses the rule's regex against the whole title.
func (rs *RuleSet) Evaluate(attrs WindowAttributes) Decision {
	rs.mu.RLock()
	defer rs.mu.RUnlock()

	proc := normalizeProc(attrs.ProcName)
	class := strings.ToLower(strings.TrimSpace(attrs.Class))

	for i, r := range rs.rules {
		if r.cfg.Process != "" && normalizeProc(r.cfg.Process) != proc {
			continue
		}
		if r.cfg.Class != "" && strings.ToLower(r.cfg.Class) != class {
			continue
		}
		if r.pattern != nil && !r.pattern.MatchString(attrs.Title) {
			continue
		}

		action := Action(strings.ToLower(strings.TrimSpace(r.cfg.Action)))
		if action == "" {
			action = ActionTile
		}
		return Decision{
			Action:          action,
			RuleIndex:       i,
			RuleDescription: describeRule(r.cfg),
		}
	}

	return Decision{Action: ActionTile, RuleIndex: -1}
}

func describeRule(r config.RuleConfig) string {
	parts := make([]string, 0, 3)
	if r.Process != "" {
		parts = append(parts, "process="+r.Process)
	}
	if r.Class != "" {
		parts = append(parts, "class="+r.Class)
	}
	if r.Title != "" {
		parts = append(parts, "title="+r.Title)
	}
	return strings.Join(parts, " ")
}

// IsDefault reports whether the app is one NovaBar should treat specially regardless
// of config, so that built-in guarantees hold even with an empty rule list.
func IsDefaultFloating(procName string) bool {
	switch normalizeProc(procName) {
	case "taskmgr", "mmc", "regedit":
		return true
	}
	return false
}