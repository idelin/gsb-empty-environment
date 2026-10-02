package spacetime

import (
	"crypto/ed25519"
	"math"
	"math/rand"
	"testing"
	"time"
)

// --- 疾控流调验收场景:两名确诊者在商场相遇 3 次,阈值 10 米 ---

var testBase = time.Date(2026, 9, 30, 14, 0, 0, 0, time.UTC).Unix()

func buildMallScenario() (*Trajectory, *Trajectory) {
	at := func(off int64, x, y, z float64) Sample {
		return Sample{T: testBase + off, P: Vec3{X: x, Y: y, Z: z}}
	}
	a := &Trajectory{EntityID: "CASE-A-0930", Samples: []Sample{
		at(0, 0, 0, 0), at(600, 100, 0, 0), at(1200, 100, 80, 5),
		at(1800, 20, 80, 5), at(2400, 20, 0, 0), at(3600, 150, 0, 0),
		at(4800, 150, 100, 0), at(6000, 60, 100, 0), at(7200, 60, 0, 0),
	}}
	far := Vec3{X: 1000, Y: 1000, Z: 50}
	nearA := func(off int64, d Vec3) Sample {
		return Sample{T: testBase + off, P: a.PosAt(float64(testBase + off)).Add(d)}
	}
	b := &Trajectory{EntityID: "CASE-B-0930", Samples: []Sample{
		{T: testBase + 0, P: far}, {T: testBase + 1240, P: far},
		nearA(1250, Vec3{X: 4}), nearA(1450, Vec3{X: 4}),
		{T: testBase + 1460, P: far}, {T: testBase + 3640, P: far},
		nearA(3650, Vec3{Y: 6}), nearA(3720, Vec3{Y: 6}),
		{T: testBase + 3730, P: far}, {T: testBase + 6090, P: far},
		nearA(6100, Vec3{X: 3, Y: 3}), nearA(6600, Vec3{X: 3, Y: 3}),
		{T: testBase + 6610, P: far}, {T: testBase + 7200, P: far},
	}}
	return a, b
}

func mallPlaces() *PlaceRegistry {
	return &PlaceRegistry{
		Fallback: "万象城·公共区域",
		Zones: []Zone{
			{Name: "万象城 2F 化妆品区·兰蔻专柜", Min: Vec3{X: 0, Y: 60, Z: 3}, Max: Vec3{X: 120, Y: 100, Z: 8}},
			{Name: "万象城 1F 东侧走廊·扶梯口", Min: Vec3{X: 130, Y: -10, Z: -2}, Max: Vec3{X: 170, Y: 110, Z: 2}},
			{Name: "万象城 1F 西区·星巴克咖啡吧台", Min: Vec3{X: 40, Y: 0, Z: -2}, Max: Vec3{X: 80, Y: 110, Z: 2}},
		},
	}
}

func mallProver() (*Prover, []int) {
	a, b := buildMallScenario()
	idx := NewGridIndex(60, 25)
	ids := idx.Build(a, b)
	seed := make([]byte, ed25519.SeedSize)
	copy(seed, []byte("CDC-epidemiological-investigation"))
	return NewProver(idx, mallPlaces(), seed), ids
}

// 验收:精准输出 3 个相距 <10 米的时间段与具体柜台位置。
func TestMallThreeContactIntervals(t *testing.T) {
	prover, ids := mallProver()
	bundle := prover.Prove(ids[0], ids[1], 10)

	if len(bundle.Certs) != 3 {
		t.Fatalf("期望 3 段相交, 实际 %d 段", len(bundle.Certs))
	}
	want := []struct {
		startIn, endIn [2]float64 // 起止时刻允许区间(相对 testBase 秒)
		place          string
		minDist        float64
	}{
		{[2]float64{1240, 1250}, [2]float64{1450, 1460}, "万象城 2F 化妆品区·兰蔻专柜", 4},
		{[2]float64{3640, 3650}, [2]float64{3720, 3730}, "万象城 1F 东侧走廊·扶梯口", 6},
		{[2]float64{6090, 6100}, [2]float64{6600, 6610}, "万象城 1F 西区·星巴克咖啡吧台", math.Sqrt(18)},
	}
	for i, w := range want {
		c := bundle.Certs[i]
		if c.Start-testBase2() < w.startIn[0] || c.Start-testBase2() > w.startIn[1] {
			t.Errorf("证书#%d 开始时刻 %.2f 不在 %v", i, c.Start-testBase2(), w.startIn)
		}
		if c.End-testBase2() < w.endIn[0] || c.End-testBase2() > w.endIn[1] {
			t.Errorf("证书#%d 结束时刻 %.2f 不在 %v", i, c.End-testBase2(), w.endIn)
		}
		if c.Place != w.place {
			t.Errorf("证书#%d 地点 = %q, 期望 %q", i, c.Place, w.place)
		}
		if math.Abs(c.MinDistM-w.minDist) > 1e-6 {
			t.Errorf("证书#%d 最近距离 = %.6f, 期望 %.6f", i, c.MinDistM, w.minDist)
		}
	}
}

func testBase2() float64 { return float64(testBase) }

// 不可抵赖:证明包验证通过;篡改任一字段即验证失败。
func TestProofNonRepudiation(t *testing.T) {
	prover, ids := mallProver()
	bundle := prover.Prove(ids[0], ids[1], 10)
	if !bundle.Verify() {
		t.Fatal("原始证明包应通过验证")
	}

	tamper := func(mutate func(*ProofBundle)) *ProofBundle {
		// 深拷贝后篡改
		cp := &ProofBundle{
			EntityA: bundle.EntityA, EntityB: bundle.EntityB,
			ThresholdM: bundle.ThresholdM, PubKey: bundle.PubKey,
			BundleHash: bundle.BundleHash,
		}
		for _, c := range bundle.Certs {
			cc := *c
			cp.Certs = append(cp.Certs, &cc)
		}
		mutate(cp)
		return cp
	}
	cases := map[string]func(*ProofBundle){
		"篡改地点":   func(b *ProofBundle) { b.Certs[0].Place = "伪造地点" },
		"篡改开始时刻": func(b *ProofBundle) { b.Certs[1].Start += 5 },
		"篡改最近距离": func(b *ProofBundle) { b.Certs[2].MinDistM = 0.1 },
		"调换证书顺序": func(b *ProofBundle) { b.Certs[0], b.Certs[1] = b.Certs[1], b.Certs[0] },
		"删除末尾证书": func(b *ProofBundle) { b.Certs = b.Certs[:2] },
		"伪造签名":   func(b *ProofBundle) { b.Certs[0].Signature = b.Certs[1].Signature },
		"替换公钥":   func(b *ProofBundle) { b.PubKey = NewProver(nil, nil, nil).PubKeyHex() },
		"篡改轨迹哈希": func(b *ProofBundle) { b.Certs[0].TrajHashA = b.Certs[0].TrajHashB },
		"篡改捆扎哈希": func(b *ProofBundle) { b.BundleHash = b.Certs[0].Hash },
	}
	for name, fn := range cases {
		if tamper(fn).Verify() {
			t.Errorf("篡改场景 %q 未被检出", name)
		}
	}
}

// 索引查询与暴力全量求交结果必须完全一致。
func TestIndexMatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	mkTraj := func(id string, n int, follow *Trajectory, radius float64) *Trajectory {
		tr := &Trajectory{EntityID: id}
		pos := Vec3{rng.Float64() * 100, rng.Float64() * 100, rng.Float64() * 10}
		for i := 0; i < n; i++ {
			if follow != nil && i%3 != 0 {
				// 2/3 的时间贴着目标走(距离 radius/2),1/3 远走
				pos = follow.Samples[i].P.Add(Vec3{X: radius / 2})
			} else if follow == nil {
				pos = pos.Add(Vec3{X: rng.Float64()*20 - 10, Y: rng.Float64()*20 - 10, Z: rng.Float64()*4 - 2})
			} else {
				pos = Vec3{X: 500, Y: 500, Z: 50}
			}
			tr.Samples = append(tr.Samples, Sample{T: testBase + int64(i)*30, P: pos})
		}
		return tr
	}
	const radius = 15.0
	a := mkTraj("RAND-A", 120, nil, radius)
	b := mkTraj("RAND-B", 120, a, radius)
	idx := NewGridIndex(60, 25)
	ids := idx.Build(a, b)

	got := idx.Contacts(ids[0], ids[1], radius)
	want := idx.ContactsBruteForce(ids[0], ids[1], radius)
	if len(got) != len(want) {
		t.Fatalf("索引 %d 段 vs 暴力 %d 段", len(got), len(want))
	}
	if len(got) == 0 {
		t.Fatal("测试数据应至少产生一段相交")
	}
	for i := range got {
		if math.Abs(got[i].Start-want[i].Start) > 1e-6 ||
			math.Abs(got[i].End-want[i].End) > 1e-6 ||
			math.Abs(got[i].MinDist-want[i].MinDist) > 1e-9 {
			t.Errorf("时段#%d 不一致: 索引 %+v vs 暴力 %+v", i, got[i], want[i])
		}
	}
}

// 性能:64 名实体 × 721 个采样点(2 小时, 10 秒粒度),单次求交须为毫秒级。
func TestQueryPerformance(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	const entities, samples = 64, 721
	trajs := make([]*Trajectory, entities)
	for e := 0; e < entities; e++ {
		tr := &Trajectory{EntityID: "E" + string(rune('A'+e))}
		pos := Vec3{rng.Float64() * 500, rng.Float64() * 500, rng.Float64() * 18}
		for i := 0; i < samples; i++ {
			pos = pos.Add(Vec3{X: rng.Float64()*6 - 3, Y: rng.Float64()*6 - 3})
			tr.Samples = append(tr.Samples, Sample{T: testBase + int64(i)*10, P: pos})
		}
		trajs[e] = tr
	}
	// 让 E1 在第 200-400 个采样点贴身跟随 E0(相距 5 米),制造真实相交
	for i := 200; i <= 400; i++ {
		trajs[1].Samples[i].P = trajs[0].Samples[i].P.Add(Vec3{X: 5})
	}
	idx := NewGridIndex(60, 25)
	ids := idx.Build(trajs...)
	prover := NewProver(idx, mallPlaces(), nil)

	start := time.Now()
	bundle := prover.Prove(ids[0], ids[1], 10)
	elapsed := time.Since(start)
	t.Logf("64 实体 × %d 采样点, 求交+签发 %d 份证书耗时 %v", samples, len(bundle.Certs), elapsed)
	if len(bundle.Certs) == 0 {
		t.Fatal("应检出跟随产生的相交时段")
	}
	if elapsed > 100*time.Millisecond {
		t.Fatalf("求交耗时 %v 超出毫秒级要求", elapsed)
	}
	if !bundle.Verify() {
		t.Fatal("证明包验证失败")
	}
}

// 线性插值与钳制。
func TestPosAt(t *testing.T) {
	tr := &Trajectory{EntityID: "T", Samples: []Sample{
		{T: 100, P: Vec3{X: 0, Y: 0, Z: 0}},
		{T: 200, P: Vec3{X: 10, Y: 20, Z: 5}},
	}}
	if p := tr.PosAt(150); p != (Vec3{X: 5, Y: 10, Z: 2.5}) {
		t.Errorf("中点插值错误: %+v", p)
	}
	if p := tr.PosAt(50); p != (Vec3{}) {
		t.Errorf("下界钳制错误: %+v", p)
	}
	if p := tr.PosAt(250); p != (Vec3{X: 10, Y: 20, Z: 5}) {
		t.Errorf("上界钳制错误: %+v", p)
	}
}

// 轨迹哈希:内容相同则哈希相同,任一采样点变化则哈希不同。
func TestTrajectoryHash(t *testing.T) {
	a, b := buildMallScenario()
	a2 := &Trajectory{EntityID: a.EntityID, Samples: append([]Sample(nil), a.Samples...)}
	if a.Hash() != a2.Hash() {
		t.Fatal("相同内容哈希应一致")
	}
	a2.Samples[3].P.X += 0.5
	if a.Hash() == a2.Hash() {
		t.Fatal("篡改采样点后哈希必须变化")
	}
	if a.Hash() == b.Hash() {
		t.Fatal("不同轨迹哈希不应相同")
	}
}
