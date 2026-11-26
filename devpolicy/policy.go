package devpolicy

import (
	"errors"
	"fmt"
	"sort"
)

// PolicyError describes a structurally invalid rule or policy.
type PolicyError struct{ msg string }

func (e *PolicyError) Error() string { return e.msg }

func invalidf(format string, args ...any) error {
	return &PolicyError{msg: fmt.Sprintf(format, args...)}
}

// AsPolicyError extracts a *PolicyError from err when present.
func AsPolicyError(err error) (*PolicyError, bool) {
	var pe *PolicyError
	return pe, errors.As(err, &pe)
}

// Policy is an ordered collection of rules spanning one or more nested
// cgroup layers. It is safe to compile a Policy concurrently after building.
type Policy struct {
	Rules []Rule
}

// Add appends a rule and returns the policy for chaining.
func (p *Policy) Add(r Rule) *Policy {
	p.Rules = append(p.Rules, r)
	return p
}

// orderedRules returns rules sorted for deterministic replay: layer first,
// then the explicit Order, preserving insertion order as the final tie-break.
func orderedRules(in []Rule) []Rule {
	out := append([]Rule(nil), in...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Layer != out[j].Layer {
			return out[i].Layer < out[j].Layer
		}
		if out[i].Order != out[j].Order {
			return out[i].Order < out[j].Order
		}
		return false
	})
	return out
}

// Compile builds the symbolic decision diagrams. It validates every rule,
// requires contiguous layers starting at 0, replays each layer in order and
// finally intersects the layers so ancestor denials cannot be restored.
func (p *Policy) Compile() (*CompiledPolicy, error) {
	return p.CompileDomain(MaxDeviceNo)
}

// CompileDomain is Compile with an explicit domain upper bound. It exists to
// support small-domain exhaustive testing; production callers use Compile.
func (p *Policy) CompileDomain(domainMax uint16) (*CompiledPolicy, error) {
	if domainMax > MaxDeviceNo {
		return nil, invalidf("domain max %d exceeds %d", domainMax, MaxDeviceNo)
	}
	rules := orderedRules(p.Rules)
	if err := validateRules(rules); err != nil {
		return nil, err
	}

	compiled := &CompiledPolicy{Rules: rules, DomainMax: domainMax}
	maxLayer := 0
	for _, r := range rules {
		if r.Layer > maxLayer {
			maxLayer = r.Layer
		}
	}

	for idx, t := range orderedTypes() {
		// Layer 0 starts default-deny; each subsequent layer is intersected,
		// which removes every bit the ancestor did not grant (or explicitly denied).
		effective := fullPartition(0)
		effective[0].Mask = MaskAll // identity for the first AND
		for layer := 0; layer <= maxLayer; layer++ {
			layerRules := filterLayer(rules, layer)
			diagram := compileLayer(t, layerRules, domainMax)
			if layer == 0 {
				compiled.Layers[idx] = diagram
			}
			effective = intersectPartitionsDomain(effective, diagram.Partition, domainMax)
		}
		compiled.Effective[idx] = &Diagram{Type: t, Partition: compact(effective)}
	}
	return compiled, nil
}

// validateRules checks rule legality. Sparse layers are intentional: an empty
// layer contributes default-deny through the layer intersection.
func validateRules(rules []Rule) error {
	maxLayer := -1
	for _, r := range rules {
		if err := r.validate(); err != nil {
			return err
		}
		if r.Layer > maxLayer {
			maxLayer = r.Layer
		}
	}
	// Sparse layers are legal: an intermediate cgroup with no device rule simply
	// contributes the default-deny mask, which the intersection already models.
	// We only require that rules form a non-empty, non-negative nesting rooted at
	// layer 0 when any rule exists.
	if len(rules) > 0 && maxLayer < 0 {
		return invalidf("policy contains no usable layer")
	}
	return nil
}

// filterLayer returns the rules belonging to one layer, retaining order.
func filterLayer(rules []Rule, layer int) []Rule {
	var out []Rule
	for _, r := range rules {
		if r.Layer == layer {
			out = append(out, r)
		}
	}
	return out
}

// TraceEvent is one witnessed rule application while replaying the policy
// for a single counter-example device.
type TraceEvent struct {
	Rule Rule
	// Before/After are the running masks immediately around this rule.
	Before uint8
	After  uint8
	// Matched reports whether the device falls inside the rule rectangle.
	Matched bool
}

// Witness is the full rule-chain evidence explaining one device's decision.
type Witness struct {
	Type  DevType
	Major uint16
	Minor uint16
	// LayerMasks is the per-layer final mask before intersection.
	LayerMasks []uint8
	// Effective is the intersected final mask.
	Effective uint8
	// Trace holds, per layer, the ordered events that mention the device type.
	Trace [][]TraceEvent
}

// Trace replays every layer's rules for one device and records the rule
// chain. It reuses the exact single-layer accumulator semantics of the
// compiler without enumerating any other device.
func (c *CompiledPolicy) Trace(t DevType, major, minor uint16) Witness {
	w := Witness{Type: t, Major: major, Minor: minor}
	depth := c.layerCount()
	for layer := 0; layer < depth; layer++ {
		mask := uint8(0)
		var events []TraceEvent
		for _, r := range c.Rules {
			if r.Layer != layer || (r.Type != TypeAll && r.Type != t) {
				continue
			}
			matched := major >= r.MajorMin && major <= r.MajorMax &&
				minor >= r.MinorMin && minor <= r.MinorMax
			before := mask
			if matched {
				mask = foldMask(mask, r.Effect, r.Perm)
			}
			events = append(events, TraceEvent{Rule: r, Before: before, After: mask, Matched: matched})
		}
		w.LayerMasks = append(w.LayerMasks, mask)
		w.Trace = append(w.Trace, events)
	}
	eff := uint8(MaskAll)
	for _, m := range w.LayerMasks {
		eff &= m
	}
	w.Effective = eff
	return w
}

// MatchedRules returns only the trace events whose rectangles contain the
// witnessed device, in replay order across all layers.
func (w Witness) MatchedRules() []TraceEvent {
	var out []TraceEvent
	for _, events := range w.Trace {
		for _, ev := range events {
			if ev.Matched {
				out = append(out, ev)
			}
		}
	}
	return out
}
