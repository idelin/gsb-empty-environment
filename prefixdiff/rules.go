package main

import (
	"bufio"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// Rule is a single allow/deny prefix rule in one rule set.
type Rule struct {
	Allow  bool
	Prefix string
	Depth  int // prefix length in runes
}

// Config holds the parsed problem instance.
type Config struct {
	Alphabet []rune
	MaxDepth int
	DefAllow bool // decision when no rule matches (default: deny)
	Old      []Rule
	New      []Rule
}

// decide resolves the permission for path s under rules:
// the deepest matching prefix wins; at equal depth deny wins;
// if nothing matches, the configured default applies.
func decide(rules []Rule, s string, defAllow bool) bool {
	best := -1
	allow := defAllow
	for _, r := range rules {
		if !strings.HasPrefix(s, r.Prefix) {
			continue
		}
		switch {
		case r.Depth > best:
			best = r.Depth
			allow = r.Allow
		case r.Depth == best:
			allow = allow && r.Allow // deny wins ties
		}
	}
	return allow
}

// parseConfig reads the rule file format:
//
//	alphabet abc            # each rune is one symbol (required, once)
//	maxdepth 4              # paths are strings of exactly this length (>= 1)
//	default allow|deny      # optional, default deny
//	old allow aab           # rule: <old|new> <allow|deny> <prefix>
//	new deny -              # "-" means the empty (root) prefix
//
// Blank lines and text after '#' are ignored.
func parseConfig(r io.Reader) (*Config, error) {
	cfg := &Config{}
	haveAlphabet, haveDepth := false, false
	alphaSet := map[rune]bool{}

	sc := bufio.NewScanner(r)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := sc.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		errf := func(format string, args ...any) error {
			return fmt.Errorf("line %d: %s", lineNo, fmt.Sprintf(format, args...))
		}
		switch fields[0] {
		case "alphabet":
			if len(fields) != 2 {
				return nil, errf("alphabet expects exactly one token of symbols")
			}
			if haveAlphabet {
				return nil, errf("duplicate alphabet")
			}
			for _, ch := range fields[1] {
				if alphaSet[ch] {
					return nil, errf("alphabet has duplicate symbol %q", ch)
				}
				alphaSet[ch] = true
				cfg.Alphabet = append(cfg.Alphabet, ch)
			}
			if len(cfg.Alphabet) == 0 {
				return nil, errf("alphabet must not be empty")
			}
			haveAlphabet = true
		case "maxdepth":
			if len(fields) != 2 {
				return nil, errf("maxdepth expects one integer")
			}
			if haveDepth {
				return nil, errf("duplicate maxdepth")
			}
			var d int
			if _, err := fmt.Sscanf(fields[1], "%d", &d); err != nil || d < 1 {
				return nil, errf("maxdepth must be an integer >= 1")
			}
			cfg.MaxDepth = d
			haveDepth = true
		case "default":
			if len(fields) != 2 {
				return nil, errf("default expects allow|deny")
			}
			switch fields[1] {
			case "allow":
				cfg.DefAllow = true
			case "deny":
				cfg.DefAllow = false
			default:
				return nil, errf("unknown default action %q", fields[1])
			}
		case "old", "new":
			if len(fields) != 3 {
				return nil, errf("rule expects: %s <allow|deny> <prefix>", fields[0])
			}
			var allow bool
			switch fields[1] {
			case "allow":
				allow = true
			case "deny":
				allow = false
			default:
				return nil, errf("unknown action %q", fields[1])
			}
			prefix := fields[2]
			if prefix == "-" {
				prefix = ""
			}
			if !haveAlphabet {
				return nil, errf("rule before alphabet declaration")
			}
			for _, ch := range prefix {
				if !alphaSet[ch] {
					return nil, errf("prefix %q uses symbol %q outside the alphabet", fields[2], ch)
				}
			}
			rule := Rule{Allow: allow, Prefix: prefix, Depth: utf8.RuneCountInString(prefix)}
			if fields[0] == "old" {
				cfg.Old = append(cfg.Old, rule)
			} else {
				cfg.New = append(cfg.New, rule)
			}
		default:
			return nil, errf("unknown directive %q", fields[0])
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if !haveAlphabet {
		return nil, fmt.Errorf("missing alphabet declaration")
	}
	if !haveDepth {
		return nil, fmt.Errorf("missing maxdepth declaration")
	}
	for _, set := range []struct {
		name  string
		rules []Rule
	}{{"old", cfg.Old}, {"new", cfg.New}} {
		for _, r := range set.rules {
			if r.Depth > cfg.MaxDepth {
				return nil, fmt.Errorf("%s rule prefix %q is deeper than maxdepth and can never match", set.name, displayPrefix(r.Prefix))
			}
		}
	}
	return cfg, nil
}
