// Package assembler 按 trace/span ID 把乱序到达的遥测 span 组装成树。
//
// 支持：
//   - 子先于父到达（孤儿暂存，父到达后归位，根晚到同理）；
//   - 重复 span：同父为幂等更新，不同父报 parent_conflict 且保留首次归属；
//   - 缺父超时：超过 TTL 未等到父的孤儿进入 unresolved，绝不挂到任意根下；
//   - 结构循环（A↔B 互相为父）与跨 trace 错误引用分别报告不同问题类型。
package assembler

import (
	"fmt"
	"time"
)

// Event 是一条本地合成（或由夹具回放）的 span 到达事件。
type Event struct {
	TraceID  string    `json:"trace_id"`
	SpanID   string    `json:"span_id"`
	ParentID string    `json:"parent_id"` // 空串表示根 span
	Name     string    `json:"name"`
	At       time.Time `json:"at"`
}

// IssueKind 区分结构循环与跨 trace 错误引用等问题类型。
type IssueKind string

const (
	IssueParentConflict IssueKind = "parent_conflict"        // 重复 span 声明了不同的父
	IssueCycle          IssueKind = "structural_cycle"       // 同 trace 内的结构循环
	IssueCrossTrace     IssueKind = "cross_trace_reference"  // 父 ID 指向其他 trace 的 span
	IssueMissingParent  IssueKind = "missing_parent_timeout" // 缺父超时
)

// Issue 是组装过程中发现的一条结构问题。
type Issue struct {
	Kind    IssueKind
	TraceID string
	SpanID  string
	Detail  string
}

func (i Issue) String() string {
	return fmt.Sprintf("[%s] trace=%s span=%s %s", i.Kind, i.TraceID, i.SpanID, i.Detail)
}

// Span 是组装树中的节点。
type Span struct {
	TraceID  string
	SpanID   string
	ParentID string
	Name     string

	parent   *Span
	Children []*Span

	waitingSince time.Time // 成为孤儿的时刻
	cyclic       bool      // 因结构循环被拒绝挂接
}

// Parent 返回已解析的父节点，未解析时为 nil。
func (s *Span) Parent() *Span { return s.parent }

type traceState struct {
	id              string
	spans           map[string]*Span   // 本 trace 已收到的全部 span
	roots           []*Span            // 显式声明 ParentID 为空的根
	orphans         map[string]*Span   // 等待父到达的 span
	waitingByParent map[string][]*Span // 缺失的父 ID -> 等待它的孤儿们
	unresolved      []*Span            // 超时或循环、最终未能归位的 span
}

// Assembler 按 trace/span ID 组装父子关系。时钟由事件时间驱动，
// 也可用 AdvanceTo 手动推进（用于回放结束后的超时结算）。
type Assembler struct {
	ttl       time.Duration
	now       time.Time
	traces    map[string]*traceState
	spanOwner map[string]string // spanID -> 首次出现的 traceID，用于跨 trace 引用检测
	issues    []Issue
}

// New 创建组装器，ttl 为孤儿等待父到达的超时时长。
func New(ttl time.Duration) *Assembler {
	return &Assembler{
		ttl:       ttl,
		traces:    make(map[string]*traceState),
		spanOwner: make(map[string]string),
	}
}

// Ingest 处理一条 span 事件。
func (a *Assembler) Ingest(ev Event) {
	if ev.TraceID == "" || ev.SpanID == "" {
		return
	}
	if ev.At.After(a.now) {
		a.now = ev.At
	}
	a.expire()

	ts := a.trace(ev.TraceID)

	if existing, ok := ts.spans[ev.SpanID]; ok {
		if existing.ParentID == ev.ParentID {
			// 幂等更新：同父重复到达，只刷新可变字段。
			if ev.Name != "" {
				existing.Name = ev.Name
			}
		} else {
			a.issues = append(a.issues, Issue{
				Kind:    IssueParentConflict,
				TraceID: ev.TraceID,
				SpanID:  ev.SpanID,
				Detail: fmt.Sprintf("duplicate span declares parent %q, keeping original parent %q",
					ev.ParentID, existing.ParentID),
			})
		}
		return
	}

	s := &Span{
		TraceID:  ev.TraceID,
		SpanID:   ev.SpanID,
		ParentID: ev.ParentID,
		Name:     ev.Name,
	}
	ts.spans[s.SpanID] = s
	if _, seen := a.spanOwner[s.SpanID]; !seen {
		a.spanOwner[s.SpanID] = s.TraceID
	}

	if s.ParentID == "" {
		ts.roots = append(ts.roots, s)
	} else {
		a.link(ts, s)
	}
	a.adopt(ts, s)
}

// link 尝试把 s 挂到它的父节点下；父未知则登记为孤儿。
func (a *Assembler) link(ts *traceState, s *Span) {
	parent, ok := ts.spans[s.ParentID]
	if ok {
		// 结构循环检测：从父向上走，若回到 s 自身则成环。
		visited := map[*Span]bool{}
		for p := parent; p != nil && !visited[p]; p = p.parent {
			visited[p] = true
			if p == s {
				s.cyclic = true
				ts.unresolved = append(ts.unresolved, s)
				a.issues = append(a.issues, Issue{
					Kind:    IssueCycle,
					TraceID: s.TraceID,
					SpanID:  s.SpanID,
					Detail:  fmt.Sprintf("attaching %q under %q would create a cycle", s.SpanID, s.ParentID),
				})
				return
			}
		}
		s.parent = parent
		parent.Children = append(parent.Children, s)
		return
	}

	// 父不在本 trace：若该 ID 属于其他 trace，报跨 trace 错误引用。
	if owner, known := a.spanOwner[s.ParentID]; known && owner != ts.id {
		a.issues = append(a.issues, Issue{
			Kind:    IssueCrossTrace,
			TraceID: s.TraceID,
			SpanID:  s.SpanID,
			Detail:  fmt.Sprintf("parent %q belongs to trace %q", s.ParentID, owner),
		})
	}
	s.waitingSince = a.now
	ts.orphans[s.SpanID] = s
	ts.waitingByParent[s.ParentID] = append(ts.waitingByParent[s.ParentID], s)
}

// adopt 把等待 s 的孤儿们挂接到 s 下（父/根晚到后的归位）。
func (a *Assembler) adopt(ts *traceState, s *Span) {
	waiting := ts.waitingByParent[s.SpanID]
	if len(waiting) == 0 {
		return
	}
	delete(ts.waitingByParent, s.SpanID)
	for _, child := range waiting {
		delete(ts.orphans, child.SpanID)
		a.link(ts, child)
		a.adopt(ts, child)
	}
}

// AdvanceTo 推进时钟并结算缺父超时（回放结束后调用以收尾）。
func (a *Assembler) AdvanceTo(t time.Time) {
	if t.After(a.now) {
		a.now = t
	}
	a.expire()
}

// expire 把等待超过 TTL 的孤儿移入 unresolved 并报缺父超时，
// 不会把它们挂到任何根下。
func (a *Assembler) expire() {
	for _, ts := range a.traces {
		for id, s := range ts.orphans {
			if a.now.Sub(s.waitingSince) < a.ttl {
				continue
			}
			delete(ts.orphans, id)
			waiting := ts.waitingByParent[s.ParentID]
			for i, w := range waiting {
				if w == s {
					ts.waitingByParent[s.ParentID] = append(waiting[:i], waiting[i+1:]...)
					break
				}
			}
			if len(ts.waitingByParent[s.ParentID]) == 0 {
				delete(ts.waitingByParent, s.ParentID)
			}
			ts.unresolved = append(ts.unresolved, s)
			a.issues = append(a.issues, Issue{
				Kind:    IssueMissingParent,
				TraceID: s.TraceID,
				SpanID:  s.SpanID,
				Detail:  fmt.Sprintf("parent %q never arrived within %s", s.ParentID, a.ttl),
			})
		}
	}
}

func (a *Assembler) trace(id string) *traceState {
	ts, ok := a.traces[id]
	if !ok {
		ts = &traceState{
			id:              id,
			spans:           make(map[string]*Span),
			orphans:         make(map[string]*Span),
			waitingByParent: make(map[string][]*Span),
		}
		a.traces[id] = ts
	}
	return ts
}

// Issues 返回至今发现的所有结构问题。
func (a *Assembler) Issues() []Issue { return a.issues }

// Roots 返回指定 trace 的根 span 列表。
func (a *Assembler) Roots(traceID string) []*Span {
	if ts, ok := a.traces[traceID]; ok {
		return ts.roots
	}
	return nil
}

// Orphans 返回指定 trace 仍在等待父到达的 span。
func (a *Assembler) Orphans(traceID string) []*Span {
	var out []*Span
	if ts, ok := a.traces[traceID]; ok {
		for _, s := range ts.orphans {
			out = append(out, s)
		}
	}
	return out
}

// Unresolved 返回指定 trace 因超时或循环最终未能归位的 span。
func (a *Assembler) Unresolved(traceID string) []*Span {
	if ts, ok := a.traces[traceID]; ok {
		return ts.unresolved
	}
	return nil
}

// Lookup 返回指定 trace 中已收到的 span。
func (a *Assembler) Lookup(traceID, spanID string) *Span {
	if ts, ok := a.traces[traceID]; ok {
		return ts.spans[spanID]
	}
	return nil
}

// Cyclic 报告该 span 是否因结构循环被拒绝挂接。
func (s *Span) Cyclic() bool { return s.cyclic }
