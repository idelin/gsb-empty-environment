package netsec

import "fmt"

// ZeroStep 记录一次零耗时转移：派发的事件以及转移前后的指纹。
type ZeroStep struct {
	Event Event
	From  string
	To    string
}

// CycleEvidence 是“无限零耗时循环”的可复现证据。
type CycleEvidence struct {
	// Prefix 是进入循环之前的零耗时转移。
	Prefix []ZeroStep
	// Loop 是构成循环本体的转移；循环末尾的指纹等于 LoopStartAt。
	Loop []ZeroStep
	// LoopStartAt 是循环首次出现时的状态指纹。
	LoopStartAt string
	// FrozenClock 是整个循环期间从未前进的虚拟时刻。
	FrozenClock Time
}

// String 以可读形式输出循环证据，便于测试与人工审计。
func (c CycleEvidence) String() string {
	s := fmt.Sprintf("zero-duration cycle at frozen clock=%d, entry fingerprint=%q\n", c.FrozenClock, c.LoopStartAt)
	for _, st := range c.Prefix {
		s += fmt.Sprintf("  prefix: %q --%s--> %q\n", st.From, st.Event.Name, st.To)
	}
	for i, st := range c.Loop {
		s += fmt.Sprintf("  loop[%d]: %q --%s--> %q\n", i, st.From, st.Event.Name, st.To)
	}
	return s
}

// maxSearchSteps 是搜索的硬上限，防止模型不产生指纹重复时无限运行。
const maxSearchSteps = 1_000_000

// SearchZeroCycle 在“时钟冻结”的前提下反复派发零耗时事件，搜索会阻止
// 时间前进的无限零耗时循环。
//
// 判定依据是状态指纹（由宿主通过 Fingerprint 提供）在同一冻结时刻重复：
// 指纹重复意味着从同一状态还会再生出同一串零耗时事件，时钟因此永远无法
// 推进，待处理的超时事件被无限推迟。
//
// 返回 (nil, false) 表示在预算内没有发现循环（系统在当前时刻走到静止，
// 或达到搜索硬上限）。
func SearchZeroCycle(s *Scheduler, fingerprint func() string) (*CycleEvidence, error) {
	startClock := s.Clock()
	seen := make(map[string]int) // 指纹 -> 在 path 中的下标
	var path []ZeroStep

	for len(path) < maxSearchSteps {
		fp := fingerprint()
		if idx, ok := seen[fp]; ok {
			ev := CycleEvidence{
				Prefix:      append([]ZeroStep(nil), path[:idx]...),
				Loop:        append([]ZeroStep(nil), path[idx:]...),
				LoopStartAt: fp,
				FrozenClock: startClock,
			}
			return &ev, nil
		}
		seen[fp] = len(path)

		ev, ok, err := s.Step()
		if err != nil {
			return nil, err
		}
		if !ok {
			// 当前时刻已静止：时间可以继续推进，不存在零耗时循环。
			return nil, nil
		}
		if s.Clock() != startClock {
			// 防御性检查：Step 只派发事件、不推进时钟，正常情况下不会触发。
			return nil, fmt.Errorf("netsec: clock advanced during zero-step search from %d to %d", startClock, s.Clock())
		}
		path = append(path, ZeroStep{Event: ev, From: fp, To: fingerprint()})
	}
	return nil, nil
}
