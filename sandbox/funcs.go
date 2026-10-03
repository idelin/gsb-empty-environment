package sandbox

import (
	"fmt"
	"math"
)

// callBuiltin 调用白名单内建函数。所有函数均为纯函数,
// 不接触宿主环境的文件、网络、进程等任何资源。
func (s *Sandbox) callBuiltin(e *evaluator, name string, args []any) (any, error) {
	switch name {
	case "abs":
		f, err := numArg(name, args, 0, 1)
		if err != nil {
			return nil, err
		}
		return math.Abs(f), nil
	case "floor":
		f, err := numArg(name, args, 0, 1)
		if err != nil {
			return nil, err
		}
		return math.Floor(f), nil
	case "ceil":
		f, err := numArg(name, args, 0, 1)
		if err != nil {
			return nil, err
		}
		return math.Ceil(f), nil
	case "round":
		f, err := numArg(name, args, 0, 1)
		if err != nil {
			return nil, err
		}
		return math.Round(f), nil
	case "min", "max":
		if len(args) == 0 {
			return nil, fmt.Errorf("%w: %s requires at least 1 argument", ErrEval, name)
		}
		best, err := numArg(name, args, 0, -1)
		if err != nil {
			return nil, err
		}
		for i := 1; i < len(args); i++ {
			f, err := numArg(name, args, i, -1)
			if err != nil {
				return nil, err
			}
			if name == "min" && f < best {
				best = f
			}
			if name == "max" && f > best {
				best = f
			}
		}
		return best, nil
	case "len":
		if len(args) != 1 {
			return nil, fmt.Errorf("%w: len requires 1 argument", ErrEval)
		}
		str, ok := args[0].(string)
		if !ok {
			return nil, fmt.Errorf("%w: len requires string, got %T", ErrEval, args[0])
		}
		return float64(len(str)), nil
	case "concat":
		total := 0
		for _, a := range args {
			str, ok := a.(string)
			if !ok {
				return nil, fmt.Errorf("%w: concat requires strings, got %T", ErrEval, a)
			}
			total += len(str)
		}
		if err := e.alloc(int64(total)); err != nil {
			return nil, err
		}
		out := ""
		for _, a := range args {
			out += a.(string)
		}
		return out, nil
	case "eval":
		// 沙箱内自求值:对字符串表达式在同一沙箱内重新解析、校验、执行,
		// 与外层共享同一份步数/内存/超时预算,因此无限递归必然被预算中断。
		if len(args) != 1 {
			return nil, fmt.Errorf("%w: eval requires 1 argument", ErrEval)
		}
		src, ok := args[0].(string)
		if !ok {
			return nil, fmt.Errorf("%w: eval requires string, got %T", ErrEval, args[0])
		}
		if e.depth+1 > e.cfg.MaxEvalDepth {
			return nil, fmt.Errorf("%w: eval recursion depth exceeds limit %d", ErrBudgetExceeded, e.cfg.MaxEvalDepth)
		}
		if err := e.alloc(evalFrameMem); err != nil {
			return nil, err
		}
		defer e.release(evalFrameMem)
		node, err := parse(src, e.charge)
		if err != nil {
			return nil, err
		}
		if err := s.validate(node, e); err != nil {
			return nil, err
		}
		e.depth++
		defer func() { e.depth-- }()
		return e.eval(node)
	}
	return nil, fmt.Errorf("%w: unauthorized function %q", ErrValidation, name)
}

func numArg(name string, args []any, i, want int) (float64, error) {
	if want > 0 && len(args) != want {
		return 0, fmt.Errorf("%w: %s requires %d argument(s), got %d", ErrEval, name, want, len(args))
	}
	f, ok := args[i].(float64)
	if !ok {
		return 0, fmt.Errorf("%w: %s requires number argument, got %T", ErrEval, name, args[i])
	}
	return f, nil
}
