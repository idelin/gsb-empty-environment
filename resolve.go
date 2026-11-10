package main

import (
	"fmt"
	"strings"
)

// Hop is one node of a resolved connection chain.
type Hop struct {
	Alias     string // name as written on the command line or in ProxyJump
	HostName  string // effective HostName (defaults to the alias)
	Port      string // effective Port (defaults to "22")
	User      string // effective User, may be empty
	ViaJump   bool   // true if reached through a ProxyJump directive
	Forbidden bool   // set when HostName:Port is in the forbidden set
}

func (h Hop) Endpoint() string {
	return strings.ToLower(h.HostName) + ":" + h.Port
}

// JumpLoopError reports a ProxyJump resolution loop (a -> b -> a). It is
// distinct from an IncludeCycleError, which is a static config error.
type JumpLoopError struct {
	Chain []string
}

func (e *JumpLoopError) Error() string {
	return "proxy jump loop: " + strings.Join(e.Chain, " -> ")
}

// Chain is the ordered list of hops from the target to the last jump host.
type Chain struct {
	Hops []Hop
}

// Resolve builds the full connection chain for alias. Every ProxyJump hop
// is re-evaluated as an alias against the config, recursively, so each
// hop picks up its own HostName/Port/ProxyJump. An explicit port in a
// ProxyJump hop (alias:port) overrides the hop's configured port.
func Resolve(cfg *Config, alias string) (*Chain, error) {
	c := &Chain{}
	err := resolveInto(cfg, alias, "", "", nil, c)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// resolveInto resolves one alias and appends its hops to chain. visiting
// holds the aliases on the current resolution path for loop detection.
func resolveInto(cfg *Config, alias, portOverride, userOverride string, visiting []string, chain *Chain) error {
	for _, prev := range visiting {
		if strings.EqualFold(prev, alias) {
			return &JumpLoopError{Chain: append(append([]string{}, visiting...), alias)}
		}
	}
	visiting = append(visiting, alias)

	host := alias
	if v, ok := cfg.Get(alias, "hostname"); ok {
		host = v
	}
	port := "22"
	if v, ok := cfg.Get(alias, "port"); ok {
		port = v
	}
	if portOverride != "" {
		port = portOverride
	}
	user, _ := cfg.Get(alias, "user")
	if userOverride != "" {
		user = userOverride
	}

	chain.Hops = append(chain.Hops, Hop{
		Alias:    alias,
		HostName: host,
		Port:     port,
		User:     user,
		ViaJump:  len(chain.Hops) > 0,
	})

	pj, ok := cfg.Get(alias, "proxyjump")
	if !ok || strings.EqualFold(pj, "none") || pj == "" {
		return nil
	}
	for _, spec := range strings.Split(pj, ",") {
		spec = strings.TrimSpace(spec)
		if spec == "" {
			continue
		}
		hopUser, hopAlias, hopPort := parseJumpSpec(spec)
		if err := resolveInto(cfg, hopAlias, hopPort, hopUser, visiting, chain); err != nil {
			return err
		}
	}
	return nil
}

// parseJumpSpec splits a ProxyJump hop of the form [user@]host[:port].
// IPv6 literals in brackets are tolerated.
func parseJumpSpec(spec string) (user, host, port string) {
	if i := strings.LastIndex(spec, "@"); i >= 0 {
		user, spec = spec[:i], spec[i+1:]
	}
	if strings.HasPrefix(spec, "[") {
		if end := strings.Index(spec, "]"); end >= 0 {
			host = spec[1:end]
			if rest := spec[end+1:]; strings.HasPrefix(rest, ":") {
				port = rest[1:]
			}
			return user, host, port
		}
	}
	if i := strings.LastIndex(spec, ":"); i >= 0 && !strings.Contains(spec[i+1:], ":") {
		return user, spec[:i], spec[i+1:]
	}
	return user, spec, ""
}

// MarkForbidden flags every hop whose resolved host:port endpoint appears
// in the forbidden set, and reports whether any hop was flagged.
func (c *Chain) MarkForbidden(forbidden map[string]bool) bool {
	violation := false
	for i := range c.Hops {
		if forbidden[c.Hops[i].Endpoint()] {
			c.Hops[i].Forbidden = true
			violation = true
		}
	}
	return violation
}

// ParseForbiddenSet parses comma-separated host:port entries.
func ParseForbiddenSet(s string) (map[string]bool, error) {
	set := map[string]bool{}
	for _, e := range strings.Split(s, ",") {
		e = strings.TrimSpace(strings.ToLower(e))
		if e == "" {
			continue
		}
		if !strings.Contains(e, ":") {
			return nil, fmt.Errorf("forbidden entry %q must be host:port", e)
		}
		set[e] = true
	}
	return set, nil
}
