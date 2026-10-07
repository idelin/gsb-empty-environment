package vpa

// ProtocolA and ProtocolB model two versions of a small protocol parser
// over the alphabet {c0, c1} (calls: open element of kind 0/1),
// {r0, r1} (returns: close element of kind 0/1) and {i} (internal:
// payload data). Stack symbols are the pending element kinds {0, 1}.
//
// States: q0 = base (nothing open), q1 = inside an element,
// q2 = error (absorbing), q3 = inside a freshly opened c1 element.
// Finals: {q0, q1, q3} — anything but the error state.
//
// Both versions share a dedicated empty-stack return rule: a stray close
// with nothing open goes to the error state q2. A close whose kind does
// not match the pending open also goes to q2.
//
// ProtocolB contains a bug: opening a c0 element directly inside a c1
// element (transition on c0 from q3) pushes the wrong tag 1 instead of
// 0, so a later matching close is misdiagnosed.
func ProtocolA() *Automaton { return protocol("A", false) }
func ProtocolB() *Automaton { return protocol("B", true) }

func protocol(name string, bug bool) *Automaton {
	a := New(name, 4, 0, []int{0, 1, 3},
		[]Kind{Call, Call, Return, Return, Internal},
		[]string{"c0", "c1", "r0", "r1", "i"},
		[]string{"0", "1"})

	// Calls: push the opened element kind, remember c1-context in q3.
	a.OnCall(0, 0, 1, 0)
	a.OnCall(0, 1, 1, 1)
	a.OnCall(1, 0, 1, 0)
	a.OnCall(1, 1, 3, 1)
	pushC0InC1 := 0
	if bug {
		pushC0InC1 = 1 // the bug: wrong tag pushed inside a c1 element
	}
	a.OnCall(3, 0, 1, pushC0InC1)
	a.OnCall(3, 1, 3, 1)
	a.OnCall(2, 0, 2, 0)
	a.OnCall(2, 1, 2, 1)

	// Dedicated empty-stack return rule: stray close -> error q2.
	for q := 0; q < 4; q++ {
		a.OnReturnEmpty(q, 2, 2)
		a.OnReturnEmpty(q, 3, 2)
	}
	// Returns on a non-empty stack: matching close pops, mismatch -> q2.
	for _, q := range []int{1, 3} {
		for k := 0; k < 2; k++ {
			for top := 0; top < 2; top++ {
				next := 2
				if top == k {
					next = 1
				}
				a.OnReturn(q, 2+k, top, next)
			}
		}
	}
	// q0 is only reachable with an empty stack and q2 is absorbing;
	// define their non-empty returns for completeness.
	for k := 0; k < 2; k++ {
		for top := 0; top < 2; top++ {
			a.OnReturn(0, 2+k, top, 2)
			a.OnReturn(2, 2+k, top, 2)
		}
	}
	// Internal payload symbol: self-loop everywhere.
	for q := 0; q < 4; q++ {
		a.OnInternal(q, 4, q)
	}
	return a
}
