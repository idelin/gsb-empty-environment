package ormap

import "fmt"

// Reference computes the outcome directly from a set of operations,
// with no replica machinery: an assignment survives iff no remove in
// the set carries its dot. It serves as the ground truth that the
// replicated implementation is checked against.
func Reference(ops []Op) (map[string][]Version, error) {
	adds := make(map[string]map[Dot]Version)
	removed := make(map[string]map[Dot]bool)
	for _, op := range ops {
		switch op.Kind {
		case OpAdd:
			byDot, ok := adds[op.Key]
			if !ok {
				byDot = make(map[Dot]Version)
				adds[op.Key] = byDot
			}
			if existing, ok := byDot[op.Version.Dot]; ok && existing != op.Version {
				return nil, &DotConflictError{Dot: op.Version.Dot, Existing: existing, Incoming: op.Version}
			}
			byDot[op.Version.Dot] = op.Version
		case OpRemove:
			set, ok := removed[op.Key]
			if !ok {
				set = make(map[Dot]bool)
				removed[op.Key] = set
			}
			for _, d := range op.Dots {
				set[d] = true
			}
		default:
			return nil, fmt.Errorf("ormap: unknown op kind %d", op.Kind)
		}
	}
	out := make(map[string][]Version, len(adds))
	for key, byDot := range adds {
		dead := removed[key]
		var live []Version
		for d, v := range byDot {
			if !dead[d] {
				live = append(live, v)
			}
		}
		sortVersions(live)
		out[key] = live
	}
	return out, nil
}
