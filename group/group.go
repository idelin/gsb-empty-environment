// Package group 维护实体分组：显式“必须同组”(same) 与“不得同组”(forbid)
// 两类硬关系。same 关系按传递闭包形成连通分量即分组；新增关系若与既有
// 关系在传递闭包下冲突，则拒绝并给出路径证据。相似度仅为软建议，不自动
// 形成硬关系。支持以事务为单位撤销最近操作，回滚后用连通模型校验不变量。
package group

import (
	"errors"
	"fmt"
	"sort"
)

// Edge 表示一对实体之间的关系。
type Edge struct {
	A string
	B string
}

// ConflictError 描述一次因传递闭包撞上禁止关系（或反向）而被拒绝的操作，
// 并携带路径证据。
type ConflictError struct {
	// Op 为被拒绝的操作："same" 或 "forbid"。
	Op string
	// Edge 为试图加入的关系。
	Edge Edge
	// Forbid 为被撞上的禁止关系（Op=="same" 时有效）。
	Forbid Edge
	// PathA 为 Edge.A 到 Forbid.A 的 same 路径（含端点）。
	PathA []string
	// PathB 为 Edge.B 到 Forbid.B 的 same 路径（含端点）。
	PathB []string
	// SamePath 为 Edge.A 到 Edge.B 的既有 same 路径（Op=="forbid" 时有效）。
	SamePath []string
}

func (e *ConflictError) Error() string {
	if e.Op == "same" {
		return fmt.Sprintf("拒绝同组关系 %s~%s：与禁止关系 %s!~%s 冲突；证据路径 %s 与 %s",
			e.Edge.A, e.Edge.B, e.Forbid.A, e.Forbid.B,
			joinPath(e.PathA), joinPath(e.PathB))
	}
	return fmt.Sprintf("拒绝禁止关系 %s!~%s：二者已同组；证据路径 %s",
		e.Edge.A, e.Edge.B, joinPath(e.SamePath))
}

func joinPath(p []string) string {
	out := ""
	for i, n := range p {
		if i > 0 {
			out += " ~ "
		}
		out += n
	}
	return out
}

// Suggestion 是一条相似度软建议，不参与任何硬关系推导。
type Suggestion struct {
	A      string
	B      string
	Score  float64
	Reason string
}

// Store 保存实体关系图与事务日志。
type Store struct {
	same        map[string]map[string]bool
	forbid      map[string]map[string]bool
	suggestions []Suggestion
	txlog       []tx
}

// tx 记录一次已应用变更的逆操作。
type tx struct {
	desc string
	undo func()
}

// New 返回空 Store。
func New() *Store {
	return &Store{
		same:   make(map[string]map[string]bool),
		forbid: make(map[string]map[string]bool),
	}
}

// ErrNothingToUndo 表示没有可撤销的事务。
var ErrNothingToUndo = errors.New("没有可撤销的事务")

func addEdge(g map[string]map[string]bool, a, b string) {
	if g[a] == nil {
		g[a] = make(map[string]bool)
	}
	if g[b] == nil {
		g[b] = make(map[string]bool)
	}
	g[a][b] = true
	g[b][a] = true
}

func removeEdge(g map[string]map[string]bool, a, b string) {
	delete(g[a], b)
	delete(g[b], a)
	if len(g[a]) == 0 {
		delete(g, a)
	}
	if len(g[b]) == 0 {
		delete(g, b)
	}
}

func hasEdge(g map[string]map[string]bool, a, b string) bool {
	return g[a] != nil && g[a][b]
}

// Path 返回 a 到 b 经 same 边的一条路径（含端点）；不连通时返回 nil。
func (s *Store) Path(a, b string) []string {
	prev := map[string]string{a: ""}
	queue := []string{a}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur == b {
			var path []string
			for n := b; ; n = prev[n] {
				path = append([]string{n}, path...)
				if n == a {
					return path
				}
			}
		}
		for next := range s.same[cur] {
			if _, seen := prev[next]; !seen {
				prev[next] = cur
				queue = append(queue, next)
			}
		}
	}
	return nil
}

// Connected 报告 a、b 是否在同一组（same 传递闭包）。
func (s *Store) Connected(a, b string) bool {
	return s.Path(a, b) != nil
}

// component 返回 start 所在连通分量的成员集合。
func (s *Store) component(start string) map[string]bool {
	seen := map[string]bool{start: true}
	queue := []string{start}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for next := range s.same[cur] {
			if !seen[next] {
				seen[next] = true
				queue = append(queue, next)
			}
		}
	}
	return seen
}

// LinkSame 加入“必须同组”关系。若合并后的分量撞上某条禁止关系，
// 拒绝本次操作并返回携带路径证据的 *ConflictError。
func (s *Store) LinkSame(a, b string) error {
	if a == b || hasEdge(s.same, a, b) {
		return nil
	}
	if s.Connected(a, b) {
		return nil // 已传递同组，幂等
	}
	ca, cb := s.component(a), s.component(b)
	// 既有图一致，冲突的 forbid 边必一端在 ca、另一端在 cb。
	for x, ys := range s.forbid {
		for y := range ys {
			if x > y {
				continue
			}
			if (ca[x] && cb[y]) || (ca[y] && cb[x]) {
				return &ConflictError{
					Op:     "same",
					Edge:   Edge{a, b},
					Forbid: Edge{x, y},
					PathA:  s.Path(a, x),
					PathB:  s.Path(b, y),
				}
			}
		}
	}
	addEdge(s.same, a, b)
	s.txlog = append(s.txlog, tx{
		desc: fmt.Sprintf("same %s ~ %s", a, b),
		undo: func() { removeEdge(s.same, a, b) },
	})
	return nil
}

// LinkForbid 加入“不得同组”关系。若两端已处于同一连通分量，
// 拒绝并返回携带 same 路径证据的 *ConflictError。
func (s *Store) LinkForbid(a, b string) error {
	if hasEdge(s.forbid, a, b) {
		return nil
	}
	if p := s.Path(a, b); p != nil {
		return &ConflictError{
			Op:       "forbid",
			Edge:     Edge{a, b},
			SamePath: p,
		}
	}
	addEdge(s.forbid, a, b)
	s.txlog = append(s.txlog, tx{
		desc: fmt.Sprintf("forbid %s !~ %s", a, b),
		undo: func() { removeEdge(s.forbid, a, b) },
	})
	return nil
}

// UnlinkSame 移除一条显式 same 边（传递边不可直接移除）。
func (s *Store) UnlinkSame(a, b string) error {
	if !hasEdge(s.same, a, b) {
		return fmt.Errorf("same 边 %s ~ %s 不存在", a, b)
	}
	removeEdge(s.same, a, b)
	s.txlog = append(s.txlog, tx{
		desc: fmt.Sprintf("unsame %s ~ %s", a, b),
		undo: func() { addEdge(s.same, a, b) },
	})
	return nil
}

// UnlinkForbid 移除一条 forbid 边。
func (s *Store) UnlinkForbid(a, b string) error {
	if !hasEdge(s.forbid, a, b) {
		return fmt.Errorf("forbid 边 %s !~ %s 不存在", a, b)
	}
	removeEdge(s.forbid, a, b)
	s.txlog = append(s.txlog, tx{
		desc: fmt.Sprintf("unforbid %s !~ %s", a, b),
		undo: func() { addEdge(s.forbid, a, b) },
	})
	return nil
}

// Undo 撤销最近一次变更并回滚；回滚后用连通模型校验不变量。
func (s *Store) Undo() error {
	n := len(s.txlog)
	if n == 0 {
		return ErrNothingToUndo
	}
	last := s.txlog[n-1]
	s.txlog = s.txlog[:n-1]
	last.undo()
	return s.Verify()
}

// Suggest 记录一条相似度软建议；不会形成任何硬关系。
func (s *Store) Suggest(a, b string, score float64, reason string) {
	s.suggestions = append(s.suggestions, Suggestion{A: a, B: b, Score: score, Reason: reason})
}

// Suggestions 返回全部软建议（按记录顺序）。
func (s *Store) Suggestions() []Suggestion {
	out := make([]Suggestion, len(s.suggestions))
	copy(out, s.suggestions)
	return out
}

// Groups 返回当前分组（same 连通分量），组内与组间均按字典序排序。
func (s *Store) Groups() [][]string {
	seen := make(map[string]bool)
	var groups [][]string
	for node := range s.same {
		if seen[node] {
			continue
		}
		comp := s.component(node)
		var members []string
		for m := range comp {
			seen[m] = true
			members = append(members, m)
		}
		sort.Strings(members)
		groups = append(groups, members)
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i][0] < groups[j][0] })
	return groups
}

// Verify 用图连通模型核对不变量：任何 forbid 边的两端不得落在同一
// same 连通分量。返回第一个违例，无违例返回 nil。
func (s *Store) Verify() error {
	for x, ys := range s.forbid {
		for y := range ys {
			if x > y {
				continue
			}
			if p := s.Path(x, y); p != nil {
				return &ConflictError{
					Op:       "forbid",
					Edge:     Edge{x, y},
					SamePath: p,
				}
			}
		}
	}
	return nil
}
