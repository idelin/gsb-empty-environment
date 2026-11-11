package netsec

import "fmt"

// Kind classifies a capability.
type Kind int

const (
	ReadPath Kind = iota
	WritePath
	CallFunc
)

func (k Kind) String() string {
	switch k {
	case ReadPath:
		return "read"
	case WritePath:
		return "write"
	case CallFunc:
		return "call"
	}
	return "unknown"
}

// Capability is a single grant observed while interpreting a plan: reading
// or writing a normalized path, or calling a trusted function.
type Capability struct {
	Kind Kind
	Path string // normalized path; set for ReadPath and WritePath
	Func string // trusted function name; set for CallFunc
}

func (c Capability) String() string {
	switch c.Kind {
	case ReadPath, WritePath:
		return c.Kind.String() + " " + c.Path
	case CallFunc:
		return c.Kind.String() + " " + c.Func
	}
	return "unknown"
}

// interpreter enumerates the capabilities a plan reaches under one concrete
// condition assignment. Fixed-count loops are unrolled; a step budget bounds
// pathological nesting.
type interpreter struct {
	normalize func(string) string
	maxSteps  int
	steps     int
	caps      map[Capability]struct{}
}

func (in *interpreter) walk(n Node, a Assignment) error {
	if n == nil {
		return nil
	}
	if in.steps >= in.maxSteps {
		return fmt.Errorf("netsec: interpreter step limit %d exceeded", in.maxSteps)
	}
	in.steps++
	switch n := n.(type) {
	case Seq:
		for _, s := range n.Nodes {
			if err := in.walk(s, a); err != nil {
				return err
			}
		}
	case Read:
		in.caps[Capability{Kind: ReadPath, Path: in.normalize(n.Path)}] = struct{}{}
	case Write:
		in.caps[Capability{Kind: WritePath, Path: in.normalize(n.Path)}] = struct{}{}
	case Call:
		in.caps[Capability{Kind: CallFunc, Func: n.Func}] = struct{}{}
	case If:
		if n.Cond.Eval(a) {
			return in.walk(n.Then, a)
		}
		return in.walk(n.Else, a)
	case Loop:
		if n.Count < 0 {
			return fmt.Errorf("netsec: negative loop count %d", n.Count)
		}
		for i := 0; i < n.Count; i++ {
			if err := in.walk(n.Body, a); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("netsec: unknown node type %T", n)
	}
	return nil
}

// run interprets a plan under one assignment and returns the reached
// capabilities.
func run(plan Node, a Assignment, opts Options) (map[Capability]struct{}, error) {
	in := &interpreter{
		normalize: opts.Normalize,
		maxSteps:  opts.MaxSteps,
		caps:      map[Capability]struct{}{},
	}
	if err := in.walk(plan, a); err != nil {
		return nil, err
	}
	return in.caps, nil
}

// planVars returns the bitmask of condition variables referenced by a plan.
func planVars(n Node) (int, error) {
	if n == nil {
		return 0, nil
	}
	switch n := n.(type) {
	case Seq:
		vars := 0
		for _, s := range n.Nodes {
			v, err := planVars(s)
			if err != nil {
				return 0, err
			}
			vars |= v
		}
		return vars, nil
	case Read, Write, Call:
		return 0, nil
	case If:
		thenVars, err := planVars(n.Then)
		if err != nil {
			return 0, err
		}
		elseVars, err := planVars(n.Else)
		if err != nil {
			return 0, err
		}
		return n.Cond.Vars() | thenVars | elseVars, nil
	case Loop:
		return planVars(n.Body)
	default:
		return 0, fmt.Errorf("netsec: unknown node type %T", n)
	}
}

// collectSyntactic gathers every capability mentioned anywhere in a plan,
// ignoring conditions and loop trip counts. It backs the
// unreachable-dangerous verification: a syntactically present dangerous
// capability that no assignment reaches is not an actual addition.
func collectSyntactic(n Node, normalize func(string) string, caps map[Capability]struct{}) error {
	if n == nil {
		return nil
	}
	switch n := n.(type) {
	case Seq:
		for _, s := range n.Nodes {
			if err := collectSyntactic(s, normalize, caps); err != nil {
				return err
			}
		}
	case Read:
		caps[Capability{Kind: ReadPath, Path: normalize(n.Path)}] = struct{}{}
	case Write:
		caps[Capability{Kind: WritePath, Path: normalize(n.Path)}] = struct{}{}
	case Call:
		caps[Capability{Kind: CallFunc, Func: n.Func}] = struct{}{}
	case If:
		if err := collectSyntactic(n.Then, normalize, caps); err != nil {
			return err
		}
		return collectSyntactic(n.Else, normalize, caps)
	case Loop:
		// The body is syntactically present even when Count is zero;
		// enumeration later proves it unreachable.
		return collectSyntactic(n.Body, normalize, caps)
	default:
		return fmt.Errorf("netsec: unknown node type %T", n)
	}
	return nil
}
