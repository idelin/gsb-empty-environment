package commit

import "fmt"

func (r *Recorder) occupied() []SectorID {
	out := make([]SectorID, 0, len(r.slots))
	for id := range r.slots {
		out = append(out, id)
	}
	return out
}

// Step 执行并核对一拍。
func (r *Recorder) Step(op Op) error {
	return r.step(op)
}

func (r *Recorder) step(op Op) error {
	from := r.head
	var tickCost int64

	switch op.Kind {
	case "read":
		if len(r.slots) >= r.inst.K {
			return fmt.Errorf("commit: read %d with all %d slots occupied", op.Sector, r.inst.K)
		}
		if _, ok := r.slots[op.Sector]; ok {
			return fmt.Errorf("commit: sector %d already resident", op.Sector)
		}
		if r.over[op.Sector] {
			return fmt.Errorf("commit: read covered entry sector %d", op.Sector)
		}
		cyl, ok := r.inst.Cylinders[op.Sector]
		if !ok || cyl != op.Cyl {
			return fmt.Errorf("commit: read %d cylinder mismatch", op.Sector)
		}
		data, err := r.dev.Read(op.Sector)
		if err != nil {
			return err
		}
		if len(data) != r.inst.SectorSize {
			return fmt.Errorf("commit: sector %d size %d != %d", op.Sector, len(data), r.inst.SectorSize)
		}
		cp := make([]byte, len(data))
		copy(cp, data)
		r.slots[op.Sector] = cp
		tickCost = r.inst.ReadCost(from, cyl)
		if tickCost < 0 {
			return fmt.Errorf("commit: negative read cost %d", tickCost)
		}
		r.ReadCost += tickCost
		r.Reads++
		r.head = cyl

	case "release":
		if _, ok := r.slots[op.Sector]; !ok {
			return fmt.Errorf("commit: release non-resident sector %d", op.Sector)
		}
		delete(r.slots, op.Sector)
		r.Releases++
		// 释放不移动磁头，也不产生费用。

	case "write":
		tgt := r.findTarget(op.Sector)
		if tgt == nil {
			return fmt.Errorf("commit: write to non-target sector %d", op.Sector)
		}
		if r.written[op.Sector] {
			return fmt.Errorf("commit: target %d written twice", op.Sector)
		}
		payload, err := r.assemble(tgt)
		if err != nil {
			return err
		}
		if len(payload) != r.inst.SectorSize {
			return fmt.Errorf("commit: assembled payload size %d != %d", len(payload), r.inst.SectorSize)
		}
		if err := r.dev.Write(op.Sector, payload); err != nil {
			return err
		}
		r.written[op.Sector] = true
		r.over[op.Sector] = true
		r.Writes++
		r.head = op.Cyl

	default:
		return fmt.Errorf("commit: unknown op kind %q", op.Kind)
	}

	r.Ticks++
	r.Log = append(r.Log, Tick{
		Index:    r.Ticks,
		Op:       op,
		HeadFrom: from,
		HeadTo:   r.head,
		Occupied: r.occupied(),
		ReadCost: tickCost,
	})
	return nil
}

func (r *Recorder) findTarget(addr SectorID) *Target {
	for t := range r.inst.Targets {
		if r.inst.Targets[t].Address == addr {
			return &r.inst.Targets[t]
		}
	}
	return nil
}

// assemble 模拟专用组装寄存器：写入瞬间所有引用源必须同时驻留，
// 碎片不跨指令保存；常量片段直接取自配方。
func (r *Recorder) assemble(tgt *Target) ([]byte, error) {
	out := make([]byte, 0, r.inst.SectorSize)
	for f := range tgt.Fragments {
		frag := tgt.Fragments[f]
		if frag.Source == nil {
			out = append(out, frag.Constant...)
			continue
		}
		data, ok := r.slots[*frag.Source]
		if !ok {
			return nil, fmt.Errorf("commit: write %d source %d not resident", tgt.Address, *frag.Source)
		}
		end := frag.SourceOffset + frag.Length
		if end > len(data) {
			return nil, fmt.Errorf("commit: source %d slice out of range", *frag.Source)
		}
		out = append(out, data[frag.SourceOffset:end]...)
	}
	return out, nil
}
