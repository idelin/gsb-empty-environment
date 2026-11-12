package cond

import (
	"fmt"
	"sort"
	"strings"
)

// Expr 是编译后的条件表达式，可反复对请求求值。
type Expr interface {
	// eval 在 path 处短路求值，并把证据追加到 ev。
	eval(l Labels, path string, ev *Evidence) Tri
	// String 返回表达式的规范化文本，用于证据与测试。
	String() string
	// labels 收集表达式引用的标签名（去重、排序由调用方保证）。
	labels(map[string]struct{})
}

// Exists 判断标签是否出现（空集合也算出现）。
type Exists struct{ Label string }

// AnyEq 判断标签集合中是否任一元素等于 Want。
type AnyEq struct {
	Label string
	Want  string
}

// AllEq 判断标签集合中是否全体元素等于 Want。
// 空集合为真（空真），缺失为未知。
type AllEq struct {
	Label string
	Want  string
}

// Neg 是显式否定。
type Neg struct{ Arg Expr }

// Conj 是合取，按序短路求值。
type Conj struct{ Args []Expr }

// Disj 是析取，按序短路求值。
type Disj struct{ Args []Expr }

func (e Exists) String() string { return fmt.Sprintf("exists(%s)", e.Label) }
func (e AnyEq) String() string  { return fmt.Sprintf("anyEq(%s,%q)", e.Label, e.Want) }
func (e AllEq) String() string  { return fmt.Sprintf("allEq(%s,%q)", e.Label, e.Want) }
func (e Neg) String() string    { return "not(" + e.Arg.String() + ")" }

func (e Conj) String() string {
	parts := make([]string, len(e.Args))
	for i, a := range e.Args {
		parts[i] = a.String()
	}
	return "and(" + strings.Join(parts, " ") + ")"
}

func (e Disj) String() string {
	parts := make([]string, len(e.Args))
	for i, a := range e.Args {
		parts[i] = a.String()
	}
	return "or(" + strings.Join(parts, " ") + ")"
}

func (e Exists) labels(m map[string]struct{}) { m[e.Label] = struct{}{} }
func (e AnyEq) labels(m map[string]struct{})  { m[e.Label] = struct{}{} }
func (e AllEq) labels(m map[string]struct{})  { m[e.Label] = struct{}{} }
func (e Neg) labels(m map[string]struct{})    { e.Arg.labels(m) }

func (e Conj) labels(m map[string]struct{}) {
	for _, a := range e.Args {
		a.labels(m)
	}
}

func (e Disj) labels(m map[string]struct{}) {
	for _, a := range e.Args {
		a.labels(m)
	}
}

// Labels 返回表达式引用的标签名，升序去重。
func LabelsOf(e Expr) []string {
	m := make(map[string]struct{})
	e.labels(m)
	out := make([]string, 0, len(m))
	for name := range m {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Compile 把声明式条件规格编译为可求值的 Expr。
// 规格是 JSON 风格结构，例如：
//
//	{"op": "exists", "label": "team"}
//	{"op": "anyEq",  "label": "role", "value": "admin"}
//	{"op": "allEq",  "label": "scope", "value": "read"}
//	{"op": "not",    "arg":  <spec>}
//	{"op": "and",    "args": [<spec>, ...]}
//	{"op": "or",     "args": [<spec>, ...]}
func Compile(spec any) (Expr, error) {
	m, ok := spec.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("cond: 条件必须是对象，得到 %T", spec)
	}
	op, _ := m["op"].(string)
	switch op {
	case "exists":
		label, err := strField(m, "label")
		if err != nil {
			return nil, err
		}
		return Exists{Label: label}, nil
	case "anyEq", "allEq":
		label, err := strField(m, "label")
		if err != nil {
			return nil, err
		}
		want, err := strField(m, "value")
		if err != nil {
			return nil, err
		}
		if op == "anyEq" {
			return AnyEq{Label: label, Want: want}, nil
		}
		return AllEq{Label: label, Want: want}, nil
	case "not":
		arg, ok := m["arg"]
		if !ok {
			return nil, fmt.Errorf("cond: not 缺少 arg")
		}
		sub, err := Compile(arg)
		if err != nil {
			return nil, err
		}
		return Neg{Arg: sub}, nil
	case "and", "or":
		raw, ok := m["args"].([]any)
		if !ok || len(raw) == 0 {
			return nil, fmt.Errorf("cond: %s 需要非空 args 数组", op)
		}
		args := make([]Expr, len(raw))
		for i, r := range raw {
			sub, err := Compile(r)
			if err != nil {
				return nil, fmt.Errorf("cond: %s 第 %d 个参数: %w", op, i, err)
			}
			args[i] = sub
		}
		if op == "and" {
			return Conj{Args: args}, nil
		}
		return Disj{Args: args}, nil
	default:
		return nil, fmt.Errorf("cond: 未知操作 %q", op)
	}
}

func strField(m map[string]any, key string) (string, error) {
	s, ok := m[key].(string)
	if !ok || s == "" {
		return "", fmt.Errorf("cond: 字段 %q 需要非空字符串", key)
	}
	return s, nil
}
