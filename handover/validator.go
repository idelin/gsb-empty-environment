package handover

import "fmt"

// simState 是验证器内部的逐槽状态：nil 指针表示空槽。
type simState map[int]*Handle

// Validate 从零开始逐步模拟计划，检查全部安全不变量：
//   - 仅使用宿主提供的唯一临时槽，且跨权限边界前临时槽必须为空；
//   - 任何写入都不得覆盖仍占用的源槽；
//   - 并发交换的槽对互不相交；
//   - 最终每个角色目标槽持有所需句柄，且所有未选句柄均带 cloexec；
//   - 最终没有额外继承槽，临时槽已释放。
func (p *Plan) Validate(initial *Table) error {
	if p == nil {
		return fmt.Errorf("%w: nil plan", ErrInvalidPlan)
	}
	if initial == nil {
		return fmt.Errorf("%w: nil initial table", ErrInvalidPlan)
	}

	state := make(simState, len(initial.Handles))
	for i := range initial.Handles {
		h := initial.Handles[i]
		cp := h
		state[h.Slot] = &cp
	}

	tempInUse := false
	for step, op := range p.Ops {
		switch op.Kind {
		case OpSwap:
			a, b := state[op.A], state[op.B]
			if a == nil || b == nil {
				return p.bad(step, op, "swap requires two occupied slots")
			}
			state[op.A], state[op.B] = b, a

		case OpParSwap:
			seen := make(map[int]struct{})
			for _, pair := range op.Pairs {
				if state[pair[0]] == nil || state[pair[1]] == nil {
					return p.bad(step, op, "parallel swap requires occupied slots")
				}
				for _, slot := range pair {
					if _, dup := seen[slot]; dup {
						return p.bad(step, op, "parallel swap pairs must be disjoint")
					}
					seen[slot] = struct{}{}
				}
			}
			for _, pair := range op.Pairs {
				state[pair[0]], state[pair[1]] = state[pair[1]], state[pair[0]]
			}

		case OpLoad:
			if op.A != p.TempSlot {
				return p.bad(step, op, "load must use the single host temp slot")
			}
			if tempInUse || state[op.A] != nil {
				return p.bad(step, op, "temp slot must be empty before load")
			}
			if state[op.B] == nil {
				return p.bad(step, op, "load source must be occupied")
			}
			state[op.A] = state[op.B]
			state[op.B] = nil
			tempInUse = true

		case OpMove:
			if state[op.A] == nil {
				return p.bad(step, op, "move source must be occupied")
			}
			if state[op.B] != nil {
				return p.bad(step, op, "move would overwrite a slot still needed")
			}
			state[op.B] = state[op.A]
			state[op.A] = nil

		case OpStore:
			if op.A != p.TempSlot || !tempInUse || state[op.A] == nil {
				return p.bad(step, op, "store must drain the occupied temp slot")
			}
			if state[op.B] != nil {
				return p.bad(step, op, "store would overwrite a slot still needed")
			}
			state[op.B] = state[op.A]
			state[op.A] = nil
			tempInUse = false

		case OpDropTemp:
			if op.A != p.TempSlot {
				return p.bad(step, op, "drop must reference the temp slot")
			}
			if tempInUse || state[op.A] != nil {
				return p.bad(step, op, "temp slot must be empty before permission boundary")
			}

		case OpBarrier:
			if tempInUse || state[p.TempSlot] != nil {
				return p.bad(step, op, "temp slot must be empty crossing permission boundary")
			}

		case OpSetCloexec:
			if state[op.A] == nil {
				return p.bad(step, op, "setcloexec target must be occupied")
			}
			state[op.A].Cloexec = op.On

		case OpClose:
			if state[op.A] == nil {
				return p.bad(step, op, "close target must be occupied")
			}
			state[op.A] = nil

		default:
			return p.bad(step, op, "unknown op kind")
		}
	}

	if tempInUse || state[p.TempSlot] != nil {
		return fmt.Errorf("%w: temp slot %d not released", ErrInvalidPlan, p.TempSlot)
	}

	// 终态核对：角色目标槽持有期望句柄；未选占用槽必须 cloexec。
	expectedHandles := make(map[int]Handle, len(p.Assignments))
	for _, a := range p.Assignments {
		expectedHandles[a.Target] = Handle{
			Slot:   a.Target,
			Object: a.Object,
			Perm:   a.Perm,
		}
	}

	for slot, h := range state {
		if h == nil {
			continue
		}
		if want, ok := expectedHandles[slot]; ok {
			if h.Object != want.Object || h.Perm != want.Perm {
				return fmt.Errorf("%w: target slot %d holds %s/%s, want %s/%s",
					ErrInvalidPlan, slot, h.Object, h.Perm, want.Object, want.Perm)
			}
			if h.Cloexec {
				return fmt.Errorf("%w: selected handle at slot %d must be inherited", ErrInvalidPlan, slot)
			}
			continue
		}
		if !h.Cloexec {
			return fmt.Errorf("%w: unselected handle at slot %d would be inherited", ErrInvalidPlan, slot)
		}
	}

	for slot := range expectedHandles {
		if state[slot] == nil {
			return fmt.Errorf("%w: target slot %d is empty after plan", ErrInvalidPlan, slot)
		}
	}
	return nil
}

func (p *Plan) bad(step int, op Op, msg string) error {
	return fmt.Errorf("%w: step %d %s: %s", ErrInvalidPlan, step, op, msg)
}
