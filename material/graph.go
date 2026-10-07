package material

import (
	"fmt"
	"sort"
)

// Graph 是材料库某一时刻的版本图快照：节点为 (证据, 版本)，
// 边为版本记录上冻结的依赖关系。所有核对都是只读的。
type Graph struct {
	records map[Ref]VersionRecord
	current map[string]int
}

// Graph 对材料库做一致快照，用于核对依赖更新、撤回与发布前检查。
func (l *Library) Graph() *Graph {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.snapshotLocked()
}

// snapshotLocked 在已持有锁的前提下构建快照。
func (l *Library) snapshotLocked() *Graph {
	g := &Graph{
		records: make(map[Ref]VersionRecord),
		current: make(map[string]int),
	}
	for id, e := range l.items {
		g.current[id] = e.Current
		for v, rec := range e.Versions {
			g.records[Ref{EvidenceID: id, Version: v}] = cloneRecord(rec)
		}
	}
	return g
}

// UpdateEdge 描述一条“依赖已有更新版本”的核对结果。
type UpdateEdge struct {
	Item   Ref // 持有依赖的版本
	Dep    Ref // 被钉住的依赖版本
	Latest int // 依赖证据的当前最新版本
}

func (u UpdateEdge) String() string {
	return fmt.Sprintf("%s 依赖 %s，最新为 v%d", u.Item, u.Dep, u.Latest)
}

// DependencyUpdates 核对依赖更新：凡是被钉在旧版本、且证据已产生
// 更新版本的依赖边，都会列入结果（按确定性顺序返回）。
func (g *Graph) DependencyUpdates() []UpdateEdge {
	var out []UpdateEdge
	for item, rec := range g.records {
		for _, dep := range rec.Deps {
			if latest, ok := g.current[dep.EvidenceID]; ok && dep.Version < latest {
				out = append(out, UpdateEdge{Item: item, Dep: dep, Latest: latest})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Item != out[j].Item {
			return out[i].Item.String() < out[j].Item.String()
		}
		return out[i].Dep.String() < out[j].Dep.String()
	})
	return out
}

// WithdrawnDeps 核对撤回：返回所有指向已撤回版本的依赖边。
func (g *Graph) WithdrawnDeps() []UpdateEdge {
	var out []UpdateEdge
	for item, rec := range g.records {
		for _, dep := range rec.Deps {
			if depRec, ok := g.records[dep]; ok && depRec.Withdrawn {
				out = append(out, UpdateEdge{Item: item, Dep: dep, Latest: g.current[dep.EvidenceID]})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Item != out[j].Item {
			return out[i].Item.String() < out[j].Item.String()
		}
		return out[i].Dep.String() < out[j].Dep.String()
	})
	return out
}

// VerifyFrozenSet 核对批量发布的冻结集合一致性：
//  1. 集合内每个版本都必须存在、未撤回、且已签收；
//  2. 集合内任何版本声明的依赖都必须精确落在同一集合中，
//     不允许混用集合外的新旧版本签收。
func (g *Graph) VerifyFrozenSet(batch []Ref) error {
	if len(batch) == 0 {
		return fmt.Errorf("material: empty publish batch")
	}
	frozen := make(map[Ref]bool, len(batch))
	for _, ref := range batch {
		if frozen[ref] {
			return fmt.Errorf("material: duplicate batch entry %s", ref)
		}
		frozen[ref] = true
	}
	for _, ref := range batch {
		rec, ok := g.records[ref]
		if !ok {
			return fmt.Errorf("material: %v: %s", ErrVersionNotFound, ref)
		}
		if rec.Withdrawn {
			return fmt.Errorf("material: %v: %s", ErrVersionWithdrawn, ref)
		}
		if !rec.Signed() {
			return fmt.Errorf("material: %s 无任何签收", ref)
		}
		for _, dep := range rec.Deps {
			if !frozen[dep] {
				return fmt.Errorf("material: %s 的依赖 %s 不在冻结集合内（禁止混用新旧签收）", ref, dep)
			}
		}
	}
	return nil
}
