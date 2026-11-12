package cond

// Simplify 对表达式做保持三态语义的逻辑化简：
// 双重否定消除、De Morgan 推进、合取/析取压平、
// 单子句收缩与结构去重。全部规则在 Kleene 强三态逻辑下成立，
// 并由 CheckEquivalent 做有限穷举验证。
func Simplify(e Expr) Expr {
	switch n := e.(type) {
	case Neg:
		return simplifyNeg(Simplify(n.Arg))
	case Conj:
		args := make([]Expr, 0, len(n.Args))
		for _, a := range n.Args {
			args = appendFlat(args, Simplify(a), true)
		}
		args = dedupe(args)
		if len(args) == 1 {
			return args[0]
		}
		return Conj{Args: args}
	case Disj:
		args := make([]Expr, 0, len(n.Args))
		for _, a := range n.Args {
			args = appendFlat(args, Simplify(a), false)
		}
		args = dedupe(args)
		if len(args) == 1 {
			return args[0]
		}
		return Disj{Args: args}
	default:
		return e
	}
}

// simplifyNeg 把否定向叶子推进。
func simplifyNeg(arg Expr) Expr {
	switch n := arg.(type) {
	case Neg:
		// 双重否定：¬¬x ≡ x（Kleene 否定是对合）。
		return n.Arg
	case Conj:
		// De Morgan：¬(a∧b…) ≡ ¬a∨¬b…
		args := make([]Expr, len(n.Args))
		for i, a := range n.Args {
			args[i] = simplifyNeg(a)
		}
		return Disj{Args: args}
	case Disj:
		// De Morgan：¬(a∨b…) ≡ ¬a∧¬b…
		args := make([]Expr, len(n.Args))
		for i, a := range n.Args {
			args[i] = simplifyNeg(a)
		}
		return Conj{Args: args}
	default:
		return Neg{Arg: arg}
	}
}

// appendFlat 压平同类结合子：and(a, and(b,c)) ≡ and(a,b,c)。
func appendFlat(dst []Expr, e Expr, conj bool) []Expr {
	switch n := e.(type) {
	case Conj:
		if conj {
			return append(dst, n.Args...)
		}
	case Disj:
		if !conj {
			return append(dst, n.Args...)
		}
	}
	return append(dst, e)
}

// dedupe 按规范化文本去除重复子句：x∧x ≡ x，x∨x ≡ x。
func dedupe(args []Expr) []Expr {
	seen := make(map[string]struct{}, len(args))
	out := args[:0]
	for _, a := range args {
		key := a.String()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, a)
	}
	return out
}
