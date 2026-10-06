package rewrite

import "fmt"

// Replay verifies cycle evidence against cfg: it applies Entry from the
// initial state, records the reached state, applies Cycle, and requires
// the final state to equal the recorded one exactly — string, control
// state and resources. Because every step is re-validated (match
// position, control state, resource bounds), evidence that silently
// reuses one-time resources, or only repeats the string while resources
// differ, is rejected.
func Replay(cfg Config, entry, cycle []Step) error {
	if err := cfg.validate(); err != nil {
		return err
	}
	cur := cfg.Init
	apply := func(st Step) error {
		if st.Rule < 0 || st.Rule >= len(cfg.Rules) {
			return fmt.Errorf("rule index %d out of range", st.Rule)
		}
		ns, ok := cfg.applyAt(st.Rule, st.Position, cur)
		if !ok {
			r := cfg.Rules[st.Rule]
			return fmt.Errorf("cannot apply rule %d (%q) at position %d in state {str:%q ctl:%d res:%v}",
				st.Rule, r.Name, st.Position, cur.Str, cur.Ctl, cur.Resources)
		}
		cur = ns
		return nil
	}
	for i, st := range entry {
		if err := apply(st); err != nil {
			return fmt.Errorf("entry step %d: %v", i, err)
		}
	}
	if len(cycle) == 0 {
		return fmt.Errorf("empty cycle evidence")
	}
	start := cur
	for i, st := range cycle {
		if err := apply(st); err != nil {
			return fmt.Errorf("cycle step %d: %v", i, err)
		}
	}
	if !cur.equal(start) {
		return fmt.Errorf("cycle does not close: end state {str:%q ctl:%d res:%v} differs from start {str:%q ctl:%d res:%v}",
			cur.Str, cur.Ctl, cur.Resources, start.Str, start.Ctl, start.Resources)
	}
	return nil
}
