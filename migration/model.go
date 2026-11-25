// Package migration 实现回滚位置迁移的问题语义与最坏烧录次数最优的
// 部分可观测信念规划器，并产出可独立核验的证书。
//
// 语义要点：
//   - 每个计数位置有稳定所有者与固定签名范围；影子位置用于暂存新计数。
//   - “烧录计数”与“切换指针”是两类不同的持久动作，且切换分
//     request（进入 pending）与 confirm（进入 committed）两步，
//     两步之间允许掉电，掉电窗口内旧路径与影子路径都可能被固件选中。
//   - 安全不变量：任意可达具体状态下，所有可能被选中的读取路径都拒绝
//     原已撤销映像；同时至少一个指定恢复组合仍可完整启动。
//   - 公开读回只含公开位置的代次，不含指针与非公开位置；策略只能按
//     观测分支，禁止依据不可见内部状态选择不同动作。
package migration

import (
	"prescreen.local/ddd-10554-v3/spec"
)

// Path 是一条固件可能选中的读取路径：
// 对每个所有者、每个签名范围指定实际读取的计数位置。
type Path map[spec.OwnerID]map[spec.Scope]spec.PositionID

// Model 缓存芯片规格的派生索引，供语义查询与规划复用。
type Model struct {
	chip      *spec.Chip
	positions map[spec.PositionID]spec.Position
	byOwner   map[spec.OwnerID][]spec.Position
}

// New 构造并校验芯片模型。
func New(chip *spec.Chip) (*Model, error) {
	if err := Validate(chip); err != nil {
		return nil, err
	}
	m := &Model{
		chip:      chip,
		positions: map[spec.PositionID]spec.Position{},
		byOwner:   map[spec.OwnerID][]spec.Position{},
	}
	for _, p := range chip.Positions {
		m.positions[p.ID] = p
		m.byOwner[p.Owner] = append(m.byOwner[p.Owner], p)
	}
	return m, nil
}

// Chip 返回底层规格。
func (m *Model) Chip() *spec.Chip { return m.chip }

// SelectablePositions 返回某所有者在给定指针阶段下，每个签名范围
// 可能被固件读取的位置集合。
func (m *Model) SelectablePositions(owner spec.OwnerID, phase spec.PointerPhase) map[spec.Scope][]spec.PositionID {
	out := map[spec.Scope][]spec.PositionID{}
	for _, p := range m.byOwner[owner] {
		include := false
		switch {
		case !p.Shadow && phase == spec.PhaseStable:
			include = true
		case !p.Shadow && phase == spec.PhasePending:
			include = true
		case p.Shadow && phase == spec.PhasePending:
			include = true
		case p.Shadow && phase == spec.PhaseCommitted:
			include = true
		}
		if !include {
			continue
		}
		for _, sc := range p.Scopes {
			out[sc] = append(out[sc], p.ID)
		}
	}
	return out
}

// Paths 枚举某具体状态下所有可能被固件选中的读取路径。
//
// 共享位置分裂后，一个所有者在某指针阶段的可选位置是若干“位置组”，
// 每组覆盖互不相交的范围（例如旧单位置 vs 两个影子兄弟位置）。
// 固件在每个范围上独立选择可读位置，因此路径是范围到位置的映射；
// 接受判定（imageAcceptedOnPath）要求所有者的全部签名范围都被覆盖。
func (m *Model) Paths(state *spec.State) []Path {
	var result []Path
	current := Path{}

	var buildOwner func(ownerIdx int)
	buildOwner = func(ownerIdx int) {
		if ownerIdx == len(m.chip.Owners) {
			dup := Path{}
			for o, scm := range current {
				dup[o] = map[spec.Scope]spec.PositionID{}
				for sc, pid := range scm {
					dup[o][sc] = pid
				}
			}
			result = append(result, dup)
			return
		}
		owner := m.chip.Owners[ownerIdx]
		selectable := m.SelectablePositions(owner, state.Pointers[owner].Phase)
		var scopes []spec.Scope
		for sc := range selectable {
			scopes = append(scopes, sc)
		}
		var buildScope func(scopeIdx int)
		buildScope = func(scopeIdx int) {
			if scopeIdx == len(scopes) {
				buildOwner(ownerIdx + 1)
				return
			}
			sc := scopes[scopeIdx]
			for _, pid := range selectable[sc] {
				if current[owner] == nil {
					current[owner] = map[spec.Scope]spec.PositionID{}
				}
				current[owner][sc] = pid
				buildScope(scopeIdx + 1)
			}
		}
		buildScope(0)
	}
	buildOwner(0)
	return result
}

// imageAcceptedOnPath 判断映像在单条路径上是否被接受。
// 映像未声明的范围按版本 0 处理。
func (m *Model) imageAcceptedOnPath(state *spec.State, path Path, img spec.Image) bool {
	for _, owner := range m.chip.Owners {
		scm, ok := path[owner]
		if !ok {
			return false
		}
		// 必须覆盖该所有者签名范围并集中的每一个范围
		// （共享位置分裂后由多个兄弟位置分别承载）。
		for _, p := range m.byOwner[owner] {
			for _, sc := range p.Scopes {
				pid, ok := scm[sc]
				if !ok {
					return false
				}
				holder := m.positions[pid]
				if !hasScope(holder, sc) {
					return false
				}
				cnt, ok := state.Counters[pid][sc]
				if !ok || cnt > img.Versions[sc] {
					return false
				}
			}
		}
	}
	return true
}

func (m *Model) image(id string) spec.Image { return m.chip.Images[id] }

// rejectsRevoked 判断状态下所有可能路径是否都拒绝每个所有者的撤销映像。
func (m *Model) rejectsRevoked(state *spec.State) bool {
	for owner, imgID := range m.chip.Revoked {
		img := m.image(imgID)
		selectable := m.SelectablePositions(owner, state.Pointers[owner].Phase)
		// 逐范围独立保证：每个签名范围的每个可选位置，当前代次都必须
		// 高于撤销映像在该范围上的版本。这样共享位置分裂后，必须把每个
		// 影子范围都暂存到足够代次，pending 窗口与切换后才不会在任一范围
		// 上回放行已撤销版本。
		for sc, pids := range selectable {
			for _, pid := range pids {
				holder := m.positions[pid]
				if !hasScope(holder, sc) {
					continue
				}
				if state.Counters[pid][sc] <= img.Versions[sc] {
					return false
				}
			}
		}
	}
	return true
}

// comboBootable 判断恢复组合在某状态下是否可完整启动。
// 路径按所有者独立选择，逐所有者要求每个范围至少存在一个可选位置
// 使该范围的当前代次不高于恢复映像版本。
func (m *Model) comboBootable(state *spec.State, combo spec.RecoveryCombo) bool {
	for _, owner := range m.chip.Owners {
		img := m.image(combo.Images[owner])
		selectable := m.SelectablePositions(owner, state.Pointers[owner].Phase)
		for sc, pids := range selectable {
			any := false
			for _, pid := range pids {
				if state.Counters[pid][sc] <= img.Versions[sc] {
					any = true
					break
				}
			}
			if !any {
				return false
			}
		}
	}
	return true
}

// recoveryAvailable 判断至少一个指定恢复组合仍可启动。
func (m *Model) recoveryAvailable(state *spec.State) bool {
	for _, combo := range m.chip.Recovery {
		if m.comboBootable(state, combo) {
			return true
		}
	}
	return false
}

// Safe 实现安全不变量：所有可能路径拒绝撤销映像，且至少保全一个恢复组合。
func (m *Model) Safe(state *spec.State) bool {
	if state == nil {
		return false
	}
	return m.rejectsRevoked(state) && m.recoveryAvailable(state)
}

// Safe 包级便捷函数，用于初始状态校验。
func Safe(chip *spec.Chip, state *spec.State) bool {
	m, err := New(chip)
	if err != nil {
		return false
	}
	return m.Safe(state)
}

// Terminal 判断迁移是否完成：全部所有者指针已确认切换到影子位置。
func (m *Model) Terminal(state *spec.State) bool {
	for _, owner := range m.chip.Owners {
		if state.Pointers[owner].Phase != spec.PhaseCommitted {
			return false
		}
	}
	return true
}

// Readback 产生公开读回：仅包含公开位置的代次，指针与非公开位置不出现。
func (m *Model) Readback(state *spec.State) map[spec.PositionID]map[spec.Scope]spec.Generation {
	out := map[spec.PositionID]map[spec.Scope]spec.Generation{}
	for _, p := range m.chip.Positions {
		if !p.Public {
			continue
		}
		dup := map[spec.Scope]spec.Generation{}
		for sc, g := range state.Counters[p.ID] {
			dup[sc] = g
		}
		out[p.ID] = dup
	}
	return out
}

// BootObservation 构造掉电/重启后的观测。
func (m *Model) BootObservation(state *spec.State) *spec.Observation {
	return &spec.Observation{Kind: spec.ObsBoot, Readback: m.Readback(state)}
}

func cloneState(s *spec.State) *spec.State {
	c := &spec.State{
		Counters: map[spec.PositionID]map[spec.Scope]spec.Generation{},
		Pointers: map[spec.OwnerID]spec.Pointer{},
	}
	for pid, scm := range s.Counters {
		c.Counters[pid] = map[spec.Scope]spec.Generation{}
		for sc, g := range scm {
			c.Counters[pid][sc] = g
		}
	}
	for o, p := range s.Pointers {
		c.Pointers[o] = p
	}
	return c
}
