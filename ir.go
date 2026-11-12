package taskpkg

// MaxInputs is the maximum number of boolean leaf inputs accepted by a rule.
// Full-enumeration differential testing stays feasible at this bound (2^12).
const MaxInputs = 12

// Emit describes when a leaf check emits its sanitization mark.
type Emit int

const (
	EmitNever Emit = iota
	EmitAlways
	EmitOnTrue
	EmitOnFalse
)

// Mark is a field-scoped sanitization mark produced by a leaf check.
// Trusted marks originate from trusted sanitizer calls; an untrusted mark
// never satisfies a trusted requirement at a forward node.
type Mark struct {
	Field   string
	Name    string
	Trusted bool
}

// Check is a leaf boolean input: given the input at index ID it returns
// true/false and optionally emits one field-scoped mark.
//
// A check is "pure" (shareable across occurrences) only when it emits no
// mark and cannot raise. Effectful checks must be re-executed at every
// occurrence, because each execution carries its own mark side effect.
type Check struct {
	ID       int
	Field    string
	Emit     Emit
	MarkName string
	Trusted  bool
	// RaiseOn, when non-nil, makes the check raise an exception instead of
	// returning a boolean whenever the input equals *RaiseOn. No mark is
	// emitted on a raise.
	RaiseOn *bool
}

// Produces reports the mark this check emits, if any.
func (c Check) Produces() (Mark, bool) {
	if c.Emit == EmitNever || c.MarkName == "" {
		return Mark{}, false
	}
	return Mark{Field: c.Field, Name: c.MarkName, Trusted: c.Trusted}, true
}

// IsPure reports whether the check has no mark side effect and cannot raise.
func (c Check) IsPure() bool {
	_, marked := c.Produces()
	return !marked && c.RaiseOn == nil
}

// Expr is a boolean expression. IDs are unique within a rule.
type Expr struct {
	ID    int
	Kind  ExprKind
	Check *Check // ExprCheck
	Child *Expr  // ExprNot
	Left  *Expr  // ExprAnd, ExprOr
	Right *Expr  // ExprAnd, ExprOr
}

type ExprKind int

const (
	ExprCheck ExprKind = iota
	ExprNot
	ExprAnd
	ExprOr
)

// Stmt is a control statement. IDs are unique within a rule.
type Stmt struct {
	ID      int
	Kind    StmtKind
	Check   *Check   // StmtCheck
	Reject  string   // StmtReject reason
	Forward *Forward // StmtForward
	Cond    *Expr    // StmtBranch
	Then    *Stmt    // StmtBranch, StmtTry (body)
	Else    *Stmt    // StmtBranch (nil falls through)
	First   *Stmt    // StmtSeq
	Second  *Stmt    // StmtSeq
	Handler *Stmt    // StmtTry (nil means the exception propagates)
}

type StmtKind int

const (
	StmtCheck StmtKind = iota
	StmtReject
	StmtForward
	StmtSeq
	StmtBranch
	StmtTry
)

// Forward is a dangerous forwarding node. It forwards only when every
// required mark already exists; trusted requirements need trusted marks.
type Forward struct {
	Name     string
	Required []Mark
}

// Rule is a complete WAF rule over Inputs boolean input positions.
type Rule struct {
	Inputs int
	Body   *Stmt
}

// Validate checks structural and indexing invariants.
func (r *Rule) Validate() error {
	if r.Inputs < 0 || r.Inputs > MaxInputs {
		return errf("rule has %d inputs, must be in [0,%d]", r.Inputs, MaxInputs)
	}
	v := &validator{inputs: r.Inputs, nodes: map[int]bool{}, checks: map[int]Check{}}
	if r.Body != nil {
		v.stmt(r.Body)
	}
	return v.err
}

type validator struct {
	err    error
	inputs int
	nodes  map[int]bool
	checks map[int]Check
}

func (v *validator) fail(format string, args ...any) {
	if v.err == nil {
		v.err = errf(format, args...)
	}
}

func (v *validator) node(id int) {
	if id <= 0 {
		v.fail("node id must be positive, got %d", id)
		return
	}
	if v.nodes[id] {
		v.fail("duplicate node id %d", id)
	}
	v.nodes[id] = true
}

func (v *validator) check(c *Check, owner int) {
	if c == nil {
		v.fail("node %d: missing check", owner)
		return
	}
	if c.ID < 0 || c.ID >= v.inputs {
		v.fail("check %d (node %d): input index out of range (inputs=%d)", c.ID, owner, v.inputs)
	}
	if c.Field == "" {
		v.fail("check %d (node %d): empty field", c.ID, owner)
	}
	if c.Emit < EmitNever || c.Emit > EmitOnFalse {
		v.fail("check %d (node %d): invalid emit mode", c.ID, owner)
	}
	if (c.Emit == EmitNever) != (c.MarkName == "") {
		v.fail("check %d (node %d): mark name requires an emit mode and vice versa", c.ID, owner)
	}
	if c.RaiseOn != nil && c.Emit != EmitNever {
		v.fail("check %d (node %d): raising check cannot also emit a mark", c.ID, owner)
	}
	if prev, ok := v.checks[c.ID]; ok {
		if prev != *c {
			v.fail("check %d: inconsistent re-declaration (field/emit/trust/raise differ)", c.ID)
		}
		return
	}
	v.checks[c.ID] = *c
}

func (v *validator) expr(e *Expr) {
	if e == nil {
		v.fail("nil expression")
		return
	}
	v.node(e.ID)
	switch e.Kind {
	case ExprCheck:
		v.check(e.Check, e.ID)
	case ExprNot:
		if e.Child == nil {
			v.fail("expr %d: not missing child", e.ID)
			return
		}
		v.expr(e.Child)
	case ExprAnd, ExprOr:
		if e.Left == nil || e.Right == nil {
			v.fail("expr %d: binary op missing operand", e.ID)
			return
		}
		v.expr(e.Left)
		v.expr(e.Right)
	default:
		v.fail("expr %d: unknown kind %d", e.ID, e.Kind)
	}
}

func (v *validator) stmt(s *Stmt) {
	if s == nil {
		v.fail("nil statement")
		return
	}
	v.node(s.ID)
	switch s.Kind {
	case StmtCheck:
		v.check(s.Check, s.ID)
	case StmtReject:
		if s.Reject == "" {
			v.fail("stmt %d: reject needs a reason", s.ID)
		}
	case StmtForward:
		if s.Forward == nil || s.Forward.Name == "" {
			v.fail("stmt %d: forward needs a name", s.ID)
			return
		}
		for _, m := range s.Forward.Required {
			if m.Field == "" || m.Name == "" {
				v.fail("stmt %d: forward requirement missing field/name", s.ID)
			}
		}
	case StmtSeq:
		if s.First == nil || s.Second == nil {
			v.fail("stmt %d: seq missing sub-statement", s.ID)
			return
		}
		v.stmt(s.First)
		v.stmt(s.Second)
	case StmtBranch:
		if s.Cond == nil || s.Then == nil {
			v.fail("stmt %d: branch missing cond/then", s.ID)
			return
		}
		v.expr(s.Cond)
		v.stmt(s.Then)
		if s.Else != nil {
			v.stmt(s.Else)
		}
	case StmtTry:
		if s.Then == nil {
			v.fail("stmt %d: try missing body", s.ID)
			return
		}
		v.stmt(s.Then)
		if s.Handler != nil {
			v.stmt(s.Handler)
		}
	default:
		v.fail("stmt %d: unknown kind %d", s.ID, s.Kind)
	}
}
