package rename

import "fmt"

// Simulate applies plan to a copy of initial (path -> file identity) and
// returns the resulting state. It fails if any step's source is missing or
// its target is occupied, i.e. if the plan itself is not executable.
func Simulate(initial map[string]string, plan *Plan) (map[string]string, error) {
	state := make(map[string]string, len(initial))
	for p, id := range initial {
		state[p] = id
	}
	for i, s := range plan.Steps {
		id, ok := state[s.From]
		if !ok {
			return nil, fmt.Errorf("step %d: source %q missing", i, s.From)
		}
		if _, busy := state[s.To]; busy {
			return nil, fmt.Errorf("step %d: target %q occupied", i, s.To)
		}
		delete(state, s.From)
		state[s.To] = id
	}
	return state, nil
}

// Verify independently cross-checks a plan: it simulates the plan against
// initial and confirms that every file's identity ends up exactly where
// moves say it should, and that no other file is created, lost, or moved.
func Verify(initial map[string]string, moves []Move, plan *Plan) error {
	final, err := Simulate(initial, plan)
	if err != nil {
		return fmt.Errorf("simulation: %v", err)
	}
	// Targets are interpreted against the initial directory: a file that
	// participates moves away from its initial path, and its target slot
	// is filled by whatever move lands there.
	froms := make(map[string]bool, len(moves))
	for _, m := range moves {
		if m.From != m.To {
			froms[m.From] = true
		}
	}
	want := make(map[string]string, len(initial))
	for p, id := range initial {
		if !froms[p] {
			want[p] = id
		}
	}
	for _, m := range moves {
		if m.From == m.To {
			continue
		}
		id, ok := initial[m.From]
		if !ok {
			return fmt.Errorf("move source %q not in initial state", m.From)
		}
		want[m.To] = id
	}
	if len(want) != len(final) {
		return fmt.Errorf("file count changed: want %d, got %d", len(want), len(final))
	}
	for p, id := range want {
		got, ok := final[p]
		if !ok {
			return fmt.Errorf("path %q (file %q) missing after plan", p, id)
		}
		if got != id {
			return fmt.Errorf("path %q holds file %q, want %q", p, got, id)
		}
	}
	return nil
}
