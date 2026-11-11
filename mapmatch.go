// Package mapmatch 提供基于隐马尔可夫模型（HMM）与 Viterbi 解码的
// 流式地图匹配能力：以道路图为输入，把带漂移的 GPS 轨迹点吸附到
// 车辆实际行驶的道路上。
//
// 基本用法：
//
//	g := mapmatch.NewGraph()
//	// ... 添加节点与路段 ...
//	m := mapmatch.NewMatcher(g, mapmatch.DefaultConfig())
//	for _, p := range points {
//		ready := m.Add(p) // 窗口确定或分段冲刷出的结果
//		_ = ready
//	}
//	tail := m.Flush() // 轨迹结束，冲刷剩余结果
package mapmatch

import "time"

// Point 是一个带时间戳的 GPS 观测点。
type Point struct {
	LatLon
	Time time.Time
}

// MatchResult 是单个输入点的匹配输出。
type MatchResult struct {
	Point   Point   // 原始输入点
	EdgeID  int64   // 匹配到的路段
	Pos     float64 // 吸附点沿路段的里程（米，从路段起点算起）
	Snapped LatLon  // 吸附到路段上的坐标
}
