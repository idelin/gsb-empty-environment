package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// KV is a single keyword/value pair inside a Host block.
type KV struct {
	Key   string // lower-cased keyword, e.g. "hostname"
	Value string
	Line  int
}

// HostBlock is an ordered `Host <patterns>` section. A block with the
// single pattern "*" represents global options (lines before any Host).
type HostBlock struct {
	Patterns []string // raw patterns, "!" prefix means negation
	KVs      []KV
	File     string
	Line     int
}

// Config is the flattened, include-expanded list of Host blocks in file
// order. First-value-wins is applied at lookup time, not parse time.
type Config struct {
	Blocks []HostBlock
}

// IncludeCycleError reports a static Include cycle (a -> b -> a). It is
// distinct from a ProxyJump loop, which is a runtime resolution error.
type IncludeCycleError struct {
	Chain []string
}

func (e *IncludeCycleError) Error() string {
	return "config include cycle: " + strings.Join(e.Chain, " -> ")
}

// ParseFile parses an ssh_config file, expanding static Include directives
// in place. Relative include paths resolve against the including file's
// directory. An include cycle returns *IncludeCycleError.
func ParseFile(path string) (*Config, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	cfg := &Config{}
	if err := parseInto(cfg, abs, nil); err != nil {
		return nil, err
	}
	return cfg, nil
}

func parseInto(cfg *Config, path string, stack []string) error {
	for _, ancestor := range stack {
		if ancestor == path {
			return &IncludeCycleError{Chain: append(append([]string{}, stack...), path)}
		}
	}
	stack = append(stack, path)

	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("include %s: %w", path, err)
	}
	defer f.Close()

	current := HostBlock{Patterns: []string{"*"}, File: path, Line: 0}
	flush := func() {
		if len(current.KVs) > 0 || current.Line > 0 {
			cfg.Blocks = append(cfg.Blocks, current)
		}
	}

	sc := bufio.NewScanner(f)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, rest := splitKeyword(line)
		lkey := strings.ToLower(key)
		switch {
		case lkey == "host":
			flush()
			current = HostBlock{
				Patterns: strings.Fields(rest),
				File:     path,
				Line:     lineNo,
			}
		case lkey == "include":
			for _, inc := range strings.Fields(rest) {
				target := inc
				if !filepath.IsAbs(target) {
					target = filepath.Join(filepath.Dir(path), target)
				}
				target = filepath.Clean(target)
				// Flush the block accumulated so far so that included
				// blocks are spliced at the exact Include position.
				flush()
				current = HostBlock{Patterns: []string{"*"}, File: path, Line: 0}
				if err := parseInto(cfg, target, stack); err != nil {
					return err
				}
			}
		default:
			current.KVs = append(current.KVs, KV{Key: lkey, Value: rest, Line: lineNo})
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	flush()
	return nil
}

func splitKeyword(line string) (key, rest string) {
	if i := strings.IndexAny(line, " \t="); i >= 0 {
		return line[:i], strings.TrimSpace(strings.TrimLeft(line[i+1:], "= \t"))
	}
	return line, ""
}

// matchPattern implements ssh wildcard matching: '*' matches any sequence
// (including empty), '?' matches exactly one byte. Matching is
// case-insensitive, mirroring ssh lowercasing the host before matching.
func matchPattern(pattern, host string) bool {
	pattern, host = strings.ToLower(pattern), strings.ToLower(host)
	var match func(p, h string) bool
	match = func(p, h string) bool {
		for len(p) > 0 {
			switch p[0] {
			case '*':
				for i := 0; i <= len(h); i++ {
					if match(p[1:], h[i:]) {
						return true
					}
				}
				return false
			case '?':
				if len(h) == 0 {
					return false
				}
				p, h = p[1:], h[1:]
			default:
				if len(h) == 0 || p[0] != h[0] {
					return false
				}
				p, h = p[1:], h[1:]
			}
		}
		return len(h) == 0
	}
	return match(pattern, host)
}

// matches reports whether the block applies to the alias: at least one
// positive pattern matches and no negated ("!") pattern matches.
func (b *HostBlock) matches(alias string) bool {
	matched := false
	for _, p := range b.Patterns {
		if strings.HasPrefix(p, "!") {
			if matchPattern(p[1:], alias) {
				return false
			}
		} else if matchPattern(p, alias) {
			matched = true
		}
	}
	return matched
}

// Get returns the effective value for key under first-value-wins: the
// first block (in file order) that both matches the alias and sets the
// key provides the value; later blocks cannot override it.
func (c *Config) Get(alias, key string) (string, bool) {
	for i := range c.Blocks {
		b := &c.Blocks[i]
		if !b.matches(alias) {
			continue
		}
		for _, kv := range b.KVs {
			if kv.Key == key {
				return kv.Value, true
			}
		}
	}
	return "", false
}
