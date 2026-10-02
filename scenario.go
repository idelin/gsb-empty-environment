package main

// DefaultScenario 构建验收场景：
//   - 16 节点集群，怀疑超时 2 个协议周期；
//   - 10 次真实节点宕机：6 次在周期探测前（偏移 0.0）注入，检测时延 2 周期；
//     4 次在周期探测后（偏移 0.9）注入，需等到下一周期才被发觉，检测时延 3 周期；
//     平均 MTTD = (6*2 + 4*3) / 10 = 2.4 周期；
//   - 5 次短暂网络丢包扰动：3 次仅覆盖直接 ping 窗口（被间接 ping 吸收），
//     2 次覆盖整周期（触发瞬时怀疑，但在超时前被澄清），均不产生误报。
func DefaultScenario() (numNodes, suspicionTimeout int, injections []Injection, cycles int) {
	numNodes = 16
	suspicionTimeout = 2

	for i := 0; i < 10; i++ {
		offset := 0.0
		if i >= 6 {
			offset = 0.9
		}
		injections = append(injections, Injection{
			ID:     i,
			Type:   CrashInjection,
			Target: i,
			Cycle:  10 + i*5,
			Offset: offset,
		})
	}

	for j := 0; j < 5; j++ {
		offset, duration := 0.1, 0.4 // 仅覆盖直接 ping 时点
		if j >= 3 {
			offset, duration = 0.0, 1.0 // 覆盖整个周期的丢包
		}
		injections = append(injections, Injection{
			ID:       10 + j,
			Type:     PacketLossInjection,
			Target:   10 + j,
			Cycle:    12 + j*7,
			Offset:   offset,
			Duration: duration,
		})
	}

	cycles = 70
	return numNodes, suspicionTimeout, injections, cycles
}
