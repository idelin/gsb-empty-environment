package main

import (
	"reflect"
	"strings"
)

// ---------- 可观察语义 ----------
// 轨迹事件：效果调用（有写或会抛错的调用，含实参值）、变量写、print。
// 纯调用（无写不抛错）不产生事件——其求值次数不可观察。
// 程序结果 = 事件轨迹 + 首错（抛错的调用名，无错为空）+ 非临时变量终态。

type Event struct {
	Kind string // "call" | "write" | "print"
	F    string // call：函数名
	Args []int  // call：实参值
	X    string // write：变量
	V    int    // write/print：值
}

type Result struct {
	Events []Event
	Err    string
	Vars   map[string]int
}

type evalErr struct{ f string }

const tmpPrefix = "__"

func isTmp(x string) bool { return strings.HasPrefix(x, tmpPrefix) }

func Run(prog []Stmt, ft FuncTable) (res Result) {
	env := map[string]int{}
	var events []Event

	defer func() {
		if r := recover(); r != nil {
			if ee, ok := r.(evalErr); ok {
				res = Result{Events: events, Err: ee.f, Vars: finalVars(env)}
				return
			}
			panic(r)
		}
	}()

	var eval func(e Expr) int
	var exec func(ss []Stmt)

	eval = func(e Expr) int {
		switch n := e.(type) {
		case *Const:
			return n.V
		case *Var:
			return env[n.Name]
		case *Add:
			return eval(n.L) + eval(n.R)
		case *And:
			if eval(n.L) == 0 {
				return 0
			}
			if eval(n.R) != 0 {
				return 1
			}
			return 0
		case *Or:
			if eval(n.L) != 0 {
				return 1
			}
			if eval(n.R) != 0 {
				return 1
			}
			return 0
		case *Not:
			if eval(n.E) == 0 {
				return 1
			}
			return 0
		case *Call:
			args := make([]int, len(n.Args))
			for i, a := range n.Args {
				args[i] = eval(a)
			}
			sig := ft[n.F]
			effectful := len(sig.Writes) > 0 || sig.Throws
			if effectful {
				events = append(events, Event{Kind: "call", F: n.F, Args: args})
			}
			if sig.Throws {
				panic(evalErr{n.F})
			}
			base := len(n.F)
			for _, a := range args {
				base += a
			}
			for _, r := range sig.Reads {
				base += env[r]
			}
			for _, w := range sig.Writes {
				env[w] = base + 1
				if !isTmp(w) {
					events = append(events, Event{Kind: "write", X: w, V: env[w]})
				}
			}
			return base
		}
		panic("eval: 未知表达式")
	}

	exec = func(ss []Stmt) {
		for _, s := range ss {
			switch n := s.(type) {
			case *Assign:
				v := eval(n.E)
				env[n.X] = v
				if !isTmp(n.X) {
					events = append(events, Event{Kind: "write", X: n.X, V: v})
				}
			case *Print:
				events = append(events, Event{Kind: "print", V: eval(n.E)})
			case *If:
				if eval(n.C) != 0 {
					exec(n.Then)
				} else {
					exec(n.Else)
				}
			}
		}
	}

	exec(prog)
	res = Result{Events: events, Vars: finalVars(env)}
	return
}

func finalVars(env map[string]int) map[string]int {
	out := map[string]int{}
	for k, v := range env {
		if !isTmp(k) {
			out[k] = v
		}
	}
	return out
}

func EqualResults(a, b Result) bool {
	if a.Err != b.Err {
		return false
	}
	if !reflect.DeepEqual(a.Events, b.Events) {
		return false
	}
	return reflect.DeepEqual(a.Vars, b.Vars)
}
