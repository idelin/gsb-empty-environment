package mpu

// VerifyPlan 独立重放整个计划，在每次寄存器写入后穷举微型地址空间：
//   - 收紧/几何阶段（第一次 StepRaiseAccess 之前），W/X 只能相对初始
//     状态减少，绝不允许短暂扩大可写或可执行范围；
//   - 进入提升阶段后，每一步的 W/X 必须始终包含于最终目标集合；
//   - 重放结束时逐地址比对，有效权限必须与目标完全一致。
//
// 任何不变量被破坏都会返回包裹 ErrUnsafeTransition 的 TransitionError。
func VerifyPlan(p *Plan) error {
	dev := replay(p.SpaceSize, p.Initial)
	initialDev := replay(p.SpaceSize, p.Initial)
	targetDev := &Device{spaceSize: p.SpaceSize, slots: cloneSlots(p.Target)}

	raising := false
	for si, step := range p.Steps {
		if step.Kind == StepRaiseAccess {
			raising = true
		}
		dev.slots[step.Slot] = step.After

		if !raising {
			if addr, bit := onlyTightens(initialDev, dev); bit != 0 {
				return &TransitionError{
					Phase:  "pre-raise step " + itoa(si),
					Slot:   step.Slot,
					Addr:   addr,
					Detail: "widen detected while tightening/geometry: " + bitName(bit),
				}
			}
		} else {
			if addr, bit := withinTarget(dev, targetDev); bit != 0 {
				return &TransitionError{
					Phase:  "raise step " + itoa(si),
					Slot:   step.Slot,
					Addr:   addr,
					Detail: "permission outside target while raising: " + bitName(bit),
				}
			}
		}
	}

	for a := 0; a < int(p.SpaceSize); a++ {
		addr := uint64(a)
		if got, want := dev.Effective(addr), targetDev.Effective(addr); got != want {
			return &TransitionError{
				Phase:  "final",
				Addr:   addr,
				Detail: "effective perm " + got.String() + " != target " + want.String(),
			}
		}
	}

	// 逐槽位比对完整寄存器状态，确保不存在残留的使能位或旧几何。
	for slot := range p.Target {
		got, want := dev.slots[slot], p.Target[slot]
		if got.Enabled != want.Enabled {
			return &TransitionError{
				Phase: "final", Slot: slot,
				Detail: "slot enable bit does not match target",
			}
		}
		if got.Enabled && got.Region != want.Region {
			return &TransitionError{
				Phase: "final", Slot: slot,
				Detail: "slot registers do not match target region",
			}
		}
	}
	return nil
}

func bitName(b Perm) string {
	if b == PermW {
		return "write"
	}
	if b == PermX {
		return "execute"
	}
	return "permission"
}
