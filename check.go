package main

import (
	"fmt"
	"sort"
	"strings"
)

// ---------- 重构变换 ----------

// Extract 执行"重复表达式抽取"：在首个出现所在顶层语句之前插入
// let tmp = e，并把两处出现替换为 tmp。调用前需保证 e 恰好出现两次。
func Extract(prog []Stmt, e Expr, tmp string) []Stmt {
	k := firstOccurStmt(prog, e)
	out := make([]Stmt, 0, len(prog)+1)
	out = append(out, prog[:k]...)
	out = append(out, Let{Name: tmp, RHS: e})
	for _, s := range prog[k:] {
		out = append(out, replaceStmt(s, e, Var{tmp}))
	}
	return out
}

// ---------- 检查结果的依赖路径 ----------

// Violation 描述一条被拒绝的理由及其依赖路径。
type Violation struct {
	Cond   string // "求值次数" | "首错" | "读取版本"
	Detail string // 具体说明
	Path   string // 依赖路径：绑定点 -> 中间事件 -> 求值点
}

func (v Violation) String() string {
	return fmt.Sprintf("[%s] %s\n    依赖路径: %s", v.Cond, v.Detail, v.Path)
}

// ---------- 抽象解释：沿所有路径记录事件 ----------

// event 是一次变量读/写（带语句位置），用于读取版本与依赖路径分析。
type event struct {
	kind string // "read" | "write"
	v    string
	loc  string
}

// evalRec 记录目标表达式 e 的一次求值。
type evalRec struct {
	loc      string         // 求值点位置
	readVers map[string]int // 求值点处 e 各读取变量的版本
	evtIdx   int            // 求值点在事件流中的下标
	threw    string         // 若本次求值内部抛错，记录函数名
}

// astate 是一条抽象路径的状态。版本号 = 变量被写次数，与具体值无关，
// 因此对每条路径是确定的。
type astate struct {
	ver   map[string]int
	evs   []event
	trail []string // 已执行语句位置（依赖路径素材）
	err   string   // 首个抛错的函数名
	recs  []evalRec
	// conds 记录本路径上已求值条件的真假：键 = 条件表达式 + 其读取变量的版本。
	// 版本相同则值相同，因此同一条件不会重复分叉（路径敏感的分支关联）。
	conds map[string]bool
	// 绑定点 L（let 插入处，即首个出现所在顶层语句之前）
	lHit bool
	lEvt int
	lLoc string
}

func (s *astate) clone() *astate {
	ver := make(map[string]int, len(s.ver))
	for k, v := range s.ver {
		ver[k] = v
	}
	evs := append([]event(nil), s.evs...)
	trail := append([]string(nil), s.trail...)
	recs := append([]evalRec(nil), s.recs...)
	conds := make(map[string]bool, len(s.conds))
	for k, v := range s.conds {
		conds[k] = v
	}
	return &astate{ver: ver, evs: evs, trail: trail, err: s.err,
		recs: recs, lHit: s.lHit, lEvt: s.lEvt, lLoc: s.lLoc, conds: conds}
}

type acheck struct {
	ft     FuncTable
	target Expr
	reads  map[string]bool
	writes map[string]bool
}

// condKey 标识一次条件求值：表达式结构 + 其读取变量的当前版本。
func (c *acheck) condKey(e Expr, st *astate) string {
	rs := readsOf(e, c.ft)
	vars := make([]string, 0, len(rs))
	for v := range rs {
		vars = append(vars, v)
	}
	sort.Strings(vars)
	s := ExprString(e)
	for _, v := range vars {
		s += fmt.Sprintf("|%s=%d", v, st.ver[v])
	}
	return s
}

// branch 按条件真假分叉：若本路径已记录同一条件的结果则直接复用，
// 否则复制状态分别走 truthy/falsy 两支并记录结果。
func (c *acheck) branch(st *astate, cond Expr, loc string) (truthy, falsy []*astate) {
	key := c.condKey(cond, st)
	if tv, ok := st.conds[key]; ok {
		if tv {
			return []*astate{st}, nil
		}
		return nil, []*astate{st}
	}
	st.conds[key] = false
	cl := st.clone()
	cl.conds[key] = true
	return []*astate{cl}, []*astate{st}
}

// eval 抽象求值：条件值未知，And/Or/If 处分叉。可能原地修改 st，
// 额外返回的状态均为克隆。
func (c *acheck) eval(st *astate, e Expr, loc string) []*astate {
	if st.err != "" {
		return []*astate{st}
	}
	recIdx := -1
	if eqExpr(e, c.target) {
		rv := make(map[string]int, len(c.reads))
		for r := range c.reads {
			rv[r] = st.ver[r]
		}
		st.recs = append(st.recs, evalRec{loc: loc, readVers: rv, evtIdx: len(st.evs)})
		recIdx = len(st.recs) - 1
	}
	var out []*astate
	switch x := e.(type) {
	case Num:
		out = []*astate{st}
	case Var:
		st.evs = append(st.evs, event{kind: "read", v: x.Name, loc: loc})
		out = []*astate{st}
	case Add:
		for _, s := range c.eval(st, x.L, loc) {
			out = append(out, c.eval(s, x.R, loc)...)
		}
	case And:
		for _, s := range c.eval(st, x.L, loc) {
			if s.err != "" {
				out = append(out, s)
				continue
			}
			truthy, falsy := c.branch(s, x.L, loc)
			out = append(out, falsy...) // L 为假：短路跳过 R
			for _, s2 := range truthy {
				out = append(out, c.eval(s2, x.R, loc)...)
			}
		}
	case Or:
		for _, s := range c.eval(st, x.L, loc) {
			if s.err != "" {
				out = append(out, s)
				continue
			}
			truthy, falsy := c.branch(s, x.L, loc)
			out = append(out, truthy...) // L 为真：短路跳过 R
			for _, s2 := range falsy {
				out = append(out, c.eval(s2, x.R, loc)...)
			}
		}
	case Call:
		sts := []*astate{st}
		for _, a := range x.Args {
			var nx []*astate
			for _, s := range sts {
				nx = append(nx, c.eval(s, a, loc)...)
			}
			sts = nx
		}
		spec := c.ft[x.Fn]
		for _, s := range sts {
			if s.err != "" {
				continue
			}
			for _, r := range spec.Reads {
				s.evs = append(s.evs, event{kind: "read", v: r, loc: loc})
			}
			if spec.Throws {
				s.err = x.Fn
				continue
			}
			for _, w := range spec.Writes {
				s.evs = append(s.evs, event{kind: "write", v: w, loc: loc})
				s.ver[w]++
			}
		}
		out = sts
	}
	if recIdx >= 0 {
		for _, s := range out {
			if s.err != "" && s.recs[recIdx].threw == "" {
				s.recs[recIdx].threw = s.err
			}
		}
	}
	return out
}

func (c *acheck) execStmt(st *astate, s Stmt, loc string) []*astate {
	if st.err != "" {
		return []*astate{st}
	}
	st.trail = append(st.trail, loc)
	switch stt := s.(type) {
	case Assign:
		sts := c.eval(st, stt.RHS, loc)
		for _, s2 := range sts {
			if s2.err != "" {
				continue
			}
			s2.evs = append(s2.evs, event{kind: "write", v: stt.Name, loc: loc})
			s2.ver[stt.Name]++
		}
		return sts
	case Let:
		sts := c.eval(st, stt.RHS, loc)
		for _, s2 := range sts {
			if s2.err != "" {
				continue
			}
			s2.evs = append(s2.evs, event{kind: "write", v: stt.Name, loc: loc})
			s2.ver[stt.Name]++
		}
		return sts
	case ExprStmt:
		return c.eval(st, stt.E, loc)
	case If:
		var out []*astate
		for _, s2 := range c.eval(st, stt.Cond, loc) {
			if s2.err != "" {
				out = append(out, s2)
				continue
			}
			truthy, falsy := c.branch(s2, stt.Cond, loc)
			for _, s3 := range truthy {
				out = append(out, c.execBlock(s3, stt.Then, loc+".then")...)
			}
			for _, s3 := range falsy {
				out = append(out, c.execBlock(s3, stt.Else, loc+".else")...)
			}
		}
		return out
	}
	return []*astate{st}
}

func (c *acheck) execBlock(st *astate, blk []Stmt, prefix string) []*astate {
	states := []*astate{st}
	for i, s := range blk {
		loc := fmt.Sprintf("%s[%d]", prefix, i)
		var nx []*astate
		for _, cur := range states {
			if cur.err != "" {
				nx = append(nx, cur)
				continue
			}
			nx = append(nx, c.execStmt(cur, s, loc)...)
		}
		states = nx
	}
	return states
}

// run 抽象执行原程序，返回所有终止路径状态；k 为绑定点所在顶层语句下标。
func (c *acheck) run(prog []Stmt, k int) []*astate {
	states := []*astate{{ver: map[string]int{}, conds: map[string]bool{}}}
	for i, s := range prog {
		loc := fmt.Sprintf("S%d", i)
		var nx []*astate
		for _, st := range states {
			if st.err != "" {
				nx = append(nx, st)
				continue
			}
			if i == k && !st.lHit {
				st.lHit = true
				st.lEvt = len(st.evs)
				st.lLoc = loc
			}
			nx = append(nx, c.execStmt(st, s, loc)...)
		}
		states = nx
	}
	return states
}

// ---------- 三条件检查 ----------

func (c *acheck) checkPath(st *astate) []Violation {
	var out []Violation
	trail := strings.Join(st.trail, " -> ")

	// 条件一：求值次数。抽取后 e 在绑定点求值，次数 = 是否到达绑定点。
	cnt := len(st.recs)
	want := 0
	if st.lHit {
		want = 1
	}
	if cnt != want {
		v := Violation{Cond: "求值次数", Path: trail}
		switch {
		case st.lHit && cnt == 0:
			v.Detail = "该路径上 e 的两次出现均被短路/分支跳过，抽取后会在绑定点多求值一次"
		case cnt > want:
			v.Detail = fmt.Sprintf("该路径上 e 被求值 %d 次，抽取后只求值 %d 次", cnt, want)
		default:
			v.Detail = fmt.Sprintf("该路径上 e 被求值 %d 次，抽取后求值 %d 次", cnt, want)
		}
		out = append(out, v)
	}

	// 条件二：读取版本。绑定点 L 到每个求值点之间的窗口内：
	//  - 不得写入 e 读取的变量（e 读到不同版本）；
	//  - 不得读/写 e 写入的变量（中间代码看到不同版本）。
	if st.lHit {
		for _, rec := range st.recs {
			win := st.evs[st.lEvt:rec.evtIdx]
			for _, ev := range win {
				if ev.kind == "write" && c.reads[ev.v] {
					out = append(out, Violation{
						Cond:   "读取版本",
						Detail: fmt.Sprintf("e 读取 %s，绑定点到求值点之间该变量被写入", ev.v),
						Path:   fmt.Sprintf("let@%s -> write(%s)@%s -> e@%s", st.lLoc, ev.v, ev.loc, rec.loc),
					})
					break
				}
				if c.writes[ev.v] {
					kind := "读取"
					if ev.kind == "write" {
						kind = "写入"
					}
					out = append(out, Violation{
						Cond:   "读取版本",
						Detail: fmt.Sprintf("e 写入 %s，绑定点到求值点之间存在对该变量的%s", ev.v, kind),
						Path:   fmt.Sprintf("let@%s -> %s(%s)@%s -> e@%s", st.lLoc, ev.kind, ev.v, ev.loc, rec.loc),
					})
					break
				}
			}
		}
	}

	// 条件三：首错。e 求值抛错时，抽取会把异常提前到绑定点，
	// 跳过 L 到求值点之间的写操作，要求该窗口无写。
	if st.lHit && cnt == 1 {
		rec := st.recs[0]
		if rec.threw != "" {
			win := st.evs[st.lEvt:rec.evtIdx]
			for _, ev := range win {
				if ev.kind == "write" {
					out = append(out, Violation{
						Cond:   "首错",
						Detail: fmt.Sprintf("e 求值抛出 %s，抽取后异常提前到绑定点，跳过其间的写 %s", rec.threw, ev.v),
						Path:   fmt.Sprintf("let@%s -> write(%s)@%s -> e@%s(throw %s)", st.lLoc, ev.v, ev.loc, rec.loc, rec.threw),
					})
					break
				}
			}
		}
	}
	return out
}

// ---------- 入口 ----------

// CheckExtract 判断把 prog 中恰好出现两次的 e 抽取为 let 绑定是否保持
// 求值次数、首错与读取版本。返回判定、拒绝理由（含依赖路径）与抽取结果。
func CheckExtract(prog []Stmt, ft FuncTable, e Expr) (bool, []Violation, []Stmt, error) {
	total := countOccurProg(prog, e)
	if total != 2 {
		return false, nil, nil, fmt.Errorf("表达式 %s 在程序中出现 %d 次，需要恰好 2 次", ExprString(e), total)
	}
	k := firstOccurStmt(prog, e)
	tmp := freshName(prog, e, ft)
	ref := Extract(prog, e, tmp)

	c := &acheck{ft: ft, target: e, reads: readsOf(e, ft), writes: writesOf(e, ft)}
	var viols []Violation
	seen := map[string]bool{}
	for _, st := range c.run(prog, k) {
		for _, v := range c.checkPath(st) {
			key := v.Cond + "|" + v.Detail + "|" + v.Path
			if !seen[key] {
				seen[key] = true
				viols = append(viols, v)
			}
		}
	}
	return len(viols) == 0, viols, ref, nil
}
