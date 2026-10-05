package main

import (
	"errors"
	"fmt"
	"sort"
)

// Tau 是内部动作（不可见动作）的标签。
const Tau = "tau"

// MaxStates 是每个转换系统允许的最大状态数。
const MaxStates = 30

// Transition 表示一条带标签的转移。
type Transition struct {
	From  string
	Label string
	To    string
}

func (t Transition) String() string {
	return fmt.Sprintf("%s -%s-> %s", t.From, t.Label, t.To)
}

// LTS 是一个有限标签转换系统，同时含可见动作与内部动作 tau。
type LTS struct {
	Name     string
	States   []string
	Initial  string
	outgoing map[string][]Transition
}

// NewLTS 构造一个 LTS，并校验状态数上限、状态引用合法性。
func NewLTS(name string, states []string, initial string, trs []Transition) (*LTS, error) {
	if len(states) == 0 {
		return nil, errors.New("状态集不能为空")
	}
	if len(states) > MaxStates {
		return nil, fmt.Errorf("系统 %s 状态数 %d 超过上限 %d", name, len(states), MaxStates)
	}
	set := make(map[string]bool, len(states))
	for _, s := range states {
		if set[s] {
			return nil, fmt.Errorf("系统 %s 状态 %q 重复", name, s)
		}
		set[s] = true
	}
	if !set[initial] {
		return nil, fmt.Errorf("系统 %s 初始状态 %q 不在状态集中", name, initial)
	}
	l := &LTS{
		Name:     name,
		States:   append([]string(nil), states...),
		Initial:  initial,
		outgoing: make(map[string][]Transition),
	}
	for _, tr := range trs {
		if !set[tr.From] || !set[tr.To] {
			return nil, fmt.Errorf("系统 %s 的转移 %s 引用了未声明的状态", name, tr)
		}
		l.outgoing[tr.From] = append(l.outgoing[tr.From], tr)
	}
	return l, nil
}

// Transitions 返回 s 的全部出边。
func (l *LTS) Transitions(s string) []Transition { return l.outgoing[s] }

// TauStar 返回从 s 经零步或多步内部动作可达的所有状态（含 s 自身）。
// 通过已访问集合处理内部循环：tau 环只会被遍历一次，
// 不会被当作可见进展，也不会造成死循环。
func (l *LTS) TauStar(s string) []string {
	seen := map[string]bool{s: true}
	queue := []string{s}
	var out []string
	for len(queue) > 0 {
		x := queue[0]
		queue = queue[1:]
		out = append(out, x)
		for _, tr := range l.outgoing[x] {
			if tr.Label == Tau && !seen[tr.To] {
				seen[tr.To] = true
				queue = append(queue, tr.To)
			}
		}
	}
	sort.Strings(out)
	return out
}

// WeakMove 返回弱转移 s =a=> · 的目标集合。
// 对可见动作 a，弱转移为 tau* · a · tau*；对 tau，弱转移为 tau*。
func (l *LTS) WeakMove(s, a string) []string {
	if a == Tau {
		return l.TauStar(s)
	}
	seen := make(map[string]bool)
	var out []string
	for _, x := range l.TauStar(s) {
		for _, tr := range l.outgoing[x] {
			if tr.Label != a {
				continue
			}
			for _, y := range l.TauStar(tr.To) {
				if !seen[y] {
					seen[y] = true
					out = append(out, y)
				}
			}
		}
	}
	sort.Strings(out)
	return out
}
