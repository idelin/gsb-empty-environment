package attrgram

import "fmt"

// Symbol is a variable binding: its static type and (after evaluation) its
// runtime value.
type Symbol struct {
	Type Type
	Val  interface{}
}

// Env is the symbol-table environment threaded through the AST as an
// inherited attribute (top-down) and extended by declarations as a
// synthesized attribute (bottom-up).
type Env map[string]Symbol

func (e Env) bind(name string, s Symbol) Env {
	next := make(Env, len(e)+1)
	for k, v := range e {
		next[k] = v
	}
	next[name] = s
	return next
}

// Diagnostic is a semantic error anchored to an AST node position.
type Diagnostic struct {
	Pos Pos
	Msg string
}

func (d Diagnostic) String() string { return fmt.Sprintf("%s: %s", d.Pos, d.Msg) }

// Result holds the outcome of evaluating a program's attributes.
type Result struct {
	Diags  []Diagnostic
	Prints []string
	values map[AttrID]interface{}
}

// TypeOf returns the synthesized type attribute of an expression.
func (r *Result) TypeOf(e Expr) Type {
	if t, ok := r.values[AttrID{e, "typ"}].(Type); ok {
		return t
	}
	return TUnknown
}

// ValueOf returns the synthesized value attribute of an expression.
func (r *Result) ValueOf(e Expr) interface{} { return r.values[AttrID{e, "val"}] }

// EnvAfter returns the synthesized environment after a statement executes.
func (r *Result) EnvAfter(s Stmt) Env {
	if env, ok := r.values[AttrID{s, "env.out"}].(Env); ok {
		return env
	}
	return nil
}

// Evaluate wires the attribute grammar for prog into an Engine and runs it.
// A non-nil error is a *CycleError indicating a cyclic attribute dependency;
// ordinary type/scope errors are reported in Result.Diags.
func Evaluate(prog *Program) (*Result, error) {
	ev := &evaluator{eng: NewEngine()}
	ev.program(prog)
	if err := ev.eng.Eval(); err != nil {
		return nil, err
	}
	return &Result{Diags: ev.diags, Prints: ev.prints, values: ev.eng.Values}, nil
}

type evaluator struct {
	eng    *Engine
	diags  []Diagnostic
	prints []string
}

func (ev *evaluator) diag(p Pos, format string, args ...interface{}) {
	ev.diags = append(ev.diags, Diagnostic{Pos: p, Msg: fmt.Sprintf(format, args...)})
}

// constAttr registers a dependency-free attribute holding a constant.
func (ev *evaluator) constAttr(n Node, name string, v interface{}) AttrID {
	id := AttrID{n, name}
	ev.eng.Add(id, nil, func(get func(AttrID) interface{}) interface{} { return v })
	return id
}

func (ev *evaluator) program(p *Program) {
	envID := ev.constAttr(p, "env.root", Env{})
	for _, s := range p.Stmts {
		envID = ev.stmt(s, envID)
	}
}

// stmt wires a statement. envID is the inherited environment entering the
// statement; it returns the synthesized environment leaving it.
func (ev *evaluator) stmt(s Stmt, envID AttrID) AttrID {
	switch st := s.(type) {
	case *Decl:
		typID, valID := ev.expr(st.Init, envID)
		out := AttrID{st, "env.out"}
		ev.eng.Add(out, []AttrID{envID, typID, valID}, func(get func(AttrID) interface{}) interface{} {
			env := get(envID).(Env)
			typ := get(typID).(Type)
			val := get(valID)
			if _, ok := env[st.Name]; ok {
				ev.diag(st.Pos, "variable %q redeclared", st.Name)
				return env
			}
			return env.bind(st.Name, Symbol{Type: typ, Val: val})
		})
		return out
	case *Print:
		typID, valID := ev.expr(st.Expr, envID)
		out := AttrID{st, "env.out"}
		ev.eng.Add(out, []AttrID{envID}, func(get func(AttrID) interface{}) interface{} {
			return get(envID)
		})
		ev.eng.Add(AttrID{st, "out"}, []AttrID{typID, valID}, func(get func(AttrID) interface{}) interface{} {
			if get(typID).(Type) == TUnknown {
				return nil
			}
			ev.prints = append(ev.prints, formatValue(get(valID)))
			return nil
		})
		return out
	}
	panic(fmt.Sprintf("attrgram: unsupported statement %T", s))
}

// expr wires an expression and returns its synthesized typ/val attributes.
func (ev *evaluator) expr(e Expr, envID AttrID) (typID, valID AttrID) {
	switch ex := e.(type) {
	case *IntLit:
		return ev.constAttr(ex, "typ", TInt), ev.constAttr(ex, "val", ex.Val)
	case *FloatLit:
		return ev.constAttr(ex, "typ", TFloat), ev.constAttr(ex, "val", ex.Val)
	case *BoolLit:
		return ev.constAttr(ex, "typ", TBool), ev.constAttr(ex, "val", ex.Val)
	case *Var:
		typID = AttrID{ex, "typ"}
		valID = AttrID{ex, "val"}
		ev.eng.Add(typID, []AttrID{envID}, func(get func(AttrID) interface{}) interface{} {
			sym, ok := get(envID).(Env)[ex.Name]
			if !ok {
				ev.diag(ex.Pos, "undefined variable %q", ex.Name)
				return TUnknown
			}
			return sym.Type
		})
		ev.eng.Add(valID, []AttrID{envID}, func(get func(AttrID) interface{}) interface{} {
			sym, ok := get(envID).(Env)[ex.Name]
			if !ok {
				return nil
			}
			return sym.Val
		})
		return typID, valID
	case *Binary:
		lt, lv := ev.expr(ex.L, envID)
		rt, rv := ev.expr(ex.R, envID)
		typID = AttrID{ex, "typ"}
		valID = AttrID{ex, "val"}
		ev.eng.Add(typID, []AttrID{lt, rt}, func(get func(AttrID) interface{}) interface{} {
			return ev.binType(ex, get(lt).(Type), get(rt).(Type))
		})
		ev.eng.Add(valID, []AttrID{typID, lv, rv}, func(get func(AttrID) interface{}) interface{} {
			if get(typID).(Type) == TUnknown {
				return nil
			}
			return ev.binVal(ex, get(lv), get(rv))
		})
		return typID, valID
	}
	panic(fmt.Sprintf("attrgram: unsupported expression %T", e))
}

func isNum(t Type) bool { return t == TInt || t == TFloat }

func (ev *evaluator) binType(b *Binary, l, r Type) Type {
	if l == TUnknown || r == TUnknown {
		return TUnknown
	}
	mismatch := func() Type {
		ev.diag(b.Pos, "type mismatch: cannot apply %q to %s and %s", b.Op, l, r)
		return TUnknown
	}
	switch b.Op {
	case "+", "-", "*", "/":
		if isNum(l) && l == r {
			return l
		}
		return mismatch()
	case "<", "<=", ">", ">=":
		if isNum(l) && l == r {
			return TBool
		}
		return mismatch()
	case "==", "!=":
		if l == r {
			return TBool
		}
		return mismatch()
	case "&&", "||":
		if l == TBool && r == TBool {
			return TBool
		}
		return mismatch()
	}
	ev.diag(b.Pos, "unknown operator %q", b.Op)
	return TUnknown
}

func (ev *evaluator) binVal(b *Binary, lv, rv interface{}) interface{} {
	switch b.Op {
	case "==":
		return lv == rv
	case "!=":
		return lv != rv
	case "&&":
		return lv.(bool) && rv.(bool)
	case "||":
		return lv.(bool) || rv.(bool)
	}
	if li, ok := lv.(int64); ok {
		ri := rv.(int64)
		switch b.Op {
		case "+":
			return li + ri
		case "-":
			return li - ri
		case "*":
			return li * ri
		case "/":
			if ri == 0 {
				ev.diag(b.Pos, "division by zero")
				return nil
			}
			return li / ri
		case "<":
			return li < ri
		case "<=":
			return li <= ri
		case ">":
			return li > ri
		case ">=":
			return li >= ri
		}
	}
	lf := lv.(float64)
	rf := rv.(float64)
	switch b.Op {
	case "+":
		return lf + rf
	case "-":
		return lf - rf
	case "*":
		return lf * rf
	case "/":
		if rf == 0 {
			ev.diag(b.Pos, "division by zero")
			return nil
		}
		return lf / rf
	case "<":
		return lf < rf
	case "<=":
		return lf <= rf
	case ">":
		return lf > rf
	case ">=":
		return lf >= rf
	}
	return nil
}

func formatValue(v interface{}) string {
	switch x := v.(type) {
	case int64:
		return fmt.Sprintf("%d", x)
	case float64:
		return fmt.Sprintf("%g", x)
	case bool:
		return fmt.Sprintf("%t", x)
	}
	return fmt.Sprintf("%v", v)
}
