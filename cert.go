package netsec

import (
	"fmt"
	"strconv"
)

// DerivationOp identifies one node of an attacker derivation proof.
type DerivationOp string

const (
	Leaf        DerivationOp = "leaf" // term taken from attacker knowledge
	ComposePair DerivationOp = "pair" // pair built from two sub-derivations
	ComposeEnc  DerivationOp = "enc"  // encryption: Kids[0] key, Kids[1] body
)

// Derivation is a replayable proof that the attacker can construct Term.
// Leaves must belong to the attacker knowledge (closed under pair
// projection and decryption with known keys) at the point of use.
type Derivation struct {
	Op   DerivationOp
	Term *Term
	Kids []*Derivation
}

// Depth is the composition depth of the derivation; leaves count as 1.
func (d *Derivation) Depth() int {
	if d.Op == Leaf || len(d.Kids) == 0 {
		return 1
	}
	return 1 + maxInt(d.Kids[0].Depth(), d.Kids[1].Depth())
}

// Leaves returns the terms this derivation draws from attacker knowledge.
func (d *Derivation) Leaves() []*Term {
	if d.Op == Leaf {
		return []*Term{d.Term}
	}
	var out []*Term
	for _, k := range d.Kids {
		out = append(out, k.Leaves()...)
	}
	return out
}

// Rebuild recomputes the derived term bottom-up and checks it against the
// Term recorded at every node.
func (d *Derivation) Rebuild() (*Term, error) {
	if d == nil || d.Term == nil {
		return nil, fmt.Errorf("netsec: malformed derivation")
	}
	switch d.Op {
	case Leaf:
		if len(d.Kids) != 0 {
			return nil, fmt.Errorf("netsec: leaf with children")
		}
		return d.Term, nil
	case ComposePair, ComposeEnc:
		if len(d.Kids) != 2 {
			return nil, fmt.Errorf("netsec: %s needs exactly two children", d.Op)
		}
		a, err := d.Kids[0].Rebuild()
		if err != nil {
			return nil, err
		}
		b, err := d.Kids[1].Rebuild()
		if err != nil {
			return nil, err
		}
		var t *Term
		if d.Op == ComposePair {
			t = PairOf(a, b)
		} else {
			t = EncOf(a, b)
		}
		if !t.Equal(d.Term) {
			return nil, fmt.Errorf("netsec: derivation node mismatch: got %s, want %s", t, d.Term)
		}
		return t, nil
	}
	return nil, fmt.Errorf("netsec: unknown derivation op %q", d.Op)
}

// Verify replays an attack certificate: it re-executes Trace against the
// protocol and the two session specifications, checking that every step is
// a legal transition, that every received message matches its pattern and
// carries a valid attacker derivation (leaves in the knowledge accumulated
// so far, composition depth within MaxComposeDepth), and that the trace
// ends in an end claim with no corresponding begin claim. initial is the
// attacker's initial knowledge, as in FindMismatch.
func (r *Result) Verify(p *Protocol, sA, sB *SessionSpec, initial []*Term) error {
	if r.Status != Attack {
		return fmt.Errorf("netsec: only attack results carry a certificate")
	}
	if err := p.Validate(); err != nil {
		return err
	}
	if len(r.Trace) == 0 {
		return fmt.Errorf("netsec: empty trace")
	}
	specs := [2]*SessionSpec{sA, sB}
	var roles [2]*Role
	var envs [2]map[string]*Term
	for i, s := range specs {
		role, err := p.role(s.Role)
		if err != nil {
			return err
		}
		roles[i] = role
		env := map[string]*Term{}
		for v, a := range s.Params {
			if !validName(v) || !validName(a) {
				return fmt.Errorf("netsec: invalid param binding %q=%q", v, a)
			}
			env[v] = AtomOf(a)
		}
		envs[i] = env
	}
	know := map[string]*Term{}
	for _, t := range initial {
		know[t.String()] = t
	}
	closeKnow(know)

	var pc [2]int
	begins := map[string]int{}
	for i, e := range r.Trace {
		if e.Session < 0 || e.Session > 1 {
			return fmt.Errorf("netsec: trace %d: bad session %d", i, e.Session)
		}
		sess := e.Session
		if e.Step != pc[sess] || pc[sess] >= len(roles[sess].Steps) {
			return fmt.Errorf("netsec: trace %d: session %d is not at step %d", i, sess, e.Step)
		}
		step := roles[sess].Steps[pc[sess]]
		if step.Kind != e.Op {
			return fmt.Errorf("netsec: trace %d: script has %s, trace has %s", i, step.Kind, e.Op)
		}
		switch e.Op {
		case FreshStep:
			want := AtomOf(step.Var + "!" + strconv.Itoa(sess))
			if e.Message == nil || !e.Message.Equal(want) {
				return fmt.Errorf("netsec: trace %d: fresh atom mismatch", i)
			}
			envs[sess][step.Var] = want
		case SendStep:
			m, ok := resolve(step.Pat, envs[sess])
			if !ok {
				return fmt.Errorf("netsec: trace %d: send pattern not ground", i)
			}
			if e.Message == nil || !m.Equal(e.Message) {
				return fmt.Errorf("netsec: trace %d: sent message mismatch", i)
			}
			addKnow(know, m)
		case RecvStep:
			if e.Proof == nil {
				return fmt.Errorf("netsec: trace %d: missing derivation", i)
			}
			built, err := e.Proof.Rebuild()
			if err != nil {
				return fmt.Errorf("netsec: trace %d: %w", i, err)
			}
			if e.Message == nil || !built.Equal(e.Message) {
				return fmt.Errorf("netsec: trace %d: derivation does not produce the received message", i)
			}
			if e.Proof.Depth() > MaxComposeDepth {
				return fmt.Errorf("netsec: trace %d: derivation depth exceeds %d", i, MaxComposeDepth)
			}
			for _, leaf := range e.Proof.Leaves() {
				if _, ok := know[leaf.String()]; !ok {
					return fmt.Errorf("netsec: trace %d: leaf %s not derivable from attacker knowledge", i, leaf)
				}
			}
			env2 := copyEnv(envs[sess])
			if !match(step.Pat, e.Message, env2) {
				return fmt.Errorf("netsec: trace %d: message does not match recv pattern", i)
			}
			envs[sess] = env2
		case BeginStep, EndStep:
			args, ok := resolveAll(step.Args, envs[sess])
			if !ok {
				return fmt.Errorf("netsec: trace %d: %s arguments not ground", i, e.Op)
			}
			if len(e.Args) != len(args) {
				return fmt.Errorf("netsec: trace %d: argument count mismatch", i)
			}
			for j := range args {
				if !args[j].Equal(e.Args[j]) {
					return fmt.Errorf("netsec: trace %d: argument %d mismatch", i, j)
				}
			}
			key := argsKey(args)
			if e.Op == BeginStep {
				begins[key]++
			} else {
				last := i == len(r.Trace)-1
				if begins[key] == 0 && !last {
					return fmt.Errorf("netsec: trace %d: mismatching end before end of trace", i)
				}
				if begins[key] != 0 && last {
					return fmt.Errorf("netsec: trace %d: final end has a matching begin", i)
				}
			}
		default:
			return fmt.Errorf("netsec: trace %d: bad op %d", i, e.Op)
		}
		pc[sess]++
	}
	if r.Trace[len(r.Trace)-1].Op != EndStep {
		return fmt.Errorf("netsec: trace does not end with an end event")
	}
	return nil
}
