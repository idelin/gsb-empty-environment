package lockmgr

// LockMode is one of the five multi-granularity lock modes.
type LockMode uint8

const (
	// None is not a real lock mode; it is used internally for the zero value.
	None LockMode = iota
	// IS is the Intention Shared mode.
	IS
	// IX is the Intention Exclusive mode.
	IX
	// S is the Shared mode.
	S
	// SIX is the Shared + Intention Exclusive mode.
	SIX
	// X is the Exclusive mode.
	X
)

// String returns the conventional short name of the lock mode.
func (m LockMode) String() string {
	switch m {
	case IS:
		return "IS"
	case IX:
		return "IX"
	case S:
		return "S"
	case SIX:
		return "SIX"
	case X:
		return "X"
	default:
		return "NONE"
	}
}

// level orders modes by strength. A mode at level n covers every mode at a
// level <= n on the same resource (re-entrant requests are free), and it
// satisfies the corresponding ancestor intention requirements.
func (m LockMode) level() int {
	switch m {
	case IS:
		return 1
	case IX:
		return 2
	case S:
		return 3
	case SIX:
		return 4
	case X:
		return 5
	default:
		return 0
	}
}

// covers reports whether holding mode m already satisfies a request for want.
func (m LockMode) covers(want LockMode) bool {
	return m.level() >= want.level()
}

// compatible is the standard multi-granularity compatibility matrix.
//
//		IS   IX   S    SIX  X
//	IS  yes  yes  yes  yes  no
//	IX  yes  yes  no   no   no
//	S   yes  no   yes  no   no
//	SIX yes  no   no   no   no
//	X   no   no   no   no   no
func compatible(a, b LockMode) bool {
	return compatMatrix[a][b]
}

var compatMatrix = [6][6]bool{
	// None, IS, IX, S, SIX, X
	{false, true, true, true, true, true},     // None (no holder)
	{true, true, true, true, true, false},     // IS
	{true, true, true, false, false, false},   // IX
	{true, true, false, true, false, false},   // S
	{true, true, false, false, false, false},  // SIX
	{true, false, false, false, false, false}, // X
}

// unionMode returns the strongest mode representing holding both a and b on
// the same resource (lock conversion within one transaction).
func unionMode(a, b LockMode) LockMode {
	if a == None {
		return b
	}
	if b == None {
		return a
	}
	if a == b {
		return a
	}
	if a.level() < b.level() {
		a, b = b, a
	}
	// a is now the stronger of the two distinct modes.
	switch a {
	case X:
		return X
	case SIX:
		// SIX with anything weaker except nothing-meaningful stays SIX.
		return SIX
	case S:
		// S + IX = SIX; S + IS = S.
		if b == IX {
			return SIX
		}
		return S
	case IX:
		// IX + IS = IX.
		return IX
	case IS:
		return IS
	default:
		return b
	}
}

// minParentMode returns the minimum mode that must be held on every ancestor
// resource before requesting want on a descendant:
//
//   - IS/S need an IS-class ancestor (IS, IX, SIX or X all qualify);
//   - IX/SIX/X need an IX-class ancestor (IX, SIX or X qualify).
func minParentMode(want LockMode) LockMode {
	switch want {
	case IS, S:
		return IS
	case IX, SIX, X:
		return IX
	default:
		return None
	}
}
