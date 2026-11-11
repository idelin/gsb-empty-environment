package netsec

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// MaxComposeDepth bounds the composition depth of messages the attacker
// may synthesize. A known term used as a building block counts as depth 1;
// each pair/encryption constructor adds one.
const MaxComposeDepth = 3

// Status is the verdict of a FindMismatch run.
type Status uint8

const (
	Safe    Status = iota // no mismatching end event is reachable
	Attack                // a trace with a mismatching end event was found
	Unknown               // the exploration budget was exhausted
)

func (s Status) String() string {
	switch s {
	case Safe:
		return "safe"
	case Attack:
		return "attack"
	case Unknown:
		return "unknown"
	}
	return "?"
}

// TraceEntry records one executed step of an interleaving. For RecvStep
// entries, Proof certifies how the attacker constructed Message.
type TraceEntry struct {
	Session int
	Step    int // index into the role script
	Op      StepKind
	Message *Term       // FreshStep (the new atom), SendStep, RecvStep
	Args    []*Term     // BeginStep, EndStep
	Proof   *Derivation // RecvStep only
}

func (e TraceEntry) String() string {
	head := fmt.Sprintf("s%d#%d %s", e.Session, e.Step, e.Op)
	switch e.Op {
	case FreshStep, SendStep:
		return head + " " + e.Message.String()
	case RecvStep:
		return head + " " + e.Message.String()
	default:
		parts := make([]string, len(e.Args))
		for i, a := range e.Args {
			parts[i] = a.String()
		}
		return head + "(" + strings.Join(parts, ",") + ")"
	}
}

// Result is the outcome of FindMismatch. For Attack, Trace is the shortest
// interleaving ending in a mismatching end event and doubles as the
// replayable certificate checked by Verify.
type Result struct {
	Status   Status
	Trace    []TraceEntry
	Explored int // number of states dequeued
}

func (r *Result) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "status=%s explored=%d", r.Status, r.Explored)
	for i, e := range r.Trace {
		fmt.Fprintf(&b, "\n  %2d. %s", i, e)
	}
	return b.String()
}

// state is one node of the interleaving search space.
type state struct {
	pc     [2]int
	env    [2]map[string]*Term
	know   map[string]*Term // attacker knowledge, closed under analysis
	begins map[string]int   // begin claims observed so far, keyed by argsKey
	trace  []TraceEntry
}

func (st *state) clone() *state {
	ns := &state{
		pc:     st.pc,
		know:   make(map[string]*Term, len(st.know)),
		begins: make(map[string]int, len(st.begins)),
		trace:  st.trace,
	}
	for i := 0; i < 2; i++ {
		ns.env[i] = make(map[string]*Term, len(st.env[i]))
		for k, v := range st.env[i] {
			ns.env[i][k] = v
		}
	}
	for k, v := range st.know {
		ns.know[k] = v
	}
	for k, v := range st.begins {
		ns.begins[k] = v
	}
	return ns
}

func (st *state) key() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d,%d|", st.pc[0], st.pc[1])
	for i := 0; i < 2; i++ {
		keys := make([]string, 0, len(st.env[i]))
		for k, v := range st.env[i] {
			keys = append(keys, k+"="+v.String())
		}
		sort.Strings(keys)
		b.WriteString(strings.Join(keys, ","))
		b.WriteByte('|')
	}
	keys := make([]string, 0, len(st.know))
	for k := range st.know {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	b.WriteString(strings.Join(keys, ","))
	b.WriteByte('|')
	beg := make([]string, 0, len(st.begins))
	for k := range st.begins {
		beg = append(beg, k)
	}
	sort.Strings(beg)
	b.WriteString(strings.Join(beg, ","))
	return b.String()
}

func argsKey(args []*Term) string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = a.String()
	}
	return strings.Join(parts, "|")
}

func copyEnv(env map[string]*Term) map[string]*Term {
	out := make(map[string]*Term, len(env))
	for k, v := range env {
		out[k] = v
	}
	return out
}

func appendTrace(trace []TraceEntry, e TraceEntry) []TraceEntry {
	out := make([]TraceEntry, len(trace)+1)
	copy(out, trace)
	out[len(trace)] = e
	return out
}

// closeKnow closes the knowledge set under pair projection and decryption
// with known keys, to a fixpoint.
func closeKnow(know map[string]*Term) {
	for changed := true; changed; {
		changed = false
		var adds []*Term
		for _, t := range know {
			switch t.K {
			case Pair:
				adds = append(adds, t.A, t.B)
			case Enc:
				if _, ok := know[t.A.String()]; ok {
					adds = append(adds, t.B)
				}
			}
		}
		for _, a := range adds {
			if _, ok := know[a.String()]; !ok {
				know[a.String()] = a
				changed = true
			}
		}
	}
}

func addKnow(know map[string]*Term, t *Term) {
	know[t.String()] = t
	closeKnow(know)
}

// synthesize enumerates every term the attacker can build from know with
// composition depth at most MaxComposeDepth, together with a shallowest
// derivation for each. Known terms are the leaves (depth 1); the attacker
// can never invent atoms, fresh or otherwise.
func synthesize(know map[string]*Term) map[string]*Derivation {
	out := map[string]*Derivation{}
	depth := map[string]int{}
	terms := map[string]*Term{}
	var order []string
	add := func(t *Term, d int, p *Derivation) {
		k := t.String()
		if _, ok := out[k]; ok {
			return
		}
		out[k] = p
		depth[k] = d
		terms[k] = t
		order = append(order, k)
	}
	for _, t := range know {
		add(t, 1, &Derivation{Op: Leaf, Term: t})
	}
	for d := 2; d <= MaxComposeDepth; d++ {
		snapshot := append([]string(nil), order...)
		for _, kx := range snapshot {
			for _, ky := range snapshot {
				if maxInt(depth[kx], depth[ky])+1 != d {
					continue
				}
				x, y := terms[kx], terms[ky]
				pt := PairOf(x, y)
				add(pt, d, &Derivation{Op: ComposePair, Term: pt, Kids: []*Derivation{out[kx], out[ky]}})
				et := EncOf(x, y)
				add(et, d, &Derivation{Op: ComposeEnc, Term: et, Kids: []*Derivation{out[kx], out[ky]}})
			}
		}
	}
	return out
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// FindMismatch explores, in breadth-first order, every interleaving of the
// two sessions described by sA and sB. It reports Attack together with the
// shortest trace in which a session performs an end claim whose arguments
// match no earlier begin claim. If no such trace exists it reports Safe;
// if more than budget states would have to be explored it reports Unknown.
// A negative budget means no limit. initial is the attacker's initial
// knowledge.
func FindMismatch(p *Protocol, sA, sB *SessionSpec, initial []*Term, budget int) (*Result, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	specs := [2]*SessionSpec{sA, sB}
	var roles [2]*Role
	var envs [2]map[string]*Term
	for i, s := range specs {
		r, err := p.role(s.Role)
		if err != nil {
			return nil, err
		}
		roles[i] = r
		env := map[string]*Term{}
		for v, a := range s.Params {
			if !validName(v) || !validName(a) {
				return nil, fmt.Errorf("netsec: invalid param binding %q=%q", v, a)
			}
			env[v] = AtomOf(a)
		}
		envs[i] = env
	}
	know := map[string]*Term{}
	for _, t := range initial {
		if t == nil || !t.Ground() {
			return nil, fmt.Errorf("netsec: initial knowledge must be ground")
		}
		know[t.String()] = t
	}
	closeKnow(know)

	root := &state{env: envs, know: know, begins: map[string]int{}}
	visited := map[string]bool{root.key(): true}
	queue := []*state{root}
	synthCache := map[string]map[string]*Derivation{}
	explored := 0

	push := func(ns *state) {
		k := ns.key()
		if visited[k] {
			return
		}
		visited[k] = true
		queue = append(queue, ns)
	}

	for len(queue) > 0 {
		if budget >= 0 && explored >= budget {
			return &Result{Status: Unknown, Explored: explored}, nil
		}
		st := queue[0]
		queue = queue[1:]
		explored++

		for sess := 0; sess < 2; sess++ {
			if st.pc[sess] >= len(roles[sess].Steps) {
				continue
			}
			stepIdx := st.pc[sess]
			step := roles[sess].Steps[stepIdx]
			switch step.Kind {
			case FreshStep:
				atom := AtomOf(step.Var + "!" + strconv.Itoa(sess))
				ns := st.clone()
				ns.pc[sess]++
				ns.env[sess][step.Var] = atom
				ns.trace = appendTrace(st.trace, TraceEntry{Session: sess, Step: stepIdx, Op: FreshStep, Message: atom})
				push(ns)
			case SendStep:
				m, ok := resolve(step.Pat, st.env[sess])
				if !ok {
					continue
				}
				ns := st.clone()
				ns.pc[sess]++
				addKnow(ns.know, m)
				ns.trace = appendTrace(st.trace, TraceEntry{Session: sess, Step: stepIdx, Op: SendStep, Message: m})
				push(ns)
			case RecvStep:
				kk := knowCacheKey(st.know)
				synth, ok := synthCache[kk]
				if !ok {
					synth = synthesize(st.know)
					synthCache[kk] = synth
				}
				keys := make([]string, 0, len(synth))
				for k := range synth {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				for _, k := range keys {
					proof := synth[k]
					env2 := copyEnv(st.env[sess])
					if !match(step.Pat, proof.Term, env2) {
						continue
					}
					ns := st.clone()
					ns.pc[sess]++
					ns.env[sess] = env2
					ns.trace = appendTrace(st.trace, TraceEntry{Session: sess, Step: stepIdx, Op: RecvStep, Message: proof.Term, Proof: proof})
					push(ns)
				}
			case BeginStep:
				args, ok := resolveAll(step.Args, st.env[sess])
				if !ok {
					continue
				}
				ns := st.clone()
				ns.pc[sess]++
				ns.begins[argsKey(args)]++
				ns.trace = appendTrace(st.trace, TraceEntry{Session: sess, Step: stepIdx, Op: BeginStep, Args: args})
				push(ns)
			case EndStep:
				args, ok := resolveAll(step.Args, st.env[sess])
				if !ok {
					continue
				}
				entry := TraceEntry{Session: sess, Step: stepIdx, Op: EndStep, Args: args}
				if st.begins[argsKey(args)] == 0 {
					return &Result{Status: Attack, Trace: appendTrace(st.trace, entry), Explored: explored}, nil
				}
				ns := st.clone()
				ns.pc[sess]++
				ns.trace = appendTrace(st.trace, entry)
				push(ns)
			}
		}
	}
	return &Result{Status: Safe, Explored: explored}, nil
}

func resolveAll(pats []*Term, env map[string]*Term) ([]*Term, bool) {
	out := make([]*Term, len(pats))
	for i, p := range pats {
		t, ok := resolve(p, env)
		if !ok {
			return nil, false
		}
		out[i] = t
	}
	return out, true
}

func knowCacheKey(know map[string]*Term) string {
	keys := make([]string, 0, len(know))
	for k := range know {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, "|")
}
