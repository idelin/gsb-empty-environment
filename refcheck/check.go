package main

import (
	"fmt"
	"strings"
)

// ---------- 抽取检查 ----------
// 判定把表达式 e 的两次出现 O1、O2（O1 按求值序在前）抽取为
//   __t = e   （插入在 O1 所属语句之前）
//   O1、O2 替换为 __t
// 是否保持：求值次数（效果调用）、首错、读取版本。
// 拒绝时给出依赖路径。

type Rejection struct {
	Kind   string
	Detail string
	Path   []string
}

func (r *Rejection) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "拒绝[%s] %s\n  依赖路径:", r.Kind, r.Detail)
	for _, p := range r.Path {
		fmt.Fprintf(&b, "\n    -> %s", p)
	}
	return b.String()
}

type writeHit struct {
	v   string
	loc []int
	src string
}

func CheckExtract(prog []Stmt, ft FuncTable, e Expr, o1, o2 []int) *Rejection {
	l1, l2 := locStr(prog, o1), locStr(prog, o2)

	if isPrefix(o1, o2) || isPrefix(o2, o1) {
		return &Rejection{
			Kind:   "出现位置嵌套",
			Detail: "两次出现互为祖先，无法在不改变结构的前提下抽取",
			Path:   []string{"O1 " + l1, "O2 " + l2},
		}
	}

	// C1：e 内含写副作用调用 —— 提取后该调用求值 2->1 次，写丢失
	for _, f := range callsIn(e) {
		if len(ft[f].Writes) > 0 {
			return &Rejection{
				Kind:   "求值次数改变",
				Detail: fmt.Sprintf("表达式含调用 %s（写 %v），提取后求值次数 2->1，写副作用丢失", f, ft[f].Writes),
				Path: []string{
					"O1 " + l1 + "  [调用 " + f + " 求值]",
					"O2 " + l2 + "  [调用 " + f + " 求值，提取后被合并]",
				},
			}
		}
	}

	sp := enclosingStmt(prog, o1) // O1 所属（将被提升的）语句
	rel := o1[len(sp):]           // O1 在该语句内的相对路径
	sn := nodeAt(prog, sp)        // 语句节点
	reads := readsOf(e, ft)

	// C2：e 可能抛错 —— O1 必须是语句内首个求值位置，
	// 否则提升后异常先于语句内之前的求值/事件，首错及其前事件序列改变
	if tc := throwCalls(e, ft); len(tc) > 0 && !isFirstEval(sn, rel) {
		return &Rejection{
			Kind:   "首错改变",
			Detail: fmt.Sprintf("表达式可能抛错（调用 %v），但 O1 不是所在语句的首个求值位置；提升会改变错误发生点之前的事件", tc),
			Path: []string{
				"提升点 " + locStr(prog, sp) + " 之前",
				"O1 " + l1 + "  [语句内存在先于 O1 的求值]",
			},
		}
	}

	// C3：提升点（语句首）到 O1 之间存在对 reads(e) 的写 —— 绑定读到旧版本
	for _, w := range collectWritesBefore(sn, ft, sp, o1) {
		if reads[w.v] {
			return &Rejection{
				Kind:   "读取版本改变",
				Detail: fmt.Sprintf("表达式读取 %s，提升到语句首后绑定发生在「%s」之前，读到旧版本", w.v, w.src),
				Path: []string{
					"提升点 " + locStr(prog, sp) + " 之前  [__t = e 在此绑定]",
					fmt.Sprintf("写 %s @ %s  [%s]", w.v, locStr(prog, w.loc), w.src),
					"O1 " + l1 + "  [原求值点]",
				},
			}
		}
	}

	// C4：绑定语句必须支配 O2（O2 求值时绑定语句必然已执行）
	if ok, desc, failLoc := stmtDominates(prog, sp, o2); !ok {
		return &Rejection{
			Kind:   "支配性失败",
			Detail: "O2 可能在绑定语句未执行时求值（__t 未绑定/过期）：" + desc,
			Path: []string{
				"绑定语句 " + locStr(prog, sp),
				"绕过点 " + locStr(prog, failLoc) + "  [" + desc + "]",
				"O2 " + l2,
			},
		}
	}

	// C5：O1 与 O2 之间存在对 reads(e) 的写 —— 两次求值读到不同版本
	for _, w := range collectBetween(prog, ft, o1, o2) {
		if reads[w.v] {
			return &Rejection{
				Kind:   "读取版本改变",
				Detail: fmt.Sprintf("表达式读取 %s，两次求值之间存在对 %s 的写", w.v, w.v),
				Path: []string{
					"O1 " + l1,
					fmt.Sprintf("写 %s @ %s  [%s]", w.v, locStr(prog, w.loc), w.src),
					"O2 " + l2,
				},
			}
		}
	}

	return nil
}

// enclosingStmt：包含该出现位置、且直接位于某语句列表中的语句（提升插入点）
func enclosingStmt(prog []Stmt, o []int) []int {
	for k := len(o) - 1; k >= 1; k-- {
		if _, ok := nodeAt(prog, o[:k]).(Stmt); ok {
			return o[:k]
		}
	}
	return nil
}

// isFirstEval：rel 指向的位置是否为该语句内首个被求值的表达式
func isFirstEval(node interface{}, rel []int) bool {
	if len(rel) == 0 {
		return true
	}
	i := rel[0]
	switch n := node.(type) {
	case *Assign:
		return isFirstEval(n.E, rel[1:])
	case *Print:
		return isFirstEval(n.E, rel[1:])
	case *If:
		return i == 0 && isFirstEval(n.C, rel[1:])
	case *Add:
		return i == 0 && isFirstEval(n.L, rel[1:])
	case *And:
		return i == 0 && isFirstEval(n.L, rel[1:])
	case *Or:
		return i == 0 && isFirstEval(n.L, rel[1:])
	case *Not:
		return isFirstEval(n.E, rel[1:])
	case *Call:
		return i == 0 && isFirstEval(n.Args[0], rel[1:])
	}
	return false
}

// stmtDominates：绑定语句 sp 是否支配 o2（每条到达 o2 的路径都先执行 sp）。
// 返回 (是否支配, 绕过原因, 绕过点位置)。
func stmtDominates(prog []Stmt, sp, o2 []int) (bool, string, []int) {
	if isPrefix(sp, o2) {
		return true, "", nil
	}
	d := 0
	for d < len(sp) && d < len(o2) && sp[d] == o2[d] {
		d++
	}
	node := nodeAt(prog, sp[:d])
	c1, c2 := sp[d], o2[d]
	switch node.(type) {
	case []Stmt:
		if c1 > c2 {
			return false, "O2 先于绑定语句求值", sp[:d]
		}
		// 从 LCA 到绑定语句的链路不能经过 If 分支（条件不满足时跳过）
		for k := d + 1; k < len(sp); k++ {
			if _, ok := nodeAt(prog, sp[:k]).(*If); ok {
				br := "then"
				if sp[k] == 2 {
					br = "else"
				}
				return false, "绑定语句位于 if 的 " + br + " 分支，条件不满足时不执行", sp[:k]
			}
		}
		return true, "", nil
	case *If:
		return false, "绑定语句位于 if 分支内，O2 在分支之外", sp[:d]
	}
	return false, "无法建立支配关系", sp[:d]
}

// collectWritesBefore：语句 node 内、按求值序位于 stop 之前的写
func collectWritesBefore(node interface{}, ft FuncTable, base, stop []int) []writeHit {
	var out []writeHit
	stopped := false
	var walk func(n interface{}, p []int)
	walk = func(n interface{}, p []int) {
		if stopped {
			return
		}
		if eqPath(p, stop) {
			stopped = true
			return
		}
		switch nn := n.(type) {
		case []Stmt:
			for i, s := range nn {
				walk(s, ext(p, i))
			}
		case *Assign:
			walk(nn.E, ext(p, 0))
			if !stopped {
				out = append(out, writeHit{nn.X, p, "赋值"})
			}
		case *Print:
			walk(nn.E, ext(p, 0))
		case *If:
			walk(nn.C, ext(p, 0))
			walk(nn.Then, ext(p, 1))
			walk(nn.Else, ext(p, 2))
		case *Add:
			walk(nn.L, ext(p, 0))
			walk(nn.R, ext(p, 1))
		case *And:
			walk(nn.L, ext(p, 0))
			walk(nn.R, ext(p, 1))
		case *Or:
			walk(nn.L, ext(p, 0))
			walk(nn.R, ext(p, 1))
		case *Not:
			walk(nn.E, ext(p, 0))
		case *Call:
			for i, a := range nn.Args {
				walk(a, ext(p, i))
			}
			if !stopped {
				for _, w := range ft[nn.F].Writes {
					out = append(out, writeHit{w, p, "调用 " + nn.F})
				}
			}
		}
	}
	walk(node, base)
	return out
}

// collectBetween：按求值序位于 O1 之后、O2 之前的所有（可能的）写
func collectBetween(prog []Stmt, ft FuncTable, o1, o2 []int) []writeHit {
	var out []writeHit
	state := 0 // 0=O1前 1=O1..O2之间 2=O2后
	var walk func(n interface{}, p []int)
	walk = func(n interface{}, p []int) {
		if state == 2 {
			return
		}
		if eqPath(p, o1) {
			state = 1
			return
		}
		if eqPath(p, o2) {
			state = 2
			return
		}
		switch nn := n.(type) {
		case []Stmt:
			for i, s := range nn {
				walk(s, ext(p, i))
			}
		case *Assign:
			walk(nn.E, ext(p, 0))
			if state == 1 {
				out = append(out, writeHit{nn.X, p, "赋值"})
			}
		case *Print:
			walk(nn.E, ext(p, 0))
		case *If:
			walk(nn.C, ext(p, 0))
			walk(nn.Then, ext(p, 1))
			walk(nn.Else, ext(p, 2))
		case *Add:
			walk(nn.L, ext(p, 0))
			walk(nn.R, ext(p, 1))
		case *And:
			walk(nn.L, ext(p, 0))
			walk(nn.R, ext(p, 1))
		case *Or:
			walk(nn.L, ext(p, 0))
			walk(nn.R, ext(p, 1))
		case *Not:
			walk(nn.E, ext(p, 0))
		case *Call:
			for i, a := range nn.Args {
				walk(a, ext(p, i))
			}
			if state == 1 {
				for _, w := range ft[nn.F].Writes {
					out = append(out, writeHit{w, p, "调用 " + nn.F})
				}
			}
		}
	}
	walk(prog, nil)
	return out
}
