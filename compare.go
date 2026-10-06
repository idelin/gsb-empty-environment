package transducer

import "errors"

// Status is the outcome of an equivalence check.
type Status int

const (
	// Equal means every complete input string of length MaxLen makes
	// both machines emit the same concatenated output.
	Equal Status = iota
	// NotEqual means a counterexample was found; Verdict.Witness holds
	// a complete input string, verified by full execution on both
	// machines.
	NotEqual
	// Unknown means the expansion budget was exhausted before the
	// product graph was fully explored. Verdict.Frontier holds the
	// remaining search frontier and can be passed to Resume.
	Unknown
)

func (s Status) String() string {
	switch s {
	case Equal:
		return "Equal"
	case NotEqual:
		return "NotEqual"
	default:
		return "Unknown"
	}
}

// Config configures an equivalence check.
type Config struct {
	// Alphabet is the set of input symbols explored at every reached
	// product state. Duplicates are ignored.
	Alphabet []rune
	// MaxLen is the public finite bound on input length: equivalence
	// is decided over all complete input strings of length exactly
	// MaxLen. MaxLen == 0 checks only the empty string and therefore
	// always yields Equal.
	MaxLen int
	// Budget bounds the number of product-graph nodes expanded. When
	// the budget is exhausted the check returns Unknown together with
	// a resumable frontier. Budget <= 0 means unlimited.
	Budget int
}

// Verdict is the result of Compare or Resume.
type Verdict struct {
	Status Status
	// Witness, set when Status is NotEqual, is a complete input string
	// of length MaxLen whose concatenated outputs differ. It is always
	// re-verified by full execution on both machines before being
	// reported.
	Witness []rune
	// Frontier, set when Status is Unknown, captures the unexplored
	// search frontier for resumption via Resume.
	Frontier *Frontier
	// Expanded counts product-graph nodes expanded during this call.
	Expanded int
}

// FrontierItem is one unexplored product-graph node. All fields are
// exported so a Frontier can be serialized (e.g. as JSON) and restored
// later.
type FrontierItem struct {
	QA, QB int
	// LagA/LagB are the pending output difference: the concatenated
	// outputs produced so far are equal up to a common prefix, with
	// LagA (resp. LagB) left over on one side. At most one is non-empty.
	LagA, LagB string
	// Path is the input prefix that reached this node.
	Path []rune
}

// Frontier is the resumable state of an interrupted check.
type Frontier struct {
	Items []FrontierItem
}

// Compare checks bounded equivalence of a and b. See the package
// documentation for the exact semantics.
func Compare(a, b *Machine, cfg Config) (Verdict, error) {
	if err := validate(a, b, &cfg); err != nil {
		return Verdict{}, err
	}
	if cfg.MaxLen == 0 {
		return Verdict{Status: Equal}, nil
	}
	root := node{qa: a.Initial, qb: b.Initial}
	return search(a, b, cfg, []node{root}), nil
}

// Resume continues a check that previously returned Unknown, using the
// frontier carried by that verdict. The same machines and configuration
// (only Budget may change) must be supplied.
func Resume(a, b *Machine, cfg Config, f *Frontier) (Verdict, error) {
	if err := validate(a, b, &cfg); err != nil {
		return Verdict{}, err
	}
	if f == nil {
		return Verdict{}, errors.New("transducer: nil frontier")
	}
	var queue []node
	for _, it := range f.Items {
		if len(it.Path) >= cfg.MaxLen {
			continue
		}
		queue = append(queue, node{
			qa: it.QA, qb: it.QB,
			lagA: it.LagA, lagB: it.LagB,
			path: append([]rune(nil), it.Path...),
		})
	}
	if len(queue) == 0 {
		return Verdict{Status: Equal}, nil
	}
	return search(a, b, cfg, queue), nil
}

// node is a product-graph vertex: both machine states plus the pending
// output difference (lag) and the input path that reached it.
type node struct {
	qa, qb     int
	lagA, lagB string
	path       []rune
}

type stateKey struct {
	qa, qb     int
	depth      int
	lagA, lagB string
}

func search(a, b *Machine, cfg Config, queue []node) Verdict {
	expanded := 0
	visited := make(map[stateKey]struct{})
	for len(queue) > 0 {
		if cfg.Budget > 0 && expanded >= cfg.Budget {
			return Verdict{Status: Unknown, Frontier: frontierFrom(queue), Expanded: expanded}
		}
		item := queue[0]
		queue = queue[1:]
		expanded++
		depth := len(item.path)
		for _, sym := range cfg.Alphabet {
			qa, outA := step(a, item.qa, sym)
			qb, outB := step(b, item.qb, sym)
			lagA, lagB := reconcile(item.lagA, item.lagB, outA, outB)
			path := append(append([]rune(nil), item.path...), sym)
			if depth+1 == cfg.MaxLen {
				// Complete input string: compare totals.
				if lagA != "" || lagB != "" || (qa == Halted) != (qb == Halted) {
					if verifies(a, b, path) {
						return Verdict{Status: NotEqual, Witness: path, Expanded: expanded}
					}
				}
				continue
			}
			if diverged(qa, qb, lagA, lagB) {
				// No suffix can reconcile the outputs; any
				// completion is a counterexample.
				full := complete(path, cfg.MaxLen, cfg.Alphabet)
				if verifies(a, b, full) {
					return Verdict{Status: NotEqual, Witness: full, Expanded: expanded}
				}
				continue
			}
			if qa == Halted && qb == Halted {
				// Both frozen with equal totals; descendants
				// can never differ.
				continue
			}
			key := stateKey{qa, qb, depth + 1, lagA, lagB}
			if _, seen := visited[key]; seen {
				continue
			}
			visited[key] = struct{}{}
			queue = append(queue, node{qa, qb, lagA, lagB, path})
		}
	}
	return Verdict{Status: Equal, Expanded: expanded}
}

// reconcile appends new edge outputs to the pending difference and strips
// the common prefix, restoring the invariant that at most one lag is
// non-empty.
func reconcile(lagA, lagB, outA, outB string) (string, string) {
	x, y := lagA+outA, lagB+outB
	i := 0
	for i < len(x) && i < len(y) && x[i] == y[i] {
		i++
	}
	return x[i:], y[i:]
}

// diverged reports whether the pending difference can never be reconciled
// by future outputs. A halted machine cannot emit further output, so any
// lag owed to it is permanent; two running machines diverge once both
// lags are non-empty (their next output byte differs).
func diverged(qa, qb int, lagA, lagB string) bool {
	aHalted, bHalted := qa == Halted, qb == Halted
	switch {
	case aHalted && bHalted:
		return lagA != "" || lagB != ""
	case aHalted:
		return lagB != ""
	case bHalted:
		return lagA != ""
	default:
		return lagA != "" && lagB != ""
	}
}

// complete pads a diverging prefix to a full length-MaxLen input; the
// choice of suffix is irrelevant because the outputs already differ.
func complete(path []rune, maxLen int, alphabet []rune) []rune {
	full := append([]rune(nil), path...)
	for len(full) < maxLen {
		full = append(full, alphabet[0])
	}
	return full
}

// verifies re-executes a candidate counterexample on both machines and
// confirms the concatenated outputs (including error flags) differ.
func verifies(a, b *Machine, input []rune) bool {
	return a.Run(input) != b.Run(input)
}

func frontierFrom(queue []node) *Frontier {
	f := &Frontier{Items: make([]FrontierItem, len(queue))}
	for i, n := range queue {
		f.Items[i] = FrontierItem{
			QA: n.qa, QB: n.qb,
			LagA: n.lagA, LagB: n.lagB,
			Path: append([]rune(nil), n.path...),
		}
	}
	return f
}

func validate(a, b *Machine, cfg *Config) error {
	if cfg.MaxLen < 0 {
		return errors.New("transducer: MaxLen must be >= 0")
	}
	if cfg.MaxLen > 0 && len(cfg.Alphabet) == 0 {
		return errors.New("transducer: Alphabet must be non-empty when MaxLen > 0")
	}
	cfg.Alphabet = dedup(cfg.Alphabet)
	if err := validateMachine(a); err != nil {
		return errors.New("transducer: machine A: " + err.Error())
	}
	if err := validateMachine(b); err != nil {
		return errors.New("transducer: machine B: " + err.Error())
	}
	return nil
}

func validateMachine(m *Machine) error {
	if m.Initial < 0 {
		return errors.New("negative initial state")
	}
	for from, row := range m.Transitions {
		if from < 0 {
			return errors.New("negative state id")
		}
		for _, e := range row {
			if e.To < 0 {
				return errors.New("negative transition target")
			}
		}
	}
	return nil
}

func dedup(symbols []rune) []rune {
	seen := make(map[rune]bool)
	out := symbols[:0]
	for _, s := range symbols {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
