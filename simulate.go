package rename

import "fmt"

// Simulate independently applies ops to a virtual filesystem, modeled as
// a map from path to file identity, and returns the final state. It
// fails if any step's source is missing or its destination is occupied,
// so a broken plan cannot pass silently.
func Simulate(initial map[string]string, ops []Op) (map[string]string, error) {
	state := make(map[string]string, len(initial))
	for p, id := range initial {
		state[p] = id
	}
	for i, op := range ops {
		id, ok := state[op.Src]
		if !ok {
			return nil, fmt.Errorf("simulate: op %d (%s -> %s): source does not exist", i, op.Src, op.Dst)
		}
		if _, taken := state[op.Dst]; taken {
			return nil, fmt.Errorf("simulate: op %d (%s -> %s): destination already exists", i, op.Src, op.Dst)
		}
		delete(state, op.Src)
		state[op.Dst] = id
	}
	return state, nil
}

// Verify checks that executing ops on the initial state yields exactly
// the mapping requested by renames: every file's identity ends up at its
// requested target, and no other file is created, lost, or moved.
func Verify(initial map[string]string, ops []Op, renames []Rename) error {
	final, err := Simulate(initial, ops)
	if err != nil {
		return err
	}
	want := make(map[string]string, len(initial))
	for p, id := range initial {
		want[p] = id
	}
	// Remove every source first, then place each identity at its
	// target: a target may itself be another rename's source.
	srcs := map[string]bool{}
	for _, r := range renames {
		s, err := clean(r.Src)
		if err != nil {
			return err
		}
		srcs[s] = true
	}
	for p := range want {
		if srcs[p] {
			delete(want, p)
		}
	}
	for _, r := range renames {
		s, err := clean(r.Src)
		if err != nil {
			return err
		}
		d, err := clean(r.Dst)
		if err != nil {
			return err
		}
		id, ok := initial[s]
		if !ok {
			return fmt.Errorf("verify: source %q missing from initial state", s)
		}
		want[d] = id
	}
	if len(final) != len(want) {
		return fmt.Errorf("verify: file count changed: got %d, want %d", len(final), len(want))
	}
	for p, id := range want {
		if got, ok := final[p]; !ok || got != id {
			return fmt.Errorf("verify: path %q holds %q, want %q", p, final[p], id)
		}
	}
	return nil
}
