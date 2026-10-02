package spacetime

import (
	"math"
	"math/rand"
	"testing"
)

// mallPOIs 是演示商场的柜台兴趣点表。
var mallPOIs = []POI{
	{Name: "化妆品柜台", X: 50, Y: 8},
	{Name: "生鲜柜台", X: 92, Y: 50},
	{Name: "收银台", X: 16, Y: 95},
	{Name: "商场入口", X: 0, Y: 50},
}

// 确诊者甲:沿商场主通道绕行一圈(单位:米,时间:秒)。
func trajectoryA() *Trajectory {
	return &Trajectory{
		EntityID: "确诊者甲",
		Samples: []Sample{
			{T: 0, X: 0, Y: 0, Zone: "一层"},
			{T: 25, X: 100, Y: 0, Zone: "一层"},
			{T: 50, X: 100, Y: 100, Zone: "一层"},
			{T: 75, X: 0, Y: 100, Zone: "一层"},
			{T: 100, X: 0, Y: 0, Zone: "一层"},
		},
	}
}

// 确诊者乙:入口 -> 化妆品柜台 -> 生鲜柜台 -> 收银台 -> 离开。
// 两人在三个柜台附近各发生一次 10 米内的近距离接触。
func trajectoryB() *Trajectory {
	return &Trajectory{
		EntityID: "确诊者乙",
		Samples: []Sample{
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
}

func newMallEngine(t *testing.T) *Engine {
	t.Helper()
	engine := NewEngine(5, 10, []byte("cdc-prover-secret-key"), mallPOIs)
	if err := engine.AddTrajectory(trajectoryA()); err != nil {
		t.Fatal(err)
	}
	if err := engine.AddTrajectory(trajectoryB()); err != nil {
		t.Fatal(err)
	}
	return engine
}

// TestMallTraceInvestigation 是验收场景:疾控流调,输入两名确诊者
// 的轨迹,引擎应精确输出 3 个相距小于 10 米的时段及对应柜台。
func TestMallTraceInvestigation(t *testing.T) {
	engine := newMallEngine(t)
	encounters, err := engine.ProveEncounters("确诊者甲", "确诊者乙", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(encounters) != 3 {
		t.Fatalf("expected 3 encounters, got %d: %+v", len(encounters), encounters)
	}

	// 每段相交的理论中心时刻与地点。
	want := []struct {
		mid      float64
		location string
	}{
		{12.5, "化妆品柜台"},
		{37.5, "生鲜柜台"},
		{71.0, "收银台"},
	}
	for i, w := range want {
		enc := encounters[i]
		if enc.Interval.Start >= w.mid || enc.Interval.End <= w.mid {
			t.Errorf("encounter %d: interval [%.3f, %.3f] should contain %.1f",
				i, enc.Interval.Start, enc.Interval.End, w.mid)
		}
		if enc.Location != w.location {
			t.Errorf("encounter %d: location = %q, want %q", i, enc.Location, w.location)
		}
		if enc.Interval.Duration() <= 0 {
			t.Errorf("encounter %d: non-positive duration", i)
		}
	}

	// 相交区间边界精确性:第一段时间窗理论值约为 (11, 14)。
	if math.Abs(encounters[0].Interval.Start-11) > 0.01 ||
		math.Abs(encounters[0].Interval.End-14) > 0.01 {
		t.Errorf("encounter 0: interval [%.4f, %.4f], want approx [11, 14]",
			encounters[0].Interval.Start, encounters[0].Interval.End)
	}

	// 证书链整体可核验。
	certs := make([]Certificate, len(encounters))
	for i, enc := range encounters {
		certs[i] = enc.Cert
	}
	if !engine.Prover().VerifyChain(certs) {
		t.Error("certificate chain verification failed")
	}
}

// TestCertificateNonRepudiation 验证证书不可篡改、不可伪造。
func TestCertificateNonRepudiation(t *testing.T) {
	engine := newMallEngine(t)
	encounters, err := engine.ProveEncounters("确诊者甲", "确诊者乙", 10)
	if err != nil {
		t.Fatal(err)
	}
	prover := engine.Prover()

	// 篡改地点字段 -> 哈希校验失败。
	tampered := encounters[0].Cert
	tampered.Location = "停车场"
	if prover.Verify(tampered) {
		t.Error("tampered certificate should not verify")
	}

	// 篡改相交时刻 -> 哈希校验失败。
	tampered = encounters[1].Cert
	tampered.Start += 1
	if prover.Verify(tampered) {
		t.Error("tampered certificate should not verify")
	}

	// 错误私钥 -> 签名校验失败。
	forger := NewProver([]byte("forged-key"))
	if forger.Verify(encounters[0].Cert) {
		t.Error("certificate should not verify under a foreign key")
	}

	// 断链(调换顺序)-> 链校验失败。
	certs := []Certificate{encounters[1].Cert, encounters[0].Cert, encounters[2].Cert}
	if prover.VerifyChain(certs) {
		t.Error("reordered chain should not verify")
	}

	// 轨迹数据被替换 -> 摘要与证书摘要不一致。
	fake := trajectoryA()
	fake.Samples[2].X += 5
	if fake.Digest() == encounters[0].Cert.DigestA {
		t.Error("modified trajectory must produce a different digest")
	}
}

// TestIndexMatchesBruteForce 性质测试:随机轨迹下,索引裁剪后的
// 求交结果必须与全量暴力求交完全一致(不漏报、不误报)。
func TestIndexMatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 50; trial++ {
		a := randomTrajectory(rng, "A", 8+trial%5)
		b := randomTrajectory(rng, "B", 8+trial%7)
		threshold := 5 + rng.Float64()*20

		engine := NewEngine(3, 8, []byte("k"), nil)
		if err := engine.AddTrajectory(a); err != nil {
			t.Fatal(err)
		}
		if err := engine.AddTrajectory(b); err != nil {
			t.Fatal(err)
		}
		got, err := engine.ProveEncounters("A", "B", threshold)
		if err != nil {
			t.Fatal(err)
		}
		want := IntersectIntervals(a, b, threshold, nil) // 暴力基准

		if len(got) != len(want) {
			t.Fatalf("trial %d: indexed got %d intervals, brute force got %d",
				trial, len(got), len(want))
		}
		for i := range want {
			if math.Abs(got[i].Interval.Start-want[i].Start) > 1e-6 ||
				math.Abs(got[i].Interval.End-want[i].End) > 1e-6 {
				t.Errorf("trial %d interval %d: indexed [%.6f, %.6f], brute [%.6f, %.6f]",
					trial, i, got[i].Interval.Start, got[i].Interval.End,
					want[i].Start, want[i].End)
			}
		}
	}
}

func randomTrajectory(rng *rand.Rand, id string, n int) *Trajectory {
	tr := &Trajectory{EntityID: id}
	t0 := rng.Float64() * 10
	for i := 0; i < n; i++ {
		tr.Samples = append(tr.Samples, Sample{
			T: t0,
			X: rng.Float64() * 100,
			Y: rng.Float64() * 100,
		})
		t0 += 2 + rng.Float64()*8
	}
	return tr
}

// TestPositionAt 验证线性插值与越界行为。
func TestPositionAt(t *testing.T) {
	tr := &Trajectory{EntityID: "t", Samples: []Sample{
		{T: 0, X: 0, Y: 0},
		{T: 10, X: 20, Y: 40},
	}}
	x, y, ok := tr.PositionAt(5)
	if !ok || x != 10 || y != 20 {
		t.Errorf("PositionAt(5) = (%v, %v, %v), want (10, 20, true)", x, y, ok)
	}
	if _, _, ok := tr.PositionAt(11); ok {
		t.Error("PositionAt(11) should be out of range")
	}
}

// TestIntersectIntervalsAnalytic 验证解析求根的精确性:
// 甲沿 x 轴匀速运动,乙垂直穿越,最近距离为 0。
func TestIntersectIntervalsAnalytic(t *testing.T) {
	a := &Trajectory{EntityID: "a", Samples: []Sample{
		{T: 0, X: 0, Y: 0}, {T: 10, X: 10, Y: 0},
	}}
	b := &Trajectory{EntityID: "b", Samples: []Sample{
		{T: 0, X: 5, Y: -10}, {T: 10, X: 5, Y: 10},
	}}
	// 距离平方 = 5(t-5)^2,阈值 2 -> |t-5| < 2/sqrt(5)。
	intervals := IntersectIntervals(a, b, 2, nil)
	if len(intervals) != 1 {
		t.Fatalf("expected 1 interval, got %d", len(intervals))
	}
	margin := 2 / math.Sqrt(5)
	if math.Abs(intervals[0].Start-(5-margin)) > 1e-9 ||
		math.Abs(intervals[0].End-(5+margin)) > 1e-9 {
		t.Errorf("interval [%.9f, %.9f], want [%.9f, %.9f]",
			intervals[0].Start, intervals[0].End, 5-margin, 5+margin)
	}
}

// TestUnknownEntity 与阈值校验。
func TestProveEncountersErrors(t *testing.T) {
	engine := newMallEngine(t)
	if _, err := engine.ProveEncounters("甲", "确诊者乙", 10); err == nil {
		t.Error("expected error for unknown entity")
	}
	if _, err := engine.ProveEncounters("确诊者甲", "确诊者乙", 0); err == nil {
		t.Error("expected error for non-positive threshold")
	}
}
