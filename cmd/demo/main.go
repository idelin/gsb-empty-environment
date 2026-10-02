// 疾控流调演示:两名确诊者在某商场的轨迹相交追溯。
//
// 运行: go run ./cmd/demo
package main

import (
	"fmt"
	"time"

	"spacetime"
)

var pois = []spacetime.POI{
	{Name: "化妆品柜台", X: 50, Y: 8},
	{Name: "生鲜柜台", X: 92, Y: 50},
	{Name: "收银台", X: 16, Y: 95},
	{Name: "商场入口", X: 0, Y: 50},
}

// 确诊者甲:沿商场主通道绕行一圈(坐标单位:米,时间单位:秒)。
var caseA = &spacetime.Trajectory{
	EntityID: "确诊者甲",
	Samples: []spacetime.Sample{
		{T: 0, X: 0, Y: 0, Zone: "一层"},
		{T: 25, X: 100, Y: 0, Zone: "一层"},
		{T: 50, X: 100, Y: 100, Zone: "一层"},
		{T: 75, X: 0, Y: 100, Zone: "一层"},
		{T: 100, X: 0, Y: 0, Zone: "一层"},
	},
}

// 确诊者乙:入口 -> 化妆品柜台 -> 生鲜柜台 -> 收银台 -> 离开。
var caseB = &spacetime.Trajectory{
	EntityID: "确诊者乙",
	Samples: []spacetime.Sample{
		{T: 0, X: 0, Y: 50, Zone: "入口"},
		{T: 10, X: 50, Y: 8, Zone: "化妆品柜台"},
		{T: 16, X: 50, Y: 8, Zone: "化妆品柜台"},
		{T: 24, X: 90, Y: 20, Zone: "通道"},
		{T: 34, X: 92, Y: 50, Zone: "生鲜柜台"},
		{T: 40, X: 92, Y: 50, Zone: "生鲜柜台"},
		{T: 50, X: 60, Y: 90, Zone: "通道"},
		{T: 68, X: 16, Y: 95, Zone: "收银台"},
		{T: 74, X: 16, Y: 95, Zone: "收银台"},
		{T: 85, X: 0, Y: 40, Zone: "通道"},
		{T: 90, X: -20, Y: 40, Zone: "场外"},
		{T: 100, X: -20, Y: 40, Zone: "场外"},
	},
}

// 时间原点:2026-10-02 09:00:00。
var base = time.Date(2026, 10, 2, 9, 0, 0, 0, time.Local)

func clock(t float64) string {
	return base.Add(time.Duration(t * float64(time.Second))).Format("15:04:05")
}

func main() {
	engine := spacetime.NewEngine(5, 10, []byte("cdc-prover-secret-key"), pois)
	if err := engine.AddTrajectory(caseA); err != nil {
		panic(err)
	}
	if err := engine.AddTrajectory(caseB); err != nil {
		panic(err)
	}

	const threshold = 10.0 // 密接判定阈值:10 米
	start := time.Now()
	encounters, err := engine.ProveEncounters("确诊者甲", "确诊者乙", threshold)
	elapsed := time.Since(start)
	if err != nil {
		panic(err)
	}

	fmt.Printf("=== 时空相交追溯:确诊者甲 x 确诊者乙(阈值 %.0f 米)===\n", threshold)
	fmt.Printf("求交耗时: %v\n\n", elapsed)
	for i, enc := range encounters {
		fmt.Printf("相交时段 #%d  %s ~ %s (持续 %.1f 秒)\n",
			i+1, clock(enc.Interval.Start), clock(enc.Interval.End), enc.Interval.Duration())
		fmt.Printf("  地点: %s  相遇中心: (%.1f, %.1f)\n", enc.Location, enc.MidX, enc.MidY)
		fmt.Printf("  证书哈希: %s...\n", enc.Cert.Hash[:16])
		fmt.Printf("  证书签名: %s...\n\n", enc.Cert.Signature[:16])
	}

	certs := make([]spacetime.Certificate, len(encounters))
	for i, enc := range encounters {
		certs[i] = enc.Cert
	}
	fmt.Printf("证书链核验: %v (共 %d 张,链式绑定轨迹摘要 %s...)\n",
		engine.Prover().VerifyChain(certs), len(certs), caseA.Digest()[:12])
}
