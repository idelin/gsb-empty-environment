package material

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Ref 唯一标识版本图中的一个节点：某证据的某个版本。
type Ref struct {
	ID      string
	Version int
}

func (r Ref) String() string { return fmt.Sprintf("%s@%d", r.ID, r.Version) }

// EventKind 区分版本图上记录的事件类型。
type EventKind int

const (
	EventAddVersion EventKind = iota
	EventSign
	EventWithdraw
	EventPublish
)

func (k EventKind) String() string {
	switch k {
	case EventAddVersion:
		return "add-version"
	case EventSign:
		return "sign"
	case EventWithdraw:
		return "withdraw"
	case EventPublish:
		return "publish"
	}
	return "unknown"
}

// Event 是版本图上的一条审计记录。
type Event struct {
	Kind   EventKind
	Ref    Ref
	Signer string // 仅签收/撤回事件使用
}

// Graph 是版本图：节点为 (证据, 版本)，边为版本间的依赖关系，
// 同时保留签收/撤回事件流，用于核对依赖更新、撤回与发布结果。
type Graph struct {
	mu     sync.RWMutex
	nodes  map[Ref]bool
	edges  map[Ref]map[Ref]bool // from 依赖 to
	events []Event
}

// NewGraph 创建空版本图。
func NewGraph() *Graph {
	return &Graph{
		nodes: make(map[Ref]bool),
		edges: make(map[Ref]map[Ref]bool),
	}
}

// AddVersion 登记一个版本节点（由 Library 在创建/更新时调用）。
func (g *Graph) AddVersion(id string, version int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	ref := Ref{ID: id, Version: version}
	g.nodes[ref] = true
	g.events = append(g.events, Event{Kind: EventAddVersion, Ref: ref})
}

// RecordSign 记录一次签收事件。
func (g *Graph) RecordSign(id string, version int, signer string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.events = append(g.events, Event{Kind: EventSign, Ref: Ref{ID: id, Version: version}, Signer: signer})
}

// RecordWithdraw 记录一次撤回事件。
func (g *Graph) RecordWithdraw(id string, version int, signer string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.events = append(g.events, Event{Kind: EventWithdraw, Ref: Ref{ID: id, Version: version}, Signer: signer})
}

// RecordPublish 记录一次发布事件。
func (g *Graph) RecordPublish(refs []Ref) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, r := range refs {
		g.events = append(g.events, Event{Kind: EventPublish, Ref: r})
	}
}

// AddDependency 声明 from 版本依赖 to 版本。
func (g *Graph) AddDependency(from, to Ref) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.nodes[from] {
		return fmt.Errorf("material: dependency source %s not in version graph", from)
	}
	if !g.nodes[to] {
		return fmt.Errorf("material: dependency target %s not in version graph", to)
	}
	if g.edges[from] == nil {
		g.edges[from] = make(map[Ref]bool)
	}
	g.edges[from][to] = true
	return nil
}

// Dependencies 返回 from 版本直接依赖的版本集合（排序后）。
func (g *Graph) Dependencies(from Ref) []Ref {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]Ref, 0, len(g.edges[from]))
	for to := range g.edges[from] {
		out = append(out, to)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ID != out[j].ID {
			return out[i].ID < out[j].ID
		}
		return out[i].Version < out[j].Version
	})
	return out
}

// Events 返回事件流副本。
func (g *Graph) Events() []Event {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]Event, len(g.events))
	copy(out, g.events)
	return out
}

// SignerOf 依据事件流重放某版本当前的签收人集合，用于与材料库状态交叉核对。
func (g *Graph) SignerOf(ref Ref) []string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	signers := make(map[string]bool)
	for _, ev := range g.events {
		if ev.Ref != ref {
			continue
		}
		switch ev.Kind {
		case EventSign:
			signers[ev.Signer] = true
		case EventWithdraw:
			delete(signers, ev.Signer)
		}
	}
	out := make([]string, 0, len(signers))
	for s := range signers {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// CheckFrozen 核对 refs 中的每个版本与其依赖是否都属于同一冻结集合：
// 任何依赖指向冻结集合之外的版本（例如依赖已更新产生的新版本，
// 或仍停留在旧版本）都会报错，从而禁止混用新旧签收。
func (g *Graph) CheckFrozen(frozen FrozenSet, refs []Ref) error {
	var problems []string
	for _, ref := range refs {
		fv, ok := frozen[ref.ID]
		if !ok {
			problems = append(problems, fmt.Sprintf("%s 不在冻结集合中", ref.ID))
			continue
		}
		if fv != ref.Version {
			problems = append(problems,
				fmt.Sprintf("%s 冻结版本为 %d，待发布版本为 %d", ref.ID, fv, ref.Version))
		}
		for _, dep := range g.Dependencies(ref) {
			dv, ok := frozen[dep.ID]
			if !ok {
				problems = append(problems,
					fmt.Sprintf("%s 的依赖 %s 不在冻结集合中", ref, dep))
				continue
			}
			if dv != dep.Version {
				problems = append(problems,
					fmt.Sprintf("%s 依赖 %s，但冻结集合中 %s 的版本为 %d（新旧混用）",
						ref, dep, dep.ID, dv))
			}
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("material: frozen set check failed:\n%s", strings.Join(problems, "\n"))
	}
	return nil
}
