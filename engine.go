package entitymerge

import "sort"

// pairKey 是记录对的无序键。
type pairKey struct{ a, b string }

func makePair(x, y string) pairKey {
	if x < y {
		return pairKey{x, y}
	}
	return pairKey{y, x}
}

// Engine 增量维护记录到实体的合并结果。
type Engine struct {
	rules []Rule

	records map[string]Record
	order   []string // 记录 ID，按到达顺序

	edges map[pairKey]map[string]bool // 记录对 -> 支撑它的规则集合
	tomb  map[pairKey]map[string]bool // 已撤销的证据（全量重算时保持生效）
	adj   map[string]map[string]bool  // 当前有效边的邻接表

	blocks []map[string][]string // 每条规则的分块索引：分块键 -> 记录 ID

	uf       *unionFind
	entityOf map[string]string          // 记录 ID -> 实体 ID
	members  map[string]map[string]bool // 实体 ID -> 记录 ID 集合
}

// New 以给定规则创建引擎。规则顺序无关结果，但会影响证据命名。
func New(rules ...Rule) *Engine {
	e := &Engine{
		rules:    rules,
		records:  map[string]Record{},
		edges:    map[pairKey]map[string]bool{},
		tomb:     map[pairKey]map[string]bool{},
		adj:      map[string]map[string]bool{},
		blocks:   make([]map[string][]string, len(rules)),
		uf:       newUnionFind(),
		entityOf: map[string]string{},
		members:  map[string]map[string]bool{},
	}
	for i := range e.blocks {
		e.blocks[i] = map[string][]string{}
	}
	return e
}

// AddRecord 加入一条记录并返回其当前实体 ID。重复 ID 直接返回原实体。
func (e *Engine) AddRecord(rec Record) string {
	if ent, dup := e.entityOf[rec.ID]; dup {
		return ent
	}
	rec.seq = len(e.order)
	e.records[rec.ID] = rec
	e.order = append(e.order, rec.ID)
	e.uf.add(rec.ID)
	e.entityOf[rec.ID] = rec.ID
	e.members[rec.ID] = map[string]bool{rec.ID: true}

	for i, rule := range e.rules {
		key, ok := rule.blockKey(rec)
		if !ok {
			continue
		}
		for _, otherID := range e.blocks[i][key] {
			pk := makePair(rec.ID, otherID)
			if e.tomb[pk][rule.Name] {
				continue // 该证据已被撤销，更正持续生效
			}
			if rule.matches(rec, e.records[otherID]) {
				e.addEvidence(pk, rule.Name)
			}
		}
		e.blocks[i][key] = append(e.blocks[i][key], rec.ID)
	}
	return e.entityOf[rec.ID]
}

// Retract 撤销一条匹配证据（某规则曾判定 idA 与 idB 匹配）。
// 只重算受影响的连通分量；返回是否存在该证据。
func (e *Engine) Retract(ruleName, idA, idB string) bool {
	pk := makePair(idA, idB)
	sup := e.edges[pk]
	if !sup[ruleName] {
		return false
	}
	delete(sup, ruleName)
	if e.tomb[pk] == nil {
		e.tomb[pk] = map[string]bool{}
	}
	e.tomb[pk][ruleName] = true
	if len(sup) > 0 {
		return true // 仍有其他规则支撑该边，连通性不变
	}
	delete(e.edges, pk)
	delete(e.adj[pk.a], pk.b)
	delete(e.adj[pk.b], pk.a)
	e.recomputeComponent(e.entityOf[pk.a])
	return true
}

// EntityOf 返回记录所属实体 ID。
func (e *Engine) EntityOf(recordID string) (string, bool) {
	ent, ok := e.entityOf[recordID]
	return ent, ok
}

// Entities 返回 实体 ID -> 升序记录 ID 列表 的快照。
func (e *Engine) Entities() map[string][]string {
	out := make(map[string][]string, len(e.members))
	for ent, set := range e.members {
		ids := make([]string, 0, len(set))
		for id := range set {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		out[ent] = ids
	}
	return out
}

// Evidences 返回当前全部有效匹配证据，按规则与记录对排序。
func (e *Engine) Evidences() []Evidence {
	var out []Evidence
	for pk, rules := range e.edges {
		for name := range rules {
			out = append(out, Evidence{Rule: name, A: pk.a, B: pk.b})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Rule != out[j].Rule {
			return out[i].Rule < out[j].Rule
		}
		if out[i].A != out[j].A {
			return out[i].A < out[j].A
		}
		return out[i].B < out[j].B
	})
	return out
}

// RecomputeAll 丢弃全部派生状态，按原始到达顺序重放所有记录做全量重算。
// 已撤销的证据保持撤销。用于校验增量维护结果与全量重算一致。
func (e *Engine) RecomputeAll() {
	records := e.records
	order := e.order
	tomb := e.tomb

	fresh := New(e.rules...)
	fresh.tomb = tomb
	*e = *fresh
	for _, id := range order {
		e.AddRecord(records[id])
	}
}

// addEvidence 记录一条证据；当它是该记录对的首条证据时建立边并合并分量。
func (e *Engine) addEvidence(pk pairKey, ruleName string) {
	sup := e.edges[pk]
	if sup == nil {
		sup = map[string]bool{}
		e.edges[pk] = sup
	}
	if sup[ruleName] {
		return
	}
	sup[ruleName] = true
	if len(sup) > 1 {
		return // 边已存在
	}
	if e.adj[pk.a] == nil {
		e.adj[pk.a] = map[string]bool{}
	}
	if e.adj[pk.b] == nil {
		e.adj[pk.b] = map[string]bool{}
	}
	e.adj[pk.a][pk.b] = true
	e.adj[pk.b][pk.a] = true
	e.mergeComponents(pk.a, pk.b)
}

// mergeComponents 合并两条记录所在的实体分量，实体 ID 取最早记录。
func (e *Engine) mergeComponents(x, y string) {
	ex, ey := e.entityOf[x], e.entityOf[y]
	if ex == ey {
		return
	}
	e.uf.union(x, y)
	keep, drop := ex, ey
	if e.earlier(ey, ex) {
		keep, drop = ey, ex
	}
	for id := range e.members[drop] {
		e.entityOf[id] = keep
		e.members[keep][id] = true
	}
	delete(e.members, drop)
}

// recomputeComponent 只重算指定实体分量：重置其成员的并查集节点，
// 用分量内剩余有效边重建连通性，再按子分量稳定分配实体 ID。
func (e *Engine) recomputeComponent(entityID string) {
	set := e.members[entityID]
	delete(e.members, entityID)

	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
		e.uf.reset(id)
	}
	for _, id := range ids {
		for nb := range e.adj[id] {
			if set[nb] && id < nb {
				e.uf.union(id, nb)
			}
		}
	}

	groups := map[string][]string{}
	for _, id := range ids {
		root := e.uf.find(id)
		groups[root] = append(groups[root], id)
	}
	for _, g := range groups {
		ent := g[0]
		for _, id := range g[1:] {
			if e.earlier(id, ent) {
				ent = id
			}
		}
		newSet := make(map[string]bool, len(g))
		for _, id := range g {
			newSet[id] = true
			e.entityOf[id] = ent
		}
		e.members[ent] = newSet
	}
}

// earlier 判定记录 a 是否早于 b（按到达顺序，同序按 ID 字典序）。
func (e *Engine) earlier(a, b string) bool {
	ra, rb := e.records[a], e.records[b]
	if ra.seq != rb.seq {
		return ra.seq < rb.seq
	}
	return a < b
}
