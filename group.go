// Package entitygroup 维护实体之间的显式分组关系：
//
//   - MustTogether（必须同组）：等价关系，按传递闭包合并成分组；
//   - MustNotTogether（不得同组）：约束，禁止两个实体落入同一分组。
//
// 每次新增关系都会基于图连通模型做冲突核对：若“必须同组”的传递闭包
// 撞上“不得同组”关系，本次关系被拒绝，并返回路径证据（ConflictError）。
// 相似度建议（SuggestSimilar）仅作为软提示保存，绝不自动形成硬关系。
// 支持 Undo 撤销最近一次成功提交的事务。
package entitygroup

import "fmt"

// RelationKind 表示硬关系类型。
type RelationKind int

const (
	MustTogether RelationKind = iota
	MustNotTogether
)

func (k RelationKind) String() string {
	if k == MustTogether {
		return "must-together"
	}
	return "must-not-together"
}

// ConflictError 表示一次因传递闭包撞上禁止关系而被拒绝的操作，
// 并携带图路径证据。
type ConflictError struct {
	// Kind 是被拒绝的关系类型，A、B 是该关系的两个端点。
	Kind RelationKind
	A, B string
	// ForbiddenEdge 是被撞上的不得同组关系（仅 MustTogether 冲突时有值）。
	ForbiddenEdge [2]string
	// Path 是“必须同组”图上的路径证据：
	//   - 新增 MustTogether 冲突时：Path 连接 ForbiddenEdge 两端，
	//     加上拟新增的边 A-B 后，禁止边两端将同组；
	//   - 新增 MustNotTogether 冲突时：Path 连接 A 与 B，
	//     说明二者在现有闭包中已同组。
	Path []string
}

func (e *ConflictError) Error() string {
	if e.Kind == MustTogether {
		return fmt.Sprintf("拒绝关系 must-together(%s, %s)：与禁止关系 must-not-together(%s, %s) 冲突，同组路径证据 %v",
			e.A, e.B, e.ForbiddenEdge[0], e.ForbiddenEdge[1], e.Path)
	}
	return fmt.Sprintf("拒绝关系 must-not-together(%s, %s)：两端已在同一分组，同组路径证据 %v",
		e.A, e.B, e.Path)
}

// Suggestion 是一条相似度软建议，不参与任何硬约束。
type Suggestion struct {
	A, B  string
	Score float64
}

// tx 是事务日志中的一条记录，用于回滚。
type tx struct {
	kind RelationKind
	a, b string
}

// Store 保存全部关系与事务日志。零值不可用，请用 New 构造。
type Store struct {
	must   map[string]map[string]bool // 必须同组：无向邻接表
	forbid map[string]map[string]bool // 不得同组：无向邻接表
	suggs  []Suggestion
	log    []tx
}

// New 创建一个空的 Store。
func New() *Store {
	return &Store{
		must:   make(map[string]map[string]bool),
		forbid: make(map[string]map[string]bool),
	}
}

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

// path 返回 must-together 图上从 src 到 dst 的一条 BFS 路径；
// 不连通时返回 nil。src == dst 时返回 [src]。
func (s *Store) path(src, dst string) []string {
	if src == dst {
		return []string{src}
	}
	prev := map[string]string{src: ""}
	queue := []string{src}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for next := range s.must[cur] {
			if _, seen := prev[next]; seen {
				continue
			}
			prev[next] = cur
			if next == dst {
				p := []string{dst}
				for p[0] != src {
					p = append([]string{prev[p[0]]}, p...)
				}
				return p
			}
			queue = append(queue, next)
		}
	}
	return nil
}

// component 返回 src 所在连通分量的全部成员。
func (s *Store) component(src string) map[string]bool {
	comp := map[string]bool{src: true}
	queue := []string{src}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for next := range s.must[cur] {
			if !comp[next] {
				comp[next] = true
				queue = append(queue, next)
			}
		}
	}
	return comp
}

// AddMustTogether 提交一条“必须同组”关系。
// 若合并后某条禁止关系的两端将落入同一分组，则拒绝并返回 *ConflictError，
// Store 保持原状。重复提交已有关系为空操作（不记入事务日志）。
func (s *Store) AddMustTogether(a, b string) error {
	if a == b {
		return nil
	}
	if hasEdge(s.must, a, b) {
		return nil
	}
	// 拟合并的分量 = comp(a) ∪ comp(b)。核对每条禁止边是否两端同落其中。
	compA := s.component(a)
	compB := s.component(b)
	merged := make(map[string]bool, len(compA)+len(compB))
	for m := range compA {
		merged[m] = true
	}
	for m := range compB {
		merged[m] = true
	}
	for x, ys := range s.forbid {
		if !merged[x] {
			continue
		}
		for y := range ys {
			if !merged[y] {
				continue
			}
			// 定向端点，使 x 落在 comp(a) 侧、y 落在 comp(b) 侧。
			if compB[x] && compA[y] {
				x, y = y, x
			}
			// 构造路径证据：x ~ a（已有）+ a-b（拟新增）+ b ~ y（已有）。
			evidence := s.evidenceThrough(x, a, b, y)
			return &ConflictError{
				Kind:          MustTogether,
				A:             a,
				B:             b,
				ForbiddenEdge: [2]string{x, y},
				Path:          evidence,
			}
		}
	}
	addEdge(s.must, a, b)
	s.log = append(s.log, tx{kind: MustTogether, a: a, b: b})
	return nil
}

// evidenceThrough 拼接 x~a、a-b、b~y 三段为一条完整路径。
// 各段可能退化（如 x==a），拼接处去重。
func (s *Store) evidenceThrough(x, a, b, y string) []string {
	seg1 := s.path(x, a)
	seg2 := s.path(b, y)
	out := append([]string{}, seg1...)
	out = append(out, b)
	if len(seg2) > 1 {
		out = append(out, seg2[1:]...)
	}
	return out
}

// AddMustNotTogether 提交一条“不得同组”关系。
// 若两端在现有“必须同组”闭包中已连通，则拒绝并返回 *ConflictError，
// Store 保持原状。重复提交已有关系为空操作（不记入事务日志）。
func (s *Store) AddMustNotTogether(a, b string) error {
	if a == b {
		return &ConflictError{
			Kind: MustNotTogether,
			A:    a,
			B:    b,
			Path: []string{a},
		}
	}
	if hasEdge(s.forbid, a, b) {
		return nil
	}
	if p := s.path(a, b); p != nil {
		return &ConflictError{
			Kind: MustNotTogether,
			A:    a,
			B:    b,
			Path: p,
		}
	}
	addEdge(s.forbid, a, b)
	s.log = append(s.log, tx{kind: MustNotTogether, a: a, b: b})
	return nil
}

// SuggestSimilar 记录一条相似度建议。它只进入建议列表，
// 绝不自动形成任何硬关系，也不影响分组与冲突判定。
func (s *Store) SuggestSimilar(a, b string, score float64) {
	s.suggs = append(s.suggs, Suggestion{A: a, B: b, Score: score})
}

// Suggestions 返回全部相似度建议。
func (s *Store) Suggestions() []Suggestion {
	out := make([]Suggestion, len(s.suggs))
	copy(out, s.suggs)
	return out
}

// Connected 报告 a、b 在当前“必须同组”闭包中是否同组；
// 若同组，同时返回一条路径证据。
func (s *Store) Connected(a, b string) (bool, []string) {
	p := s.path(a, b)
	return p != nil, p
}

// Forbidden 报告 a、b 之间是否存在不得同组关系。
func (s *Store) Forbidden(a, b string) bool {
	return hasEdge(s.forbid, a, b)
}

// Groups 返回当前分组（每个连通分量一组，按成员名字典序排列）。
func (s *Store) Groups() [][]string {
	seen := make(map[string]bool)
	var groups [][]string
	for v := range s.must {
		if seen[v] {
			continue
		}
		comp := s.component(v)
		var members []string
		for m := range comp {
			seen[m] = true
			members = append(members, m)
		}
		sortStrings(members)
		groups = append(groups, members)
	}
	return groups
}

// Undo 撤销最近一次成功提交的关系事务并回滚图状态。
// 返回被撤销的事务描述；无可撤销事务时返回 false。
func (s *Store) Undo() (string, bool) {
	if len(s.log) == 0 {
		return "", false
	}
	last := s.log[len(s.log)-1]
	s.log = s.log[:len(s.log)-1]
	if last.kind == MustTogether {
		removeEdge(s.must, last.a, last.b)
	} else {
		removeEdge(s.forbid, last.a, last.b)
	}
	return fmt.Sprintf("%s(%s, %s)", last.kind, last.a, last.b), true
}

// PendingTx 返回当前事务日志长度（已成功提交且未撤销的关系数）。
func (s *Store) PendingTx() int {
	return len(s.log)
}

func sortStrings(xs []string) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j] < xs[j-1]; j-- {
			xs[j], xs[j-1] = xs[j-1], xs[j]
		}
	}
}
