package tcheck

import "fmt"

// SafetyResult is the outcome of the authorization-before-danger check.
type SafetyResult struct {
	OK bool
	// Witness, when OK is false, is a shortest reachable execution that
	// enters a danger state from a non-authorized state. The last two
	// states are the offending transition.
	Witness Path
	// Entered is the danger state that was entered without authorization.
	Entered string
}

// CheckSafety verifies that on every reachable execution, a danger state
// is only ever entered from an authorized state (or is authorized itself).
// A danger initial state without authorization is an immediate violation.
// Unreachable states are ignored.
func (m *Machine) CheckSafety(dangerProp, authProp string) SafetyResult {
	reach := m.Reachable()

	violation := func(entry string) SafetyResult {
		return SafetyResult{OK: false, Entered: entry}
	}

	// Initial state: entering the system directly into danger.
	if m.Has(m.Initial, dangerProp) && !m.Has(m.Initial, authProp) {
		r := violation(m.Initial)
		r.Witness = Path{m.Initial}
		return r
	}

	// Scan reachable transitions in deterministic order.
	for _, s := range m.States() {
		if !reach[s] {
			continue
		}
		for _, t := range m.Successors(s) {
			if !m.Has(t, dangerProp) {
				continue
			}
			if m.Has(s, authProp) || m.Has(t, authProp) {
				continue // authorized at the moment of entry
			}
			r := violation(t)
			prefix := m.shortestPath(m.Initial, s, func(string) bool { return true })
			r.Witness = append(prefix, t)
			return r
		}
	}
	return SafetyResult{OK: true}
}

func (r SafetyResult) String() string {
	if r.OK {
		return "SAFETY OK: danger is only entered while authorized"
	}
	return fmt.Sprintf("SAFETY VIOLATED: %q entered without authorization\n  witness: %s",
		r.Entered, r.Witness)
}
