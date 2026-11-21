package commit

import "fmt"

// SpyDevice 包装物理底盘，记录全部盘面访问，供“不得偷看计划外底盘”的断言使用。
type SpyDevice struct {
	Inner Device
	Calls []SpyCall
}

// SpyCall 是一次实际底盘访问。
type SpyCall struct {
	Kind   string
	Sector SectorID
}

func (s *SpyDevice) Read(sector SectorID) ([]byte, error) {
	s.Calls = append(s.Calls, SpyCall{Kind: "read", Sector: sector})
	return s.Inner.Read(sector)
}

func (s *SpyDevice) Write(sector SectorID, payload []byte) error {
	s.Calls = append(s.Calls, SpyCall{Kind: "write", Sector: sector})
	return s.Inner.Write(sector, payload)
}

// Commit 先纯规划（不触盘），再用记录器逐拍执行并核对。
// 每个待写扇区恰写一次，结束时槽位全部释放；任何违规都返回错误。
func Commit(inst *Instance, dev Device) (*Plan, *Recorder, error) {
	plan, err := BuildPlan(inst)
	if err != nil {
		return nil, nil, err
	}
	rec := NewRecorder(inst, dev)
	for i := range plan.Ops {
		if err := rec.step(plan.Ops[i]); err != nil {
			return plan, rec, err
		}
	}
	if rec.Writes != len(inst.Targets) {
		return plan, rec, fmt.Errorf("commit: %d writes for %d targets", rec.Writes, len(inst.Targets))
	}
	if len(rec.slots) != 0 {
		return plan, rec, fmt.Errorf("commit: %d slots still occupied at end", len(rec.slots))
	}
	if rec.Reads != plan.Reads || rec.ReadCost != plan.ReadCost {
		return plan, rec, fmt.Errorf("commit: recorder metrics diverge from plan")
	}
	return plan, rec, nil
}

// ExpectedPayloads 依据一份入口盘快照，独立计算每个目标的期望最终扇区内容。
// 它只用于测试侧的“最终完整盘”核对，提交器自身从不调用它。
func ExpectedPayloads(inst *Instance, entry map[SectorID][]byte) (map[SectorID][]byte, error) {
	out := make(map[SectorID][]byte, len(inst.Targets))
	for t := range inst.Targets {
		tgt := &inst.Targets[t]
		payload := make([]byte, 0, inst.SectorSize)
		for f := range tgt.Fragments {
			frag := tgt.Fragments[f]
			if frag.Source == nil {
				payload = append(payload, frag.Constant...)
				continue
			}
			data, ok := entry[*frag.Source]
			if !ok {
				return nil, fmt.Errorf("commit: entry disk missing source %d", *frag.Source)
			}
			end := frag.SourceOffset + frag.Length
			if end > len(data) {
				return nil, fmt.Errorf("commit: source %d slice out of range", *frag.Source)
			}
			payload = append(payload, data[frag.SourceOffset:end]...)
		}
		if len(payload) != inst.SectorSize {
			return nil, fmt.Errorf("commit: target %d payload size mismatch", tgt.Address)
		}
		out[tgt.Address] = payload
	}
	return out, nil
}
