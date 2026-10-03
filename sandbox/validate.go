package sandbox

import "fmt"

// validate 对 AST 做静态校验:
//  1. 函数调用必须在白名单内(严禁调用宿主未授权函数);
//  2. AST 深度与节点数不得超过配额;
//  3. 校验开销计入指令步数预算。
func (s *Sandbox) validate(node Node, ev *evaluator) error {
	_, err := s.walk(node, 1, ev)
	return err
}

func (s *Sandbox) walk(node Node, depth int, ev *evaluator) (int, error) {
	if depth > s.cfg.MaxASTDepth {
		return 0, fmt.Errorf("%w: AST depth %d exceeds limit %d", ErrValidation, depth, s.cfg.MaxASTDepth)
	}
	if err := ev.charge(1); err != nil {
		return 0, err
	}
	total := 1
	check := func(n int, err error) (int, error) {
		if err != nil {
			return 0, err
		}
		total += n
		if total > s.cfg.MaxNodes {
			return 0, fmt.Errorf("%w: AST node count exceeds limit %d", ErrValidation, s.cfg.MaxNodes)
		}
		return total, nil
	}
	switch n := node.(type) {
	case *NumberLit, *StringLit, *BoolLit, *Ident:
		return total, nil
	case *UnaryExpr:
		return check(s.walk(n.X, depth+1, ev))
	case *BinaryExpr:
		if _, err := check(s.walk(n.L, depth+1, ev)); err != nil {
			return 0, err
		}
		return check(s.walk(n.R, depth+1, ev))
	case *CallExpr:
		if !s.cfg.AllowedFuncs[n.Name] {
			return 0, fmt.Errorf("%w: unauthorized function %q", ErrValidation, n.Name)
		}
		for _, a := range n.Args {
			if _, err := check(s.walk(a, depth+1, ev)); err != nil {
				return 0, err
			}
		}
		return total, nil
	}
	return 0, fmt.Errorf("%w: unknown AST node %T", ErrValidation, node)
}
