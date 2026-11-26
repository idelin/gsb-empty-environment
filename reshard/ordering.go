package reshard

// compareLabels orders primitive sequences lexicographically. A strict prefix
// is the smaller sequence.
func compareLabels(a, b []Op) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if c := compareOp(a[i], b[i]); c != 0 {
			return c
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	default:
		return 0
	}
}

// compareOp mirrors the dictionary order of primitive names, then argument
// order A, D1, D2 for identical primitive names.
func compareOp(x, y Op) int {
	if x.Type != y.Type {
		if x.Type < y.Type {
			return -1
		}
		return 1
	}
	for _, pair := range [][2]int{
		{x.A, y.A},
		{x.B, y.B},
		{x.D1, y.D1},
		{x.D2, y.D2},
	} {
		if pair[0] != pair[1] {
			if pair[0] < pair[1] {
				return -1
			}
			return 1
		}
	}
	return 0
}
