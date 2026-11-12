package taskpkg

import (
	"fmt"
	"sort"
)

// Constraint is the admission policy attached to a dependency edge. A candidate
// is admissible through the edge only when every field accepts it.
type Constraint struct {
	// TrustedSource is the only source identity allowed on this edge.
	TrustedSource SourceID
	// MinVersion and MaxVersion form an INCLUSIVE version interval.
	MinVersion Version
	MaxVersion Version
	// AllowedSigners lists signers accepted by the edge. An empty set rejects
	// every signed candidate.
	AllowedSigners []SignerID
}

// Validate reports malformed constraints.
func (c Constraint) Validate() error {
	if c.TrustedSource == "" {
		return fmt.Errorf("taskpkg: constraint must declare a trusted source")
	}
	if c.MinVersion.Compare(c.MaxVersion) > 0 {
		return fmt.Errorf("taskpkg: min version %s exceeds max version %s",
			c.MinVersion, c.MaxVersion)
	}
	if len(c.AllowedSigners) == 0 {
		return fmt.Errorf("taskpkg: constraint for source %q declares no allowed signers",
			c.TrustedSource)
	}
	return nil
}

func (c Constraint) signerAllowed(s SignerID) bool {
	for _, allowed := range c.AllowedSigners {
		if allowed == s {
			return true
		}
	}
	return false
}

// accepts reports whether candidate c satisfies the static part of the edge
// policy. Proof verification is performed separately through the Verifier.
func (c Constraint) accepts(cand Candidate) bool {
	if cand.Node.Source != c.TrustedSource {
		return false
	}
	if !cand.Version.InClosedInterval(c.MinVersion, c.MaxVersion) {
		return false
	}
	return c.signerAllowed(cand.Proof.Signer)
}

// Edge is a directed dependency from From to To. The constraint pins the
// expected source of To, so two edges reaching same-named packages on
// different sources point at distinct NodeIDs and never collapse.
type Edge struct {
	From       NodeID
	To         NodeID
	Constraint Constraint
	// When gates the edge; nil means the edge is always active.
	When Condition
}

func (e Edge) active(cfg PlatformConfig) bool {
	if e.When == nil {
		return true
	}
	return e.When.Eval(cfg)
}

func (e Edge) validate() error {
	if e.From.Path == "" || e.To.Path == "" {
		return fmt.Errorf("taskpkg: edge endpoints must have non-empty paths")
	}
	if err := e.Constraint.Validate(); err != nil {
		return err
	}
	if e.Constraint.TrustedSource != e.To.Source {
		return fmt.Errorf("taskpkg: edge %s -> %s pins source %q but target source is %q",
			e.From, e.To, e.Constraint.TrustedSource, e.To.Source)
	}
	if e.When != nil {
		if err := e.When.validate(); err != nil {
			return err
		}
	}
	return nil
}

// Graph is the static dependency graph together with its edge policies.
type Graph struct {
	Root  NodeID
	Edges []Edge
}

// Validate checks every edge and root references.
func (g Graph) Validate() error {
	if g.Root.Path == "" {
		return fmt.Errorf("taskpkg: graph root must have a non-empty path")
	}
	seen := make(map[[2]NodeID]bool)
	for _, e := range g.Edges {
		if err := e.validate(); err != nil {
			return err
		}
		key := [2]NodeID{e.From, e.To}
		if seen[key] {
			return fmt.Errorf("taskpkg: duplicate edge %s -> %s", e.From, e.To)
		}
		seen[key] = true
	}
	return nil
}

// activeEdges returns the edges leaving node that are enabled under cfg, in a
// deterministic order.
func (g Graph) activeEdges(node NodeID, cfg PlatformConfig) []Edge {
	var out []Edge
	for _, e := range g.Edges {
		if e.From == node && e.active(cfg) {
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].To.Source != out[j].To.Source {
			return out[i].To.Source < out[j].To.Source
		}
		return out[i].To.Path < out[j].To.Path
	})
	return out
}
