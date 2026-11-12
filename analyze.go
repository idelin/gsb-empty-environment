package netsec

import "strconv"

// never marks a term the attacker cannot derive at any finite time.
const never = int(^uint(0) >> 1)

// Proof is a derivation tree explaining one knowledge step. SubProofs are the
// strictly earlier facts the step depends on.
type Proof struct {
	Term      Term
	Rule      Rule
	At        int
	Event     *Event
	SubProofs []Proof
}

// LeakReport summarizes the analysis of one historical secret.
type LeakReport struct {
	// Secret is the term the host asked about.
	Secret Term
	// Leaked reports whether the attacker can derive the secret at all.
	Leaked bool
	// Earliest is the minimum derivation time; it is 0 when Leaked is false.
	Earliest int
	// Proof is the earliest derivation tree; it is nil when Leaked is false.
	Proof *Proof
}

// Analyze computes the earliest time at which every secret in secrets becomes
// derivable by the network attacker, together with the supporting proof.
//
// Secrets that never become derivable are reported with Leaked == false.
// Repeated secrets are deduplicated. The analysis only depends on the partial
// order given by integer times; events sharing a time may be combined, so a
// compromise at the same time as a destruction is treated as too late.
func (m *Model) Analyze(secrets ...Term) []LeakReport {
	n := len(m.terms)
	earliest := make([]int, n)
	proofs := make([]*Proof, n)
	for i := range earliest {
		earliest[i] = never
	}

	inQueue := make([]bool, n)
	queue := make([]int, 0, n)
	seed := func(id, at int, p *Proof) {
		if at < earliest[id] {
			earliest[id] = at
			proofs[id] = p
			if !inQueue[id] {
				inQueue[id] = true
				queue = append(queue, id)
			}
		}
	}

	// Timeline seeds.
	for i := range m.events {
		ev := m.events[i]
		switch ev.Kind {
		case "send":
			// Pair structure and ciphertext wrappers are transparent; keys
			// under Enc or inputs under Derive stay protected.
			forEachVisible(ev.Term, func(t Term) {
				ref := ev
				seed(t.id, ev.Time, &Proof{Term: t, Rule: RuleSend, At: ev.Time, Event: &ref})
			})
		case "compromise":
			if dt, gone := m.destroy[ev.Term.id]; gone && dt <= ev.Time {
				continue
			}
			ref := ev
			seed(ev.Term.id, ev.Time, &Proof{Term: ev.Term, Rule: RuleCompromise, At: ev.Time, Event: &ref})
		}
	}

	relax := func(id, at int, p *Proof) { seed(id, at, p) }

	// Least fixed point of the closure rules.
	for head := 0; head < len(queue); head++ {
		id := queue[head]
		inQueue[id] = false
		t := m.terms[id]
		at := earliest[id]

		// Rules whose premises include this term: propagate toward users.
		for _, uid := range m.users[id] {
			u := m.terms[uid]
			switch u.kind {
			case Pair:
				// Known component: the pair is buildable once both sides known.
				other := u.args[0]
				if other.id == id {
					other = u.args[1]
				}
				if earliest[other.id] != never {
					bt := maxInt(at, earliest[other.id])
					relax(uid, bt, &Proof{
						Term: u, Rule: RulePairBuild, At: bt,
						SubProofs: []Proof{*proofs[id], *proofs[other.id]},
					})
				}
			case Enc:
				key, plain := u.args[0], u.args[1]
				if key.id == id {
					// Known key plus visible ciphertext reveals the plaintext.
					if earliest[uid] != never {
						dt := maxInt(at, earliest[uid])
						relax(plain.id, dt, &Proof{
							Term: plain, Rule: RuleDecrypt, At: dt,
							SubProofs: []Proof{*proofs[uid], *proofs[id]},
						})
					}
				} else {
					// Known plaintext plus key lets the attacker rebuild the
					// ciphertext. Decryption is driven from the key's trigger
					// path below so its proof never depends on itself.
					if earliest[key.id] != never {
						et := maxInt(at, earliest[key.id])
						relax(uid, et, &Proof{
							Term: u, Rule: RuleEncrypt, At: et,
							SubProofs: []Proof{*proofs[key.id], *proofs[id]},
						})
					}
				}
			case Derive:
				if allKnown(u.args, earliest) {
					dt := maxOf(u.args, earliest)
					relax(uid, dt, &Proof{
						Term: u, Rule: RuleDerive, At: dt,
						SubProofs: subProofs(u.args, proofs),
					})
				}
			}
		}

		// Rules whose conclusion is a subterm of a known composite term.
		switch t.kind {
		case Pair:
			left, right := t.args[0], t.args[1]
			relax(left.id, at, &Proof{
				Term: left, Rule: RulePair, At: at, SubProofs: []Proof{*proofs[id]},
			})
			relax(right.id, at, &Proof{
				Term: right, Rule: RulePair, At: at, SubProofs: []Proof{*proofs[id]},
			})
		case Enc:
			key, plain := t.args[0], t.args[1]
			if earliest[key.id] != never {
				dt := maxInt(at, earliest[key.id])
				relax(plain.id, dt, &Proof{
					Term: plain, Rule: RuleDecrypt, At: dt,
					SubProofs: []Proof{*proofs[id], *proofs[key.id]},
				})
			}
		}
	}

	seen := make(map[int]bool, len(secrets))
	reports := make([]LeakReport, 0, len(secrets))
	for _, s := range secrets {
		if seen[s.id] {
			continue
		}
		seen[s.id] = true
		rep := LeakReport{Secret: s}
		if earliest[s.id] != never {
			rep.Leaked = true
			rep.Earliest = earliest[s.id]
			p := proofs[s.id]
			rep.Proof = p
		}
		reports = append(reports, rep)
	}
	return reports
}

// forEachVisible visits t and every subterm reachable without opening an
// encryption or a derivation: pairs are transparent, Enc and Derive are
// opaque containers from the wire observer's point of view.
func forEachVisible(t Term, visit func(Term)) {
	visit(t)
	if t.kind == Pair {
		forEachVisible(t.args[0], visit)
		forEachVisible(t.args[1], visit)
	}
}

func allKnown(args []Term, earliest []int) bool {
	for _, a := range args {
		if earliest[a.id] == never {
			return false
		}
	}
	return true
}

func maxOf(args []Term, earliest []int) int {
	best := earliest[args[0].id]
	for _, a := range args[1:] {
		best = maxInt(best, earliest[a.id])
	}
	return best
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func subProofs(args []Term, proofs []*Proof) []Proof {
	out := make([]Proof, 0, len(args))
	for _, a := range args {
		out = append(out, *proofs[a.id])
	}
	return out
}

// String renders the proof as an indented derivation tree.
func (p Proof) String() string {
	return renderProof(p, 0)
}

func renderProof(p Proof, depth int) string {
	indent := ""
	for i := 0; i < depth; i++ {
		indent += "    "
	}
	line := indent + "t=" + strconv.Itoa(p.At) + " " + string(p.Rule) +
		": " + p.Term.String()
	if p.Event != nil {
		line += " (event #" + strconv.Itoa(p.Event.Index) + ")"
	}
	for _, sub := range p.SubProofs {
		line += "\n" + renderProof(sub, depth+1)
	}
	return line
}

// String renders the term in a compact constructor notation.
func (t Term) String() string {
	switch t.kind {
	case Name:
		return t.label
	case Pair:
		return "pair(" + t.args[0].String() + ", " + t.args[1].String() + ")"
	case Enc:
		return "enc(" + t.args[0].String() + ", " + t.args[1].String() + ")"
	case Derive:
		s := "derive("
		for i, a := range t.args {
			if i > 0 {
				s += ", "
			}
			s += a.String()
		}
		return s + ")"
	default:
		return "term#" + strconv.Itoa(t.id)
	}
}
