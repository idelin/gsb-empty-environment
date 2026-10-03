package sandbox

import (
	"context"
	"fmt"
	"time"
)

// evalFrameMem 每层 eval 递归调用帧的内存计费(字节)。
const evalFrameMem = 256

// checkInterval 每累计多少步检查一次超时/取消,避免频繁读时钟。
const checkInterval = 64

type evaluator struct {
	cfg       Config
	env       map[string]any
	ctx       context.Context
	deadline  time.Time
	steps     int
	nextCheck int
	memUsed   int64
	depth     int
	sb        *Sandbox
}

// charge 计入 n 步指令预算;超额立即返回 ErrBudgetExceeded。
func (e *evaluator) charge(n int) error {
	e.steps += n
	if e.steps > e.cfg.MaxSteps {
		return ErrBudgetExceeded
	}
	if e.steps >= e.nextCheck {
		e.nextCheck = e.steps + checkInterval
		if err := e.ctx.Err(); err != nil {
			return ErrTimeout
		}
		if !e.deadline.IsZero() && time.Now().After(e.deadline) {
			return ErrTimeout
		}
	}
	return nil
}

// alloc 计入 n 字节内存配额;超额立即返回 ErrMemoryExceeded。
func (e *evaluator) alloc(n int64) error {
	e.memUsed += n
	if e.memUsed > e.cfg.MaxMemoryBytes {
		return ErrMemoryExceeded
	}
	return nil
}

func (e *evaluator) release(n int64) { e.memUsed -= n }

func (e *evaluator) eval(node Node) (any, error) {
	if err := e.charge(1); err != nil {
		return nil, err
	}
	switch n := node.(type) {
	case *NumberLit:
		return n.Value, nil
	case *BoolLit:
		return n.Value, nil
	case *StringLit:
		if err := e.alloc(int64(len(n.Value))); err != nil {
			return nil, err
		}
		return n.Value, nil
	case *Ident:
		v, ok := e.env[n.Name]
		if !ok {
			return nil, fmt.Errorf("%w: undefined variable %q", ErrEval, n.Name)
		}
		return v, nil
	case *UnaryExpr:
		x, err := e.eval(n.X)
		if err != nil {
			return nil, err
		}
		switch n.Op {
		case "-":
			f, ok := x.(float64)
			if !ok {
				return nil, fmt.Errorf("%w: unary - requires number, got %T", ErrEval, x)
			}
			return -f, nil
		case "!":
			b, ok := x.(bool)
			if !ok {
				return nil, fmt.Errorf("%w: unary ! requires bool, got %T", ErrEval, x)
			}
			return !b, nil
		}
	case *BinaryExpr:
		return e.evalBinary(n)
	case *CallExpr:
		args := make([]any, 0, len(n.Args))
		for _, a := range n.Args {
			v, err := e.eval(a)
			if err != nil {
				return nil, err
			}
			args = append(args, v)
		}
		return e.sb.callBuiltin(e, n.Name, args)
	}
	return nil, fmt.Errorf("%w: unknown AST node %T", ErrEval, node)
}

func (e *evaluator) evalBinary(n *BinaryExpr) (any, error) {
	// 短路求值
	if n.Op == "&&" || n.Op == "||" {
		l, err := e.eval(n.L)
		if err != nil {
			return nil, err
		}
		lb, ok := l.(bool)
		if !ok {
			return nil, fmt.Errorf("%w: %s requires bool operands, got %T", ErrEval, n.Op, l)
		}
		if n.Op == "&&" && !lb {
			return false, nil
		}
		if n.Op == "||" && lb {
			return true, nil
		}
		r, err := e.eval(n.R)
		if err != nil {
			return nil, err
		}
		rb, ok := r.(bool)
		if !ok {
			return nil, fmt.Errorf("%w: %s requires bool operands, got %T", ErrEval, n.Op, r)
		}
		return rb, nil
	}

	l, err := e.eval(n.L)
	if err != nil {
		return nil, err
	}
	r, err := e.eval(n.R)
	if err != nil {
		return nil, err
	}

	switch n.Op {
	case "==":
		return valueEqual(l, r), nil
	case "!=":
		return !valueEqual(l, r), nil
	}

	// 字符串拼接
	if n.Op == "+" {
		if ls, ok := l.(string); ok {
			rs, ok := r.(string)
			if !ok {
				return nil, fmt.Errorf("%w: cannot concatenate string and %T", ErrEval, r)
			}
			if err := e.alloc(int64(len(ls) + len(rs))); err != nil {
				return nil, err
			}
			return ls + rs, nil
		}
	}

	lf, lok := l.(float64)
	rf, rok := r.(float64)
	if !lok || !rok {
		return nil, fmt.Errorf("%w: operator %s requires numbers, got %T and %T", ErrEval, n.Op, l, r)
	}
	switch n.Op {
	case "+":
		return lf + rf, nil
	case "-":
		return lf - rf, nil
	case "*":
		return lf * rf, nil
	case "/":
		if rf == 0 {
			return nil, fmt.Errorf("%w: division by zero", ErrEval)
		}
		return lf / rf, nil
	case "%":
		if rf == 0 {
			return nil, fmt.Errorf("%w: modulo by zero", ErrEval)
		}
		return float64(int64(lf) % int64(rf)), nil
	case "<":
		return lf < rf, nil
	case "<=":
		return lf <= rf, nil
	case ">":
		return lf > rf, nil
	case ">=":
		return lf >= rf, nil
	}
	return nil, fmt.Errorf("%w: unknown operator %q", ErrEval, n.Op)
}

func valueEqual(a, b any) bool {
	switch av := a.(type) {
	case float64:
		bv, ok := b.(float64)
		return ok && av == bv
	case string:
		bv, ok := b.(string)
		return ok && av == bv
	case bool:
		bv, ok := b.(bool)
		return ok && av == bv
	}
	return false
}
