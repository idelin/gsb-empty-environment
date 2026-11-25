package migration

import (
	"errors"
	"fmt"

	"prescreen.local/ddd-10554-v3/spec"
)

// Validate 校验芯片规格的静态不变量与初始状态的完备性。
func Validate(chip *spec.Chip) error {
	if chip == nil {
		return errors.New("chip 为空")
	}
	if len(chip.Owners) == 0 {
		return errors.New("至少需要一个所有者")
	}
	if chip.MaxBurns < 1 {
		return errors.New("单次会话烧录预算必须 >= 1")
	}
	if chip.Initial == nil {
		return errors.New("缺少初始状态")
	}

	ownerSet := map[spec.OwnerID]bool{}
	for _, o := range chip.Owners {
		if ownerSet[o] {
			return fmt.Errorf("所有者 %q 重复", o)
		}
		ownerSet[o] = true
	}

	posSeen := map[spec.PositionID]bool{}
	legacyScopes := map[spec.OwnerID]map[spec.Scope]bool{}
	shadowScopes := map[spec.OwnerID]map[spec.Scope]bool{}
	for _, p := range chip.Positions {
		if posSeen[p.ID] {
			return fmt.Errorf("位置 %q 重复", p.ID)
		}
		posSeen[p.ID] = true
		if !ownerSet[p.Owner] {
			return fmt.Errorf("位置 %q 的所有者 %q 未声明", p.ID, p.Owner)
		}
		if len(p.Scopes) == 0 {
			return fmt.Errorf("位置 %q 没有签名范围", p.ID)
		}
		scopeSet := map[spec.Scope]bool{}
		for _, sc := range p.Scopes {
			if scopeSet[sc] {
				return fmt.Errorf("位置 %q 的签名范围 %q 重复", p.ID, sc)
			}
			scopeSet[sc] = true
		}
		target := legacyScopes
		if p.Shadow {
			target = shadowScopes
		}
		if target[p.Owner] == nil {
			target[p.Owner] = map[spec.Scope]bool{}
		}
		for sc := range scopeSet {
			if target[p.Owner][sc] {
				return fmt.Errorf("所有者 %q 的同侧位置在范围 %q 上重叠", p.Owner, sc)
			}
			target[p.Owner][sc] = true
		}
	}

	for _, o := range chip.Owners {
		leg, sh := legacyScopes[o], shadowScopes[o]
		if len(leg) == 0 {
			return fmt.Errorf("所有者 %q 缺少旧位置", o)
		}
		if len(sh) == 0 {
			return fmt.Errorf("所有者 %q 缺少影子位置", o)
		}
		if len(leg) != len(sh) || !sameScopeSet(leg, sh) {
			return fmt.Errorf("所有者 %q 的旧位置与影子位置签名范围并集不一致（不支持的分裂）", o)
		}
	}

	for id := range chip.Images {
		if id == "" {
			return errors.New("映像 ID 不能为空")
		}
	}
	for _, img := range chip.Images {
		if _, ok := chip.Images[img.ID]; !ok {
			return fmt.Errorf("映像表键与 ID 不一致: %q", img.ID)
		}
	}
	for o, id := range chip.Revoked {
		if !ownerSet[o] {
			return fmt.Errorf("撤销映像引用了未知所有者 %q", o)
		}
		if _, ok := chip.Images[id]; !ok {
			return fmt.Errorf("撤销映像 %q 不存在", id)
		}
	}
	if len(chip.Recovery) == 0 {
		return errors.New("至少需要一个指定恢复组合")
	}
	for ci, combo := range chip.Recovery {
		if len(combo.Images) != len(chip.Owners) {
			return fmt.Errorf("恢复组合 %d 未覆盖全部所有者", ci)
		}
		for o, id := range combo.Images {
			if !ownerSet[o] {
				return fmt.Errorf("恢复组合 %d 引用未知所有者 %q", ci, o)
			}
			if _, ok := chip.Images[id]; !ok {
				return fmt.Errorf("恢复组合 %d 引用未知映像 %q", ci, id)
			}
		}
	}

	for _, p := range chip.Positions {
		cnt, ok := chip.Initial.Counters[p.ID]
		if !ok {
			return fmt.Errorf("初始状态缺少位置 %q 的计数", p.ID)
		}
		for _, sc := range p.Scopes {
			if _, ok := cnt[sc]; !ok {
				return fmt.Errorf("初始状态缺少位置 %q 范围 %q 的代次", p.ID, sc)
			}
		}
		for sc := range cnt {
			if !hasScope(p, sc) {
				return fmt.Errorf("初始状态位置 %q 含未声明范围 %q", p.ID, sc)
			}
		}
	}
	for _, o := range chip.Owners {
		ptr, ok := chip.Initial.Pointers[o]
		if !ok {
			return fmt.Errorf("初始状态缺少所有者 %q 的指针", o)
		}
		switch ptr.Phase {
		case spec.PhaseStable, spec.PhasePending, spec.PhaseCommitted:
		default:
			return fmt.Errorf("所有者 %q 的指针阶段非法: %q", o, ptr.Phase)
		}
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
	if !m.Safe(chip.Initial) {
		return errors.New("初始状态本身不满足安全不变量")
	}
	for i, alt := range chip.InitialAlternatives {
		if alt == nil {
			return fmt.Errorf("备选初始状态 %d 为空", i)
		}
		for _, p := range chip.Positions {
			cnt, ok := alt.Counters[p.ID]
			if !ok {
				return fmt.Errorf("备选初始状态 %d 缺少位置 %q 计数", i, p.ID)
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
		if !m.Safe(alt) {
			return fmt.Errorf("备选初始状态 %d 不满足安全不变量", i)
		}
	}
	return nil
}

func sameScopeSet(a, b map[spec.Scope]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

func hasScope(p spec.Position, sc spec.Scope) bool {
	for _, x := range p.Scopes {
		if x == sc {
			return true
		}
	}
	return false
}
