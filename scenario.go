package swimlab

// AcceptanceScenario 构造验收场景：10 次真实节点宕机 + 5 次短暂网络丢包扰动。
//
// 检测时序（MarkThreshold = 3，即怀疑超时约 2 个周期）：
//   - 同相宕机：周期 t 的探测之前宕机，t 首次怀疑，t+2 标记 → MTTD 2 周期
//   - 异相宕机：周期 t 的探测刚结束后宕机，t+1 才首次失败，t+3 标记 → MTTD 3 周期
//
// 10 次宕机中 6 次同相、4 次异相，平均 MTTD = (6*2 + 4*3) / 10 = 2.4 周期。
// 丢包扰动持续 1~2 个周期，低于标记阈值，怀疑会被澄清，不产生误报。
func AcceptanceScenario() (cfg Config, events []FaultEvent, injections []Injection, periods int) {
	cfg = Config{NumNodes: 16, MarkThreshold: 3}
	outOfPhase := map[int]bool{1: true, 4: true, 6: true, 9: true}

	id := 0
	for i := 0; i < 10; i++ {
		occurredAt := 10 + i*12
		nodeID := 1 + i
		effectiveAt := occurredAt
		if outOfPhase[i] {
			// 宕机发生在周期 occurredAt 的探测刚结束之后，
			// 从下一周期起才可被观测到。
			effectiveAt = occurredAt + 1
		}
		events = append(events, FaultEvent{ID: id, Kind: CrashEvent, NodeID: nodeID, OccurredAt: occurredAt})
		injections = append(injections,
			Injection{Tick: effectiveAt, NodeID: nodeID, Kind: InjectCrash},
			Injection{Tick: effectiveAt + 5, NodeID: nodeID, Kind: InjectRecover},
		)
		id++
	}

	for j := 0; j < 5; j++ {
		occurredAt := 16 + j*24
		nodeID := 11 + j
		duration := 1 + j%2 // 交替持续 1、2 个周期
		events = append(events, FaultEvent{ID: id, Kind: DisturbanceEvent, NodeID: nodeID, OccurredAt: occurredAt})
		injections = append(injections,
			Injection{Tick: occurredAt, NodeID: nodeID, Kind: InjectPacketLoss, Duration: duration},
		)
		id++
	}

	return cfg, events, injections, 140
}
