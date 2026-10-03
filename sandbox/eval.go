package sandbox

import (
	"fmt"
	"math"
	"strconv"
	"time"
)

// budget tracks consumed instruction steps and allocation units, plus a
// wall-clock deadline. All counters are checked on every consumed unit so a
// runaway expression is interrupted deterministically.
type budget struct {
	maxSteps  uint64
	maxAllocs uint64
	deadline  time.Time
	steps     uint64
	allocs    uint64
}

func (b *budget) step() error {
	b.steps++
	if b.steps > b.maxSteps {
		return ErrBudgetExceeded
	}
	// Cheap periodic deadline check (every 64 steps).
	if b.steps&0x3F == 0 && time.Now().After(b.deadline) {
		return ErrTimeout
	}
	return nil
}

func (b *budget) alloc(units uint64) error {
	b.allocs += units
	if b.allocs > b.maxAllocs {
		return ErrMemoryExceeded
	}
	return nil
}

type evalCtx struct {
	b     *budget
	vars  map[string]any
	funcs map[string]Func
	rules map[string]*node
	self  string
}

func (c *evalCtx) eval(n *node) (any, error) {
	// Every AST node evaluation consumes one instruction step.
	if err := c.b.step(); err != nil {
		return nil, err
	}
	switch n.kind {
	case nNumber:
		return strconv.ParseFloat(n.val, 64)
	case nString:
		return n.val, nil
	case nBool:
		return n.val == "true", nil
	case nIdent:
		if n.val == "self" {
			return c.self, nil
		}
		v, ok := c.vars[n.val]
		if !ok {
			return nil, fmt.Errorf("undefined variable %q", n.val)
		}
		return v, nil
	case nUnary:
		return c.evalUnary(n)
	case nBinary:
		return c.evalBinary(n)
	case nCall:
		return c.evalCall(n)
	}
	return nil, fmt.Errorf("corrupt AST node kind %d", n.kind)
}

func (c *evalCtx) evalUnary(n *node) (any, error) {
	v, err := c.eval(n.args[0])
	if err != nil {
		return nil, err
	}
	if err := c.b.alloc(1); err != nil {
		return nil, err
	}
	switch n.val {
	case "-":
		f, ok := v.(float64)
		if !ok {
			return nil, fmt.Errorf("unary '-' requires a number, got %T", v)
		}
		return -f, nil
	case "!":
		b, ok := v.(bool)
		if !ok {
			return nil, fmt.Errorf("operator '!' requires a bool, got %T", v)
		}
		return !b, nil
	}
	return nil, fmt.Errorf("unknown unary operator %q", n.val)
}

func (c *evalCtx) evalBinary(n *node) (any, error) {
	// Short-circuiting logical operators.
	switch n.val {
	case "&&", "||":
		l, err := c.eval(n.args[0])
		if err != nil {
			return nil, err
		}
		lb, ok := l.(bool)
		if !ok {
			return nil, fmt.Errorf("operator %q requires bool operands, got %T", n.val, l)
		}
		if n.val == "&&" && !lb {
			return false, nil
		}
		if n.val == "||" && lb {
			return true, nil
		}
		r, err := c.eval(n.args[1])
		if err != nil {
			return nil, err
		}
		rb, ok := r.(bool)
		if !ok {
			return nil, fmt.Errorf("operator %q requires bool operands, got %T", n.val, r)
		}
		return rb, nil
	}
	l, err := c.eval(n.args[0])
	if err != nil {
		return nil, err
	}
	r, err := c.eval(n.args[1])
	if err != nil {
		return nil, err
	}
	return c.applyOp(n.val, l, r)
}

func (c *evalCtx) applyOp(op string, l, r any) (any, error) {
	if ls, ok := l.(string); ok {
		rs, ok := r.(string)
		if !ok {
			return nil, fmt.Errorf("cannot apply %q between string and %T", op, r)
		}
		switch op {
		case "+":
			// Charge allocation units proportional to the new string.
			if err := c.b.alloc(uint64(len(ls) + len(rs))); err != nil {
				return nil, err
			}
			return ls + rs, nil
		case "==":
			return ls == rs, nil
		case "!=":
			return ls != rs, nil
		case "<":
			return ls < rs, nil
		case "<=":
			return ls <= rs, nil
		case ">":
			return ls > rs, nil
		case ">=":
			return ls >= rs, nil
		}
		return nil, fmt.Errorf("unsupported string operator %q", op)
	}
	if lb, ok := l.(bool); ok {
		rb, ok := r.(bool)
		if !ok {
			return nil, fmt.Errorf("cannot apply %q between bool and %T", op, r)
		}
		switch op {
		case "==":
			return lb == rb, nil
		case "!=":
			return lb != rb, nil
		}
		return nil, fmt.Errorf("unsupported bool operator %q", op)
	}
	lf, lok := l.(float64)
	rf, rok := r.(float64)
	if !lok || !rok {
		return nil, fmt.Errorf("operator %q requires numbers, got %T and %T", op, l, r)
	}
	if err := c.b.alloc(1); err != nil {
		return nil, err
	}
	switch op {
	case "+":
		return lf + rf, nil
	case "-":
		return lf - rf, nil
	case "*":
		return lf * rf, nil
	case "/":
		if rf == 0 {
			return nil, fmt.Errorf("division by zero")
		}
		return lf / rf, nil
	case "%":
		if rf == 0 {
			return nil, fmt.Errorf("modulo by zero")
		}
		return math.Mod(lf, rf), nil
	case "==":
		return lf == rf, nil
	case "!=":
		return lf != rf, nil
	case "<":
		return lf < rf, nil
	case "<=":
		return lf <= rf, nil
	case ">":
		return lf > rf, nil
	case ">=":
		return lf >= rf, nil
	}
	return nil, fmt.Errorf("unknown operator %q", op)
}

func (c *evalCtx) evalCall(n *node) (any, error) {
	// "eval" is a sandbox built-in special form: it evaluates another named
	// rule of the same tenant. It is the only way to get recursion, and every
	// recursive step is still charged against the same shared budget, so
	// infinite recursion such as eval(self) is interrupted deterministically.
	if n.val == "eval" {
		if len(n.args) != 1 {
			return nil, fmt.Errorf("eval expects exactly 1 argument, got %d", len(n.args))
		}
		nameV, err := c.eval(n.args[0])
		if err != nil {
			return nil, err
		}
		name, ok := nameV.(string)
		if !ok {
			return nil, fmt.Errorf("eval expects a rule-name string, got %T", nameV)
		}
		target, ok := c.rules[name]
		if !ok {
			return nil, fmt.Errorf("eval: unknown rule %q", name)
		}
		if err := c.b.alloc(1); err != nil {
			return nil, err
		}
		sub := *c
		sub.self = name
		return sub.eval(target)
	}
	// Defense in depth: validation already rejected non-whitelisted calls.
	fn, ok := c.funcs[n.val]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnauthorizedFunction, n.val)
	}
	args := make([]any, 0, len(n.args))
	for _, a := range n.args {
		v, err := c.eval(a)
		if err != nil {
			return nil, err
		}
		args = append(args, v)
	}
	if err := c.b.alloc(uint64(len(args) + 1)); err != nil {
		return nil, err
	}
	return fn(args)
}
