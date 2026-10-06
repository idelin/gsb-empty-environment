package material

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ErrNotFrozen 表示待发布证据不在冻结集合中。
var ErrNotFrozen = errors.New("material: evidence not in frozen set")

// FrozenSet 是一次冻结操作捕获的「证据 ID → 版本号」快照。
// 批量发布时，所有依赖证据必须命中同一冻结集合，不允许混用新旧签收。
type FrozenSet map[string]int

// Refs 返回冻结集合对应的版本引用列表（排序后）。
func (f FrozenSet) Refs() []Ref {
	out := make([]Ref, 0, len(f))
	for id, v := range f {
		out = append(out, Ref{ID: id, Version: v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ID != out[j].ID {
			return out[i].ID < out[j].ID
		}
		return out[i].Version < out[j].Version
	})
	return out
}

// Freeze 捕获指定证据当前版本，形成冻结集合。
func (l *Library) Freeze(ids ...string) (FrozenSet, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	frozen := make(FrozenSet, len(ids))
	for _, id := range ids {
		e, ok := l.evidences[id]
		if !ok {
			return nil, fmt.Errorf("material: evidence not found: %s", id)
		}
		frozen[id] = e.current
	}
	return frozen, nil
}

// PublishResult 描述一次成功发布的版本集合。
type PublishResult struct {
	Published []Ref
}

// Publish 按冻结集合批量发布证据，语义为原子操作：
// 任一证据校验失败（版本已推进、签收为空、依赖不在同一冻结集合等），
// 整批失败且库内状态不发生任何变化。
func (l *Library) Publish(frozen FrozenSet, ids ...string) (*PublishResult, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	refs := make([]Ref, 0, len(ids))
	var problems []string
	for _, id := range ids {
		fv, ok := frozen[id]
		if !ok {
			problems = append(problems, fmt.Sprintf("%s 不在冻结集合中", id))
			continue
		}
		e, ok := l.evidences[id]
		if !ok {
			problems = append(problems, fmt.Sprintf("%s 证据不存在", id))
			continue
		}
		if e.current != fv {
			problems = append(problems,
				fmt.Sprintf("%s 冻结版本为 %d，但当前已推进到 %d（内容已更新）", id, fv, e.current))
			continue
		}
		v := e.versions[fv]
		if len(v.Signers) == 0 {
			problems = append(problems, fmt.Sprintf("%s@%d 签收人集合为空", id, fv))
			continue
		}
		refs = append(refs, Ref{ID: id, Version: fv})
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return nil, fmt.Errorf("material: publish aborted, nothing published:\n%s", strings.Join(problems, "\n"))
	}

	// 版本图核对：依赖必须全部落在同一冻结集合内。
	if err := l.graph.CheckFrozen(frozen, refs); err != nil {
		return nil, err
	}

	// 全部校验通过后才落状态，保证原子性。
	for _, r := range refs {
		l.evidences[r.ID].versions[r.Version].Published = true
	}
	l.graph.RecordPublish(refs)
	return &PublishResult{Published: refs}, nil
}

// IsPublished 查询某证据的某版本是否已发布。
func (l *Library) IsPublished(id string, version int) bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	e, ok := l.evidences[id]
	if !ok {
		return false
	}
	v, ok := e.versions[version]
	return ok && v.Published
}
