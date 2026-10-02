package mini

import (
	"fmt"

	"attrgram/attr"
)

// Attribute names used by the mini language.
const (
	attrEnv    = "env"    // inherited: symbol table visible at the node
	attrType   = "type"   // synthesized: type of an expression
	attrEnvOut = "envOut" // synthesized: symbol table after a statement
)

func key(n Node, name string) attr.Key { return attr.Key{Node: n, Name: name} }

// Diagnostic is a semantic error located at an AST node's source position.
type Diagnostic struct {
	Pos Pos
	Msg string
}

func (d Diagnostic) String() string { return fmt.Sprintf("%s: %s", d.Pos, d.Msg) }

// Result of a semantic analysis run.
type Result struct {
	Env         *Env // final symbol table (synthesized envOut of the program)
	Diagnostics []Diagnostic
}

// Check collects the attribute rules of every AST node, evaluates them in
// dependency (topological) order and returns the final environment plus all
// type diagnostics. A cyclic attribute dependency is reported as an
// *attr.CycleError.
func Check(prog *Program) (*Result, error) {
	c := &compiler{ev: attr.NewEvaluator()}
	// Seed the inherited env attribute of the root with the empty environment.
	c.ev.Set(key(prog, attrEnv), NewEnv())
	var walk func(n Node)
	walk = func(n Node) {
		n.rules(c)
		for _, ch := range n.Children() {
			walk(ch)
		}
	}
	walk(prog)
	if err := c.ev.Evaluate(); err != nil {
		return nil, err
	}
	v, _ := c.ev.Get(key(prog, attrEnvOut))
	return &Result{Env: v.(*Env), Diagnostics: c.diags}, nil
}

type compiler struct {
	ev    *attr.Evaluator
	diags []Diagnostic
}

func (c *compiler) rule(target attr.Key, deps []attr.Key, eval func(attr.Getter) attr.Value) {
	if err := c.ev.AddRule(attr.Rule{Target: target, Deps: deps, Eval: eval}); err != nil {
		panic(err)
	}
}

// pass defines target as a copy of dep (used to thread inherited attributes
// down and synthesized attributes up unchanged).
func (c *compiler) pass(target, dep attr.Key) {
	c.rule(target, []attr.Key{dep}, func(g attr.Getter) attr.Value { return g(dep) })
}

func (c *compiler) errorf(pos Pos, format string, args ...any) {
	c.diags = append(c.diags, Diagnostic{Pos: pos, Msg: fmt.Sprintf(format, args...)})
}

// --- statement rules ---

func (p *Program) rules(c *compiler) {
	// Inherited attribute: each statement sees the env synthesized by its
	// predecessor (top-down threading of the symbol table).
	for i, s := range p.Stmts {
		dep := key(p, attrEnv)
		if i > 0 {
			dep = key(p.Stmts[i-1], attrEnvOut)
		}
		c.pass(key(s, attrEnv), dep)
	}
	last := key(p, attrEnv)
	if len(p.Stmts) > 0 {
		last = key(p.Stmts[len(p.Stmts)-1], attrEnvOut)
	}
	c.pass(key(p, attrEnvOut), last)
}

func (d *VarDecl) rules(c *compiler) {
	// Inherited: the initializer is evaluated in the declaration's env.
	c.pass(key(d.Init, attrEnv), key(d, attrEnv))
	// Synthesized: env extended with the declared variable.
	c.rule(key(d, attrEnvOut), []attr.Key{key(d, attrEnv), key(d.Init, attrType)},
		func(g attr.Getter) attr.Value {
			env := g(key(d, attrEnv)).(*Env)
			t := g(key(d.Init, attrType)).(Type)
			if _, exists := env.Lookup(d.Name); exists {
				c.errorf(d.Pos(), "variable %q redeclared", d.Name)
			}
			if d.HasType {
				if t != TError && !assignable(d.DeclType, t) {
					c.errorf(d.Pos(), "cannot use %s value as %s in declaration of %q",
						t, d.DeclType, d.Name)
				}
				t = d.DeclType
			}
			return env.Extend(d.Name, t)
		})
}

func (s *ExprStmt) rules(c *compiler) {
	c.pass(key(s.X, attrEnv), key(s, attrEnv))
	c.pass(key(s, attrEnvOut), key(s, attrEnv))
}

func (s *PrintStmt) rules(c *compiler) {
	c.pass(key(s.X, attrEnv), key(s, attrEnv))
	c.pass(key(s, attrEnvOut), key(s, attrEnv))
}

// --- expression rules ---

func (l *Literal) rules(c *compiler) {
	c.rule(key(l, attrType), nil, func(attr.Getter) attr.Value { return l.Kind })
}

func (id *Ident) rules(c *compiler) {
	c.rule(key(id, attrType), []attr.Key{key(id, attrEnv)},
		func(g attr.Getter) attr.Value {
			env := g(key(id, attrEnv)).(*Env)
			t, ok := env.Lookup(id.Name)
			if !ok {
				c.errorf(id.Pos(), "undefined variable %q", id.Name)
				return TError
			}
			return t
		})
}

func (u *Unary) rules(c *compiler) {
	c.pass(key(u.X, attrEnv), key(u, attrEnv))
	c.rule(key(u, attrType), []attr.Key{key(u.X, attrType)},
		func(g attr.Getter) attr.Value {
			t := g(key(u.X, attrType)).(Type)
			if t == TError {
				return TError
			}
			switch u.Op {
			case "-":
				if numeric(t) {
					return t
				}
			case "!":
				if t == TBool {
					return TBool
				}
			}
			c.errorf(u.Pos(), "cannot apply unary operator %q to %s", u.Op, t)
			return TError
		})
}

func (b *Binary) rules(c *compiler) {
	c.pass(key(b.L, attrEnv), key(b, attrEnv))
	c.pass(key(b.R, attrEnv), key(b, attrEnv))
	c.rule(key(b, attrType), []attr.Key{key(b.L, attrType), key(b.R, attrType)},
		func(g attr.Getter) attr.Value {
			lt := g(key(b.L, attrType)).(Type)
			rt := g(key(b.R, attrType)).(Type)
			return c.binaryType(b, lt, rt)
		})
}

func (c *compiler) binaryType(b *Binary, l, r Type) Type {
	if l == TError || r == TError {
		return TError // suppress cascading errors
	}
	switch b.Op {
	case "+":
		if numeric(l) && numeric(r) {
			return promote(l, r)
		}
		if l == TString && r == TString {
			return TString
		}
	case "-", "*", "/":
		if numeric(l) && numeric(r) {
			return promote(l, r)
		}
	case "%":
		if l == TInt && r == TInt {
			return TInt
		}
	case "<", "<=", ">", ">=":
		if numeric(l) && numeric(r) {
			return TBool
		}
		if l == TString && r == TString {
			return TBool
		}
	case "==", "!=":
		if l == r || numeric(l) && numeric(r) {
			return TBool
		}
	case "&&", "||":
		if l == TBool && r == TBool {
			return TBool
		}
	}
	c.errorf(b.Pos(), "cannot apply operator %q to %s and %s", b.Op, l, r)
	return TError
}
