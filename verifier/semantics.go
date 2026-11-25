// Package verifier 独立核验 migration 规划器产出的证书。
//
// 核验器只依赖 spec 包的纯数据类型，自行重新实现芯片语义、
// 信念枚举、安全检查与最小最坏烧录次数求解；它绝不 import
// migration 包，因此核验结论不会被规划器实现中的错误“背书”。
package verifier

import (
	"fmt"
	"sort"

	"prescreen.local/ddd-10554-v3/spec"
)

// engine 是核验器自有的语义实现（与 migration 包刻意重复、独立编写）。
type engine struct {
	chip      *spec.Chip
	positions map[spec.PositionID]spec.Position
	byOwner   map[spec.OwnerID][]spec.Position
}

func newEngine(chip *spec.Chip) (*engine, error) {
	if err := validateChip(chip); err != nil {
		return nil, err
	}
	e := &engine{
		chip:      chip,
		positions: map[spec.PositionID]spec.Position{},
		byOwner:   map[spec.OwnerID][]spec.Position{},
	}
	for _, p := range chip.Positions {
		e.positions[p.ID] = p
		e.byOwner[p.Owner] = append(e.byOwner[p.Owner], p)
	}
	return e, nil
}

func validateChip(chip *spec.Chip) error {
	if chip == nil || chip.Initial == nil {
		return fmt.Errorf("芯片或初始状态为空")
	}
	if chip.MaxBurns < 1 {
		return fmt.Errorf("烧录预算非法")
	}
	owners := map[spec.OwnerID]bool{}
	for _, o := range chip.Owners {
		if owners[o] {
			return fmt.Errorf("所有者重复 %q", o)
		}
		owners[o] = true
	}
	seen := map[spec.PositionID]bool{}
	legacy := map[spec.OwnerID]map[spec.Scope]bool{}
	shadow := map[spec.OwnerID]map[spec.Scope]bool{}
	add := func(m map[spec.OwnerID]map[spec.Scope]bool, o spec.OwnerID, sc spec.Scope) error {
		if m[o] == nil {
			m[o] = map[spec.Scope]bool{}
		}
		if m[o][sc] {
			return fmt.Errorf("所有者 %q 同侧范围 %q 重叠", o, sc)
		}
		m[o][sc] = true
		return nil
	}
	for _, p := range chip.Positions {
		if seen[p.ID] {
			return fmt.Errorf("位置重复 %q", p.ID)
		}
		seen[p.ID] = true
		if !owners[p.Owner] {
			return fmt.Errorf("位置 %q 所有者未声明", p.ID)
		}
		for _, sc := range p.Scopes {
			var err error
			if p.Shadow {
				err = add(shadow, p.Owner, sc)
			} else {
				err = add(legacy, p.Owner, sc)
			}
			if err != nil {
				return err
			}
		}
	}
	for _, o := range chip.Owners {
		if len(legacy[o]) == 0 || len(shadow[o]) == 0 {
			return fmt.Errorf("所有者 %q 缺少旧位置或影子位置", o)
		}
		if len(legacy[o]) != len(shadow[o]) {
			return fmt.Errorf("所有者 %q 分裂两侧范围不一致", o)
		}
		for sc := range legacy[o] {
			if !shadow[o][sc] {
				return fmt.Errorf("所有者 %q 分裂两侧范围不一致", o)
			}
		}
	}
	if len(chip.Recovery) == 0 {
		return fmt.Errorf("缺少恢复组合")
	}
	for ci, combo := range chip.Recovery {
		if len(combo.Images) != len(chip.Owners) {
			return fmt.Errorf("恢复组合 %d 覆盖不全", ci)
		}
		for o, id := range combo.Images {
			if !owners[o] {
				return fmt.Errorf("恢复组合引用未知所有者")
			}
			if _, ok := chip.Images[id]; !ok {
				return fmt.Errorf("恢复组合引用未知映像 %q", id)
			}
		}
	}
	for o, id := range chip.Revoked {
		if !owners[o] {
			return fmt.Errorf("撤销映像引用未知所有者")
		}
		if _, ok := chip.Images[id]; !ok {
			return fmt.Errorf("撤销映像 %q 不存在", id)
		}
	}
	for _, p := range chip.Positions {
		cnt, ok := chip.Initial.Counters[p.ID]
		if !ok {
			return fmt.Errorf("初始状态缺位置 %q", p.ID)
		}
		for _, sc := range p.Scopes {
			if _, ok := cnt[sc]; !ok {
				return fmt.Errorf("初始状态缺范围代次")
			}
		}
	}
	for _, o := range chip.Owners {
		if _, ok := chip.Initial.Pointers[o]; !ok {
			return fmt.Errorf("初始状态缺所有者 %q 指针", o)
		}
	}
	for i, alt := range chip.InitialAlternatives {
		if alt == nil {
			return fmt.Errorf("备选初始状态 %d 为空", i)
		}
		for _, p := range chip.Positions {
			cnt, ok := alt.Counters[p.ID]
			if !ok {
				return fmt.Errorf("备选初始状态 %d 缺位置", i)
			}
			for _, sc := range p.Scopes {
				if _, ok := cnt[sc]; !ok {
					return fmt.Errorf("备选初始状态 %d 缺范围代次", i)
				}
			}
		}
		for _, o := range chip.Owners {
			if _, ok := alt.Pointers[o]; !ok {
				return fmt.Errorf("备选初始状态 %d 缺指针", i)
			}
		}
	}
	return nil
}

func (e *engine) selectable(owner spec.OwnerID, phase spec.PointerPhase) map[spec.Scope][]spec.PositionID {
	out := map[spec.Scope][]spec.PositionID{}
	for _, p := range e.byOwner[owner] {
		ok := false
		switch {
		case !p.Shadow && (phase == spec.PhaseStable || phase == spec.PhasePending):
			ok = true
		case p.Shadow && (phase == spec.PhasePending || phase == spec.PhaseCommitted):
			ok = true
		}
		if !ok {
			continue
		}
		for _, sc := range p.Scopes {
			out[sc] = append(out[sc], p.ID)
		}
	}
	return out
}

type vpath map[spec.OwnerID]map[spec.Scope]spec.PositionID

func (e *engine) paths(state *spec.State) []vpath {
	var result []vpath
	cur := vpath{}
	var owner func(i int)
	owner = func(i int) {
		if i == len(e.chip.Owners) {
			dup := vpath{}
			for o, m := range cur {
				dup[o] = map[spec.Scope]spec.PositionID{}
				for sc, pid := range m {
					dup[o][sc] = pid
				}
			}
			result = append(result, dup)
			return
		}
		o := e.chip.Owners[i]
		sel := e.selectable(o, state.Pointers[o].Phase)
		scopes := make([]spec.Scope, 0, len(sel))
		for sc := range sel {
			scopes = append(scopes, sc)
		}
		sort.Slice(scopes, func(a, b int) bool { return scopes[a] < scopes[b] })
		var scope func(j int)
		scope = func(j int) {
			if j == len(scopes) {
				owner(i + 1)
				return
			}
			sc := scopes[j]
			for _, pid := range sel[sc] {
				if cur[o] == nil {
					cur[o] = map[spec.Scope]spec.PositionID{}
				}
				cur[o][sc] = pid
				scope(j + 1)
			}
		}
		scope(0)
	}
	owner(0)
	return result
}

func (e *engine) accepts(state *spec.State, path vpath, img spec.Image) bool {
	for _, owner := range e.chip.Owners {
		scm, ok := path[owner]
		if !ok {
			return false
		}
		for _, p := range e.byOwner[owner] {
			for _, sc := range p.Scopes {
				pid, ok := scm[sc]
				if !ok {
					return false
				}
				holder := e.positions[pid]
				if !vHasScope(holder, sc) {
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

func vHasScope(p spec.Position, sc spec.Scope) bool {
	for _, x := range p.Scopes {
		if x == sc {
			return true
		}
	}
	return false
}

func (e *engine) safe(state *spec.State) bool {
	for o, imgID := range e.chip.Revoked {
		img := e.chip.Images[imgID]
		sel := e.selectable(o, state.Pointers[o].Phase)
		// 逐范围独立保证：每个可选位置在其持有的范围上都拒绝撤销版本。
		for sc, pids := range sel {
			for _, pid := range pids {
				p := e.positions[pid]
				if !vHasScope(p, sc) {
					continue
				}
				if state.Counters[pid][sc] <= img.Versions[sc] {
					return false
				}
			}
		}
	}
	comboOK := false
	for _, combo := range e.chip.Recovery {
		good := true
		for _, o := range e.chip.Owners {
			img := e.chip.Images[combo.Images[o]]
			sel := e.selectable(o, state.Pointers[o].Phase)
			for sc, pids := range sel {
				any := false
				for _, pid := range pids {
					if state.Counters[pid][sc] <= img.Versions[sc] {
						any = true
						break
					}
				}
				if !any {
					good = false
					break
				}
			}
			if !good {
				break
			}
		}
		if good {
			comboOK = true
			break
		}
	}
	return comboOK
}

func (e *engine) terminal(state *spec.State) bool {
	for _, o := range e.chip.Owners {
		if state.Pointers[o].Phase != spec.PhaseCommitted {
			return false
		}
	}
	return true
}

func (e *engine) readback(state *spec.State) map[spec.PositionID]map[spec.Scope]spec.Generation {
	out := map[spec.PositionID]map[spec.Scope]spec.Generation{}
	for _, p := range e.chip.Positions {
		if !p.Public {
			continue
		}
		m := map[spec.Scope]spec.Generation{}
		for sc, g := range state.Counters[p.ID] {
			m[sc] = g
		}
		out[p.ID] = m
	}
	return out
}
