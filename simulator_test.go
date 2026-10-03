package swimlab

import "testing"

func findObs(obs []Observation, kind ObservationKind, nodeID int) []int {
	var ticks []int
	for _, o := range obs {
		if o.Kind == kind && o.NodeID == nodeID {
			ticks = append(ticks, o.Tick)
		}
	}
	return ticks
}

func TestSuspectThenMark(t *testing.T) {
	sim := NewSimulator(Config{NumNodes: 2, MarkThreshold: 3})
	obs := sim.Run(6, []Injection{{Tick: 1, NodeID: 1, Kind: InjectCrash}})

	suspects := findObs(obs, ObsSuspect, 1)
	if len(suspects) != 1 || suspects[0] != 1 {
		t.Errorf("怀疑时刻 = %v, 期望 [1]", suspects)
	}
	marks := findObs(obs, ObsMark, 1)
	if len(marks) != 1 || marks[0] != 3 {
		t.Errorf("标记时刻 = %v, 期望 [3]", marks)
	}
}

func TestPacketLossSuspectedThenCleared(t *testing.T) {
	sim := NewSimulator(Config{NumNodes: 2, MarkThreshold: 3})
	obs := sim.Run(6, []Injection{{Tick: 1, NodeID: 1, Kind: InjectPacketLoss, Duration: 2}})

	suspects := findObs(obs, ObsSuspect, 1)
	if len(suspects) != 1 || suspects[0] != 1 {
		t.Errorf("怀疑时刻 = %v, 期望 [1]", suspects)
	}
	if marks := findObs(obs, ObsMark, 1); len(marks) != 0 {
		t.Errorf("短暂丢包不应被标记, 实际标记时刻 = %v", marks)
	}
	clears := findObs(obs, ObsClear, 1)
	if len(clears) != 1 || clears[0] != 3 {
		t.Errorf("澄清时刻 = %v, 期望 [3]", clears)
	}
}

func TestRecoverResetsDetectorView(t *testing.T) {
	sim := NewSimulator(Config{NumNodes: 2, MarkThreshold: 3})
	obs := sim.Run(10, []Injection{
		{Tick: 1, NodeID: 1, Kind: InjectCrash},
		{Tick: 6, NodeID: 1, Kind: InjectRecover},
	})

	marks := findObs(obs, ObsMark, 1)
	if len(marks) != 1 || marks[0] != 3 {
		t.Errorf("标记时刻 = %v, 期望 [3]", marks)
	}
	clears := findObs(obs, ObsClear, 1)
	if len(clears) != 1 || clears[0] != 6 {
		t.Errorf("恢复澄清时刻 = %v, 期望 [6]", clears)
	}
	// 恢复之后不应再产生任何观测。
	for _, o := range obs {
		if o.Tick > 6 {
			t.Errorf("恢复后仍存在观测: %+v", o)
		}
	}
}

func TestLongOutageMarksAfterThreshold(t *testing.T) {
	sim := NewSimulator(Config{NumNodes: 2, MarkThreshold: 3})
	// 丢包持续 3 个周期，达到标记阈值 —— 验证阈值语义。
	obs := sim.Run(8, []Injection{{Tick: 2, NodeID: 1, Kind: InjectPacketLoss, Duration: 3}})

	marks := findObs(obs, ObsMark, 1)
	if len(marks) != 1 || marks[0] != 4 {
		t.Errorf("标记时刻 = %v, 期望 [4]", marks)
	}
}
