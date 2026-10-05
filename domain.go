package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

type Config struct {
	Subjects []string   `json:"subjects"`
	Actions  []string   `json:"actions"`
	Segments []string   `json:"segments"`
	MaxDepth int        `json:"maxDepth"`
	Rules    []RuleSpec `json:"rules"`
}

type RuleSpec struct {
	Effect   string   `json:"effect"`
	Subjects []string `json:"subjects"`
	Actions  []string `json:"actions"`
	Path     string   `json:"path"`
}

func (c *Config) validate() error {
	if c.MaxDepth < 0 || c.MaxDepth > 16 {
		return fmt.Errorf("maxDepth must be between 0 and 16, got %d", c.MaxDepth)
	}
	if err := checkDup("subjects", c.Subjects); err != nil {
		return err
	}
	if err := checkDup("actions", c.Actions); err != nil {
		return err
	}
	if err := checkDup("segments", c.Segments); err != nil {
		return err
	}
	for i := range c.Rules {
		switch c.Rules[i].Effect {
		case "":
			c.Rules[i].Effect = "allow"
		case "allow", "deny":
		default:
			return fmt.Errorf("rule %d: invalid effect %q", i, c.Rules[i].Effect)
		}
	}
	return nil
}

func checkDup(what string, names []string) error {
	seen := map[string]bool{}
	for _, n := range names {
		if n == "" {
			return fmt.Errorf("%s: empty name", what)
		}
		if seen[n] {
			return fmt.Errorf("%s: duplicate name %q", what, n)
		}
		seen[n] = true
	}
	return nil
}

type Request struct {
	Subject string
	Action  string
	Path    []string
}

func (r Request) String() string {
	return fmt.Sprintf("subject=%s action=%s path=%s", r.Subject, r.Action, "/"+strings.Join(r.Path, "/"))
}

func (r Request) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]string{
		"subject": r.Subject,
		"action":  r.Action,
		"path":    "/" + strings.Join(r.Path, "/"),
	})
}

const maxRequests = 5000000

func enumerateRequests(cfg *Config) ([]Request, error) {
	var paths [][]string
	var gen func(prefix []string, depth int)
	gen = func(prefix []string, depth int) {
		cp := make([]string, len(prefix))
		copy(cp, prefix)
		paths = append(paths, cp)
		if depth == cfg.MaxDepth {
			return
		}
		for _, s := range cfg.Segments {
			gen(append(prefix, s), depth+1)
		}
	}
	gen(nil, 0)

	total := len(paths) * len(cfg.Subjects) * len(cfg.Actions)
	if total > maxRequests {
		return nil, fmt.Errorf("request space too large: %d requests (limit %d); shrink domains or maxDepth", total, maxRequests)
	}
	reqs := make([]Request, 0, total)
	for _, subj := range cfg.Subjects {
		for _, act := range cfg.Actions {
			for _, p := range paths {
				reqs = append(reqs, Request{Subject: subj, Action: act, Path: p})
			}
		}
	}
	return reqs, nil
}
