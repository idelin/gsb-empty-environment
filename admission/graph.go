package admission

import (
	"bytes"
	"slices"
)

// pageState 是单个映像位置在某条历史中的完整状态。
// 摘要按位置记录：别名页可共享同一摘要，但权限永远属于位置本身。
type pageState struct {
	digest   string // 空串表示该位置没有可核验内容
	trust    Trust
	perm     uint8
	attested bool
}

func unattested(perm uint8) pageState {
	return pageState{trust: TrustUnknown, perm: perm}
}

// graph 把收据按新根索引，便于在更新 DAG 上沿旧根回溯。
type graph struct {
	in       AdmissionInput
	byNew    map[string]Receipt
	children map[string][]string // oldRoot -> 子节点 newRoot 列表
	states   map[string]*intervalMap[pageState]
	heads    [][]byte
}

// Admission 是构建完成的恢复准入视图，可并发只读使用。
type Admission struct {
	g    *graph
	need uint8
}

// Build 校验输入并构建可重复使用的准入视图。验证成本只付一次，
// 之后可对多个请求区间调用 Admit。
func Build(in AdmissionInput) (*Admission, error) {
	if in.Verifier == nil {
		return nil, ErrMissingVerifier
	}
	if !in.Request.Valid() {
		return nil, ErrBadRequest
	}
	cp := in.Checkpoint
	if err := cp.Valid(); err != nil {
		return nil, err
	}
	g := &graph{
		in:       in,
		byNew:    make(map[string]Receipt),
		children: make(map[string][]string),
		states:   make(map[string]*intervalMap[pageState]),
	}
	rootKey := string(cp.Root)

	for _, rcpt := range in.Receipts {
		if err := validateReceipt(rcpt, cp.Shards); err != nil {
			return nil, err
		}
		key := string(rcpt.NewRoot)
		if prev, ok := g.byNew[key]; ok {
			if !sameReceipt(prev, rcpt) {
				return nil, ErrRootConflict
			}
			continue
		}
		g.byNew[key] = rcpt
		g.children[string(rcpt.OldRoot)] =
			append(g.children[string(rcpt.OldRoot)], key)
	}

	// 从检查点根做可达性遍历并做环检测；与检查点不相容的收据一律拒绝，
	// 避免宿主在不知情时把无关分支纳入判定。
	color := map[string]uint8{rootKey: 1}
	var walk func(key string) error
	walk = func(key string) error {
		for _, child := range g.children[key] {
			switch color[child] {
			case 1:
				return ErrGraphCycle
			case 2:
				continue
			}
			color[child] = 1
			if err := walk(child); err != nil {
				return err
			}
		}
		color[key] = 2
		return nil
	}
	if err := walk(rootKey); err != nil {
		return nil, err
	}
	for key := range g.byNew {
		if color[key] == 0 {
			return nil, ErrUnreachable
		}
	}

	heads, err := g.resolveHeads(color)
	if err != nil {
		return nil, err
	}
	g.heads = heads

	base, err := g.baseState()
	if err != nil {
		return nil, err
	}
	g.states[rootKey] = base

	need := in.Need
	if need == 0 {
		need = PermExec
	}
	return &Admission{g: g, need: need}, nil
}

// Heads 返回本次判定实际使用的候选当前根。
func (a *Admission) Heads() [][]byte {
	out := make([][]byte, len(a.g.heads))
	for i, h := range a.g.heads {
		out[i] = bytes.Clone(h)
	}
	return out
}

func (g *graph) resolveHeads(reachable map[string]uint8) ([][]byte, error) {
	if len(g.in.Heads) > 0 {
		heads := make([][]byte, 0, len(g.in.Heads))
		seen := map[string]bool{}
		for _, h := range g.in.Heads {
			key := string(h)
			if reachable[key] == 0 {
				return nil, ErrUnreachable
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			heads = append(heads, bytes.Clone(h))
		}
		return heads, nil
	}
	// 默认候选根是 DAG 的所有叶节点；丢失提交确认时宿主可显式传入内部根，
	// 把“更新可能尚未持久化”的中间状态也纳入相容历史。
	var heads [][]byte
	for key := range reachable {
		if len(g.children[key]) == 0 {
			heads = append(heads, []byte(key))
		}
	}
	slices.SortFunc(heads, bytes.Compare)
	return heads, nil
}

func sameReceipt(a, b Receipt) bool {
	return a.Publisher == b.Publisher &&
		bytes.Equal(a.OldRoot, b.OldRoot) &&
		bytes.Equal(a.NewRoot, b.NewRoot) &&
		a.Pages == b.Pages && a.Kind == b.Kind
}
