package mpu

// PlanUpdate 规划从当前 MPU 状态切换到目标区域集合的寄存器写序列。
//
// 时序分三个阶段，每个阶段都在微型地址空间上穷举验证：
//  1. 收紧（tighten）：需要变更的槽位要么禁用，要么把 RASR 权限改为
//     PermNone，每一步的可写/可执行集合只减不增；
//  2. 几何（geometry）：槽位在禁用状态下写 RBAR，再以 PermNone 使能并
//     写入目标 SRD，新覆盖到的地址一律先拒绝；
//  3. 提升（raise）：逐槽位把 RASR 权限提升到目标值，每一步的 W/X
//     集合必须始终是最终目标 W/X 集合的子集。
//
// 若不存在满足上述不变量的写入顺序，返回 ErrUnsafeTransition，
// 并在 TransitionError 中给出阶段、槽位与见证地址，固件层应放弃切换。
func (d *Device) PlanUpdate(target []Region) (*Plan, error) {
	return d.planUpdate(target, true)
}

// planUpdate 允许通过 noneEncoding 关闭“PermNone 使能区域”这一表达，
// 用于在测试中构造并验证 ErrUnsafeTransition 路径。
func (d *Device) planUpdate(target []Region, noneEncoding bool) (*Plan, error) {
	targetSlots, err := d.buildTarget(target)
	if err != nil {
		return nil, err
	}

	work := d.clone()
	initial := d.snapshot()
	var steps []Step

	if err := planTightenPhase(work, initial, targetSlots, noneEncoding, &steps); err != nil {
		return nil, err
	}
	if err := planGeometryPhase(work, initial, targetSlots, &steps); err != nil {
		return nil, err
	}
	if err := planRaisePhase(work, targetSlots, &steps); err != nil {
		return nil, err
	}

	plan := &Plan{
		SpaceSize: d.spaceSize,
		Initial:   initial,
		Target:    targetSlots,
		Steps:     steps,
	}
	if err := VerifyPlan(plan); err != nil {
		return nil, err
	}
	return plan, nil
}

func (d *Device) buildTarget(regions []Region) ([]SlotState, error) {
	if len(regions) > len(d.slots) {
		return nil, &MapError{Kind: MapCapacity, Detail: "target region count exceeds DREGION"}
	}
	slots := make([]SlotState, len(d.slots))
	used := make(map[int]bool, len(regions))
	for _, r := range regions {
		if err := validateRegion(r, d.spaceSize); err != nil {
			return nil, err
		}
		if r.Slot < 0 || r.Slot >= len(d.slots) || used[r.Slot] {
			return nil, &MapError{Kind: MapOutOfRange, Addr: r.Base,
				Detail: "target region slot number out of range or duplicated"}
		}
		used[r.Slot] = true
		slots[r.Slot] = SlotState{Enabled: true, Region: r}
	}
	// 未在目标中出现的槽位显式标记为禁用（区域字段清零），
	// 与最终 RASR.Enable=0 的硬件状态一致，避免残留旧几何。
	for i := range slots {
		if !used[i] {
			slots[i] = SlotState{Enabled: false}
		}
	}
	return slots, nil
}

// slotChange 描述一个槽位从当前状态到目标状态需要做的事。
type slotChange struct {
	slot        int
	target      SlotState
	geometry    bool // Base/SizeLog2/SRD 是否变化
	permChange  bool // 权限是否变化（含禁用/使能切换）
	alreadyNone bool // 当前已使能且为 PermNone，收紧阶段无需动作
}

func classifySlots(initial, target []SlotState, spaceSize uint64) []slotChange {
	var out []slotChange
	for i := range initial {
		cur, tgt := initial[i], target[i]
		switch {
		case !cur.Enabled && !tgt.Enabled:
			continue
		case cur.Enabled && !tgt.Enabled:
			out = append(out, slotChange{slot: i, target: tgt, permChange: true})
		case !cur.Enabled && tgt.Enabled:
			out = append(out, slotChange{slot: i, target: tgt, geometry: true, permChange: true})
		default:
			g := cur.Region.Base != tgt.Region.Base ||
				cur.Region.SizeLog2 != tgt.Region.SizeLog2 ||
				cur.Region.SRD != tgt.Region.SRD
			p := cur.Region.Perm != tgt.Region.Perm
			if !g && !p {
				continue
			}
			alreadyNone := cur.Region.Perm == PermNone
			// 即使几何完全不变，只要该槽位在目标里承载的 W/X 地址中有
			// 任何地址在初始有效映射下并不具备对应权限，原地提升 RASR
			// 就会短暂越权。此类槽位必须走“禁用→以 None 重配→提升”
			// 的完整周期。判定依据是最终目标映射，而非中间状态。
			if !g && !alreadyNone && grantsBeyondInitial(tgt.Region, initial, target, spaceSize) {
				g = true
			}
			out = append(out, slotChange{
				slot: i, target: tgt, geometry: g, permChange: p, alreadyNone: alreadyNone,
			})
		}
	}
	return out
}

// grantsBeyondInitial 报告区域 r 携带 W 或 X 的使能地址中，是否存在
// 初始有效映射不具备、但最终目标映射确实需要该权限的地址。
func grantsBeyondInitial(r Region, initial, target []SlotState, spaceSize uint64) bool {
	cur := &Device{spaceSize: spaceSize, slots: cloneSlots(initial)}
	tgtDev := &Device{spaceSize: spaceSize, slots: cloneSlots(target)}
	for a := uint64(0); a < spaceSize; a++ {
		if !r.covers(a) {
			continue
		}
		eff := cur.Effective(a)
		want := tgtDev.Effective(a)
		if r.Perm.Contains(PermW) && want.Contains(PermW) && !eff.Contains(PermW) {
			return true
		}
		if r.Perm.Contains(PermX) && want.Contains(PermX) && !eff.Contains(PermX) {
			return true
		}
	}
	return false
}

// onlyTightens 验证 next 相对 cur 的 W/X 集合只减不增，
// 返回首个见证地址与越界权限位（bit 为 0 表示通过）。
func onlyTightens(cur, next *Device) (uint64, Perm) {
	for a := 0; a < int(cur.spaceSize); a++ {
		addr := uint64(a)
		ce, ne := cur.Effective(addr), next.Effective(addr)
		if ne.Contains(PermW) && !ce.Contains(PermW) {
			return addr, PermW
		}
		if ne.Contains(PermX) && !ce.Contains(PermX) {
			return addr, PermX
		}
	}
	return 0, 0
}

// withinTarget 验证 cur 的 W/X 集合是目标 W/X 集合的子集。
func withinTarget(cur, target *Device) (uint64, Perm) {
	for a := 0; a < int(cur.spaceSize); a++ {
		addr := uint64(a)
		ce, te := cur.Effective(addr), target.Effective(addr)
		if ce.Contains(PermW) && !te.Contains(PermW) {
			return addr, PermW
		}
		if ce.Contains(PermX) && !te.Contains(PermX) {
			return addr, PermX
		}
	}
	return 0, 0
}

func unsafeErr(phase string, slot int, addr uint64, bit Perm) error {
	what := "write"
	if bit == PermX {
		what = "execute"
	}
	return &TransitionError{
		Phase:  phase,
		Slot:   slot,
		Addr:   addr,
		Detail: "step would transiently widen " + what + " permission",
	}
}

// planTightenPhase 禁用几何要变更的槽位，把纯权限变更槽位收紧到
// PermNone；写入顺序通过回溯穷举，任何一步都不得扩大 W/X。
func planTightenPhase(work *Device, initial, target []SlotState,
	noneEncoding bool, steps *[]Step) error {

	changes := classifySlots(initial, target, work.spaceSize)
	initialDev := &Device{spaceSize: work.spaceSize, slots: cloneSlots(initial)}
	pending := make(map[int]slotChange, len(changes))
	for _, c := range changes {
		if c.alreadyNone {
			// 区域已使能且权限为 PermNone：它已在最紧状态，禁用反而会
			// 让低优先级区域暴露。跳过收紧，几何阶段在保持 PermNone 的
			// 前提下直接改写 RBAR/RASR。
			continue
		}
		pending[c.slot] = c
	}

	var dfs func() bool
	dfs = func() bool {
		if len(pending) == 0 {
			return true
		}
		for _, c := range changes {
			if _, ok := pending[c.slot]; !ok {
				continue
			}
			type action struct {
				state SlotState
				kind  StepKind
			}
			var actions []action
			if c.geometry {
				// 其余几何变更必须先禁用区域，符合 ARM MPU 编程约束。
				actions = append(actions, action{
					kind:  StepDisable,
					state: SlotState{Enabled: false, Region: work.slots[c.slot].Region},
				})
			} else {
				none := work.slots[c.slot].Region
				none.Perm = PermNone
				if noneEncoding {
					// 保持使能但显式拒绝，可避免向低优先级区域回落。
					actions = append(actions, action{
						kind:  StepTighten,
						state: SlotState{Enabled: true, Region: none},
					})
				}
				actions = append(actions, action{
					kind:  StepDisable,
					state: SlotState{Enabled: false, Region: work.slots[c.slot].Region},
				})
			}
			for _, act := range actions {
				trial := work.clone()
				before := work.slots[c.slot]
				trial.slots[c.slot] = act.state
				if _, bit := onlyTightens(initialDev, trial); bit != 0 {
					continue
				}
				*steps = append(*steps, Step{
					Kind: act.kind, Slot: c.slot, Before: before, After: act.state,
				})
				delete(pending, c.slot)
				if dfs() {
					work.slots = trial.slots
					return true
				}
				*steps = (*steps)[:len(*steps)-1]
				pending[c.slot] = c
			}
		}
		return false
	}

	if !dfs() {
		// 回溯失败说明各候选均不安全；给出第一个可复现的见证地址。
		for _, c := range changes {
			trial := work.clone()
			before := work.slots[c.slot]
			trial.slots[c.slot] = SlotState{Enabled: false, Region: before.Region}
			if addr, bit := onlyTightens(initialDev, trial); bit != 0 {
				return unsafeErr("tighten", c.slot, addr, bit)
			}
		}
		return &TransitionError{Phase: "tighten", Detail: "no safe tightening order exists"}
	}
	return nil
}

// planGeometryPhase 在禁用槽位上写 RBAR，再以 PermNone 使能并写入 SRD。
// 进入本阶段时所有变更槽位均已拒绝访问，几何写入不会扩大 W/X；
// 仍逐地址复验，作为纵深防御。
func planGeometryPhase(work *Device, initial, target []SlotState, steps *[]Step) error {
	initialDev := &Device{spaceSize: work.spaceSize, slots: cloneSlots(initial)}
	for i := range target {
		cur, tgt := work.slots[i], target[i]
		if !tgt.Enabled {
			// 目标禁用：确保 RASR.Enable 已清零并记录该寄存器写。
			if cur.Enabled {
				next := SlotState{Enabled: false}
				trial := work.clone()
				trial.slots[i] = next
				if addr, bit := onlyTightens(initialDev, trial); bit != 0 {
					return unsafeErr("geometry", i, addr, bit)
				}
				*steps = append(*steps, Step{Kind: StepDisable, Slot: i, Before: cur, After: next})
				work.slots[i] = next
			}
			continue
		}
		if cur.Enabled && cur.Region == tgt.Region {
			continue // 几何未变，无需动作
		}
		before := cur

		// 1) 禁用状态下写 RBAR（基址与尺寸），此时 RASR.Enable=0。
		if !cur.Enabled || cur.Region.Base != tgt.Region.Base ||
			cur.Region.SizeLog2 != tgt.Region.SizeLog2 {
			next := SlotState{Enabled: false, Region: tgt.Region}
			trial := work.clone()
			trial.slots[i] = next
			if addr, bit := onlyTightens(work, trial); bit != 0 {
				return unsafeErr("geometry", i, addr, bit)
			}
			*steps = append(*steps, Step{Kind: StepWriteRBAR, Slot: i, Before: before, After: next})
			work.slots[i] = next
			before = next
		}

		// 2) 以 PermNone 使能并写入目标 SRD：新覆盖地址先全部拒绝。
		noneRegion := tgt.Region
		noneRegion.Perm = PermNone
		next := SlotState{Enabled: true, Region: noneRegion}
		trial := work.clone()
		trial.slots[i] = next
		if addr, bit := onlyTightens(initialDev, trial); bit != 0 {
			return unsafeErr("geometry", i, addr, bit)
		}
		*steps = append(*steps, Step{Kind: StepEnableNone, Slot: i, Before: before, After: next})
		work.slots[i] = next
	}
	return nil
}

// planRaisePhase 逐槽位把 PermNone 提升到目标权限。提升顺序通过回溯
// 穷举，每一步的 W/X 集合必须是最终目标 W/X 集合的子集。
func planRaisePhase(work *Device, target []SlotState, steps *[]Step) error {
	var pending []int
	for i := range target {
		cur, tgt := work.slots[i], target[i]
		if tgt.Enabled && cur.Enabled && cur.Region.Perm != tgt.Region.Perm {
			pending = append(pending, i)
		}
	}
	targetDev := &Device{spaceSize: work.spaceSize, slots: cloneSlots(target)}
	done := make(map[int]bool, len(pending))

	var dfs func() bool
	dfs = func() bool {
		if len(done) == len(pending) {
			return true
		}
		for _, slot := range pending {
			if done[slot] {
				continue
			}
			trial := work.clone()
			before := work.slots[slot]
			trial.slots[slot] = target[slot]
			if _, bit := withinTarget(trial, targetDev); bit != 0 {
				continue
			}
			*steps = append(*steps, Step{
				Kind: StepRaiseAccess, Slot: slot, Before: before, After: target[slot],
			})
			done[slot] = true
			if dfs() {
				work.slots = trial.slots
				return true
			}
			delete(done, slot)
			*steps = (*steps)[:len(*steps)-1]
		}
		return false
	}

	if !dfs() {
		for _, slot := range pending {
			trial := work.clone()
			trial.slots[slot] = target[slot]
			if addr, bit := withinTarget(trial, targetDev); bit != 0 {
				return unsafeErr("raise", slot, addr, bit)
			}
		}
		return &TransitionError{Phase: "raise", Detail: "no safe permission raise order exists"}
	}
	return nil
}

func cloneSlots(s []SlotState) []SlotState {
	out := make([]SlotState, len(s))
	copy(out, s)
	return out
}
