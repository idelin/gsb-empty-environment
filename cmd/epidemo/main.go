// 疾控流调演示:两名确诊者在商场内的时空相交追溯。
//
// 场景:万象城购物中心,局部坐标系(米),z 为楼层高度(1F=0, 2F=5)。
// 确诊者 A(病例甲)与确诊者 B(病例乙)于 14:00-16:00 在商场活动,
// 引擎追溯两人在何时何地相距小于 10 米,并签发不可抵赖的地点证书。
package main

import (
	"fmt"
	"time"

	st "github.com/example/spacetime-prover/spacetime"
)

var zone = time.FixedZone("CST", 8*3600)

func ts(base int64, off float64) string {
	sec := int64(off)
	nsec := int64((off - float64(sec)) * 1e9)
	return time.Unix(base+sec, nsec).In(zone).Format("15:04:05.0")
}

func main() {
	base := time.Date(2026, 9, 30, 14, 0, 0, 0, zone).Unix()
	at := func(off int64, x, y, z float64) st.Sample {
		return st.Sample{T: base + off, P: st.Vec3{X: x, Y: y, Z: z}}
	}

	// 病例甲:14:00 从 1F 西门进入,沿动线逛店,16:00 离开。
	a := &st.Trajectory{EntityID: "CASE-A-0930", Samples: []st.Sample{
		at(0, 0, 0, 0),
		at(600, 100, 0, 0),
		at(1200, 100, 80, 5), // 上 2F 化妆品区
		at(1800, 20, 80, 5),
		at(2400, 20, 0, 0), // 回到 1F
		at(3600, 150, 0, 0),
		at(4800, 150, 100, 0), // 1F 东侧走廊
		at(6000, 60, 100, 0),
		at(7200, 60, 0, 0), // 1F 西区咖啡吧
	}}

	// 病例乙:大部分时间不在商场(远处),但有三段与甲近距离接触:
	//  1) 14:20:50-14:24:10  2F 化妆品柜台,相距 4m
	//  2) 15:00:50-15:02:00  1F 东侧扶梯口,相距 6m
	//  3) 15:41:40-15:50:00  1F 西区咖啡吧台,相距约 4.2m
	far := st.Vec3{X: 1000, Y: 1000, Z: 50}
	nearA := func(off int64, d st.Vec3) st.Sample {
		return st.Sample{T: base + off, P: a.PosAt(float64(base + off)).Add(d)}
	}
	b := &st.Trajectory{EntityID: "CASE-B-0930", Samples: []st.Sample{
		{T: base + 0, P: far}, {T: base + 1240, P: far},
		nearA(1250, st.Vec3{X: 4}), nearA(1450, st.Vec3{X: 4}),
		{T: base + 1460, P: far}, {T: base + 3640, P: far},
		nearA(3650, st.Vec3{Y: 6}), nearA(3720, st.Vec3{Y: 6}),
		{T: base + 3730, P: far}, {T: base + 6090, P: far},
		nearA(6100, st.Vec3{X: 3, Y: 3}), nearA(6600, st.Vec3{X: 3, Y: 3}),
		{T: base + 6610, P: far}, {T: base + 7200, P: far},
	}}

	// 柜台级地理围栏(商场平面图的语义层)。
	places := &st.PlaceRegistry{
		Fallback: "万象城·公共区域",
		Zones: []st.Zone{
			{Name: "万象城 2F 化妆品区·兰蔻专柜", Min: st.Vec3{X: 0, Y: 60, Z: 3}, Max: st.Vec3{X: 120, Y: 100, Z: 8}},
			{Name: "万象城 1F 东侧走廊·扶梯口", Min: st.Vec3{X: 130, Y: -10, Z: -2}, Max: st.Vec3{X: 170, Y: 110, Z: 2}},
			{Name: "万象城 1F 西区·星巴克咖啡吧台", Min: st.Vec3{X: 40, Y: 0, Z: -2}, Max: st.Vec3{X: 80, Y: 110, Z: 2}},
		},
	}

	// 建索引:60 秒时间切片 × 25 米网格。
	idx := st.NewGridIndex(60, 25)
	ids := idx.Build(a, b)

	// 疾控中心的证明器密钥(演示用固定种子,实际由 HSM 托管)。
	seed := make([]byte, 32)
	copy(seed, []byte("CDC-epidemiological-investigation"))
	prover := st.NewProver(idx, places, seed)

	const threshold = 10.0 // 密接判定阈值:10 米
	start := time.Now()
	bundle := prover.Prove(ids[0], ids[1], threshold)
	elapsed := time.Since(start)

	fmt.Println("=== 四维时空反向索引 · 历史相交证明 ===")
	fmt.Printf("查询: %s × %s, 阈值 %.0f 米\n", bundle.EntityA, bundle.EntityB, threshold)
	fmt.Printf("求交耗时: %v (%.3f ms)\n", elapsed, float64(elapsed.Nanoseconds())/1e6)
	fmt.Printf("发现 %d 段相交时段:\n\n", len(bundle.Certs))
	for _, c := range bundle.Certs {
		fmt.Printf("[证书 #%d] %s ~ %s (持续 %.0f 秒)\n",
			c.Seq, ts(base, c.Start-float64(base)), ts(base, c.End-float64(base)), c.End-c.Start)
		fmt.Printf("  地点: %s\n", c.Place)
		fmt.Printf("  最近距离: %.2f 米 @ %s, 位置 (%.1f, %.1f, %.1f)\n",
			c.MinDistM, ts(base, c.MinDistT-float64(base)), c.Location.X, c.Location.Y, c.Location.Z)
		fmt.Printf("  证书哈希: %s...\n", c.Hash[:16])
		fmt.Printf("  签名:     %s...\n\n", c.Signature[:16])
	}
	fmt.Printf("证明方公钥: %s...\n", bundle.PubKey[:32])
	fmt.Printf("捆扎哈希:   %s\n", bundle.BundleHash)
	fmt.Printf("链式验证:   %v\n", bundle.Verify())
}
