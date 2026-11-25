package devrule

// Grants reports whether the rule sequence grants the given permissions to
// one device. Invalid rule sequences grant nothing.
func Grants(rules []Rule, kind Kind, major, minor int, perms Perm) bool {
	d, err := Compile(rules)
	if err != nil {
		return false
	}
	return d.Lookup(kind, major, minor)&perms == perms
}

// MinimizeChain greedily removes rules while the property still holds,
// returning a 1-minimal subsequence (in original order) that preserves it.
// It is a witness extractor: given a property such as "grants host disk
// write", the result is a minimal rule chain that still exhibits it.
func MinimizeChain(rules []Rule, property func([]Rule) bool) []Rule {
	cur := append([]Rule(nil), rules...)
	for {
		removed := false
		for i := 0; i < len(cur); {
			trial := make([]Rule, 0, len(cur)-1)
			trial = append(trial, cur[:i]...)
			trial = append(trial, cur[i+1:]...)
			if property(trial) {
				cur = trial
				removed = true
			} else {
				i++
			}
		}
		if !removed {
			return cur
		}
	}
}
