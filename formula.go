package main

import (
	"fmt"
	"strings"
)

// Formula 是弱 HML 区分公式，可在一个 LTS 的状态上求值。
type Formula interface {
	String() string
	Eval(l *LTS, s string) bool
}

// True 是永真公式。
type True struct{}

func (True) String() string             { return "T" }
func (True) Eval(l *LTS, s string) bool { return true }

// And 是合取。
type And struct{ Parts []Formula }

func (a And) String() string {
	ps := make([]string, len(a.Parts))
	for i, f := range a.Parts {
		ps[i] = f.String()
	}
	return "(" + strings.Join(ps, " /\\ ") + ")"
}

func (a And) Eval(l *LTS, s string) bool {
	for _, f := range a.Parts {
		if !f.Eval(l, s) {
			return false
		}
	}
	return true
}

// Not 是否定。
type Not struct{ Sub Formula }

func (n Not) String() string             { return "~" + n.Sub.String() }
func (n Not) Eval(l *LTS, s string) bool { return !n.Sub.Eval(l, s) }

// Diamond 是弱模态 <<a>>：a 为可见动作时表示 tau*·a·tau*，
// a == Tau 时表示 tau*（内部动作不构成可见进展）。
type Diamond struct {
	Action string
	Sub    Formula
}

func (d Diamond) String() string {
	return "<<" + d.Action + ">>" + d.Sub.String()
}

func (d Diamond) Eval(l *LTS, s string) bool {
	for _, t := range l.WeakMove(s, d.Action) {
		if d.Sub.Eval(l, t) {
			return true
		}
	}
	return false
}

// Distinguish 为不在最大关系中的状态对 (p, q) 构造弱 HML 区分公式，
// 返回的公式在 p（系统一）上为真、在 q（系统二）上为假。
// 递归按剔除轮次严格下降，因此必然终止。
func (r *BisimResult) Distinguish(p, q string) (Formula, error) {
	rem, ok := r.removal[Pair{p, q}]
	if !ok {
		return nil, fmt.Errorf("(%s, %s) 在最大弱互模拟关系内，不存在区分公式", p, q)
	}
	if rem.Attacker == 1 {
		// P 走出 rem.Move，Q 的每个弱响应 r 都与目标不可区分失败：
		// 公式 <<a>>(/\ Distinguish(目标, r)) 在 P 为真、在 Q 为假。
		parts := make([]Formula, 0, len(rem.Responders))
		for _, q2 := range rem.Responders {
			sub, err := r.Distinguish(rem.Move.To, q2)
			if err != nil {
				return nil, err
			}
			parts = append(parts, sub)
		}
		return Diamond{rem.Move.Label, andOf(parts)}, nil
	}
	// Q 走出 rem.Move，P 的每个弱响应 r 都无法匹配：
	// 公式 ~<<a>>(/\ ~Distinguish(r, 目标)) 在 P 为真、在 Q 为假。
	parts := make([]Formula, 0, len(rem.Responders))
	for _, p2 := range rem.Responders {
		sub, err := r.Distinguish(p2, rem.Move.To)
		if err != nil {
			return nil, err
		}
		parts = append(parts, Not{sub})
	}
	return Not{Diamond{rem.Move.Label, andOf(parts)}}, nil
}

func andOf(parts []Formula) Formula {
	if len(parts) == 0 {
		return True{}
	}
	return And{parts}
}
