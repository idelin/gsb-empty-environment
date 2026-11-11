package packetvm

// Eval evaluates a rule directly from its syntax tree. It is a second,
// fully independent implementation of the rule semantics, written without
// reference to the compiler or interpreter. The test harness uses it as the
// oracle for differential testing over short packets.
//
// A read whose bytes are not present in pkt yields false for the enclosing
// comparison; the compiler's verifier guarantees that on any path where a
// comparison can actually determine the verdict, the read was proven safe.
func Eval(rule Rule, pkt []byte) bool {
	if rule.Body == nil {
		return false
	}
	return evalBool(rule.Body, pkt)
}

func evalBool(b Bool, pkt []byte) bool {
	switch v := b.(type) {
	case *Cmp:
		lhs, ok1 := evalNum(v.LHS, pkt)
		rhs, ok2 := evalNum(v.RHS, pkt)
		if !ok1 || !ok2 {
			return false
		}
		return Compare(v.Op, lhs, rhs)
	case *Not:
		return !evalBool(v.Inner, pkt)
	case *And:
		for _, t := range v.Terms {
			if !evalBool(t, pkt) {
				return false
			}
		}
		return true
	case *Or:
		for _, t := range v.Terms {
			if evalBool(t, pkt) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// evalNum returns (value, ok). ok is false for a read that extends beyond
// the packet; constants and length are always available.
func evalNum(n Numeric, pkt []byte) (uint32, bool) {
	switch v := n.(type) {
	case *Imm:
		return v.Value, true
	case *Len:
		return uint32(len(pkt)), true
	case *Load:
		val, err := readFixed(pkt, v.Offset, v.Size)
		if err != nil {
			return 0, false
		}
		return val, true
	default:
		return 0, false
	}
}
