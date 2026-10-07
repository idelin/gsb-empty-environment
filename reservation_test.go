package trainsim

import "testing"

// lineNetwork 构造测试线路：A(站) - M1 - L(站) - M2 - B(站)。
func lineNetwork() *Network {
	return NewNetwork([]Segment{
		{ID: "A", Travel: 2, Station: true},
		{ID: "M1", Travel: 5},
		{ID: "L", Travel: 2, Station: true},
		{ID: "M2", Travel: 5},
		{ID: "B", Travel: 2, Station: true},
	})
}

var (
	eastPath = []SegmentID{"A", "M1", "L", "M2", "B"}
	westPath = []SegmentID{"B", "M2", "L", "M1", "A"}
)

func mustPlan(t *testing.T, s *Scheduler, train TrainID, path []SegmentID, earliest Time) []Reservation {
	t.Helper()
	rs, err := s.PlanPath(train, path, earliest)
	if err != nil {
		t.Fatalf("PlanPath(%s) 失败: %v", train, err)
	}
	return rs
}

func asPlan(train TrainID, path []SegmentID, rs []Reservation) TrainPlan {
	return TrainPlan{Train: train, Path: path, Res: rs}
}

func checkIv(t *testing.T, r Reservation, seg SegmentID, start, end Time) {
	t.Helper()
	if r.Seg != seg || r.Iv.Start != start || r.Iv.End != end {
		t.Fatalf("期望 %s [%d,%d)，得到 %s [%d,%d)", seg, start, end, r.Seg, r.Iv.Start, r.Iv.End)
	}
}

// 半开区间：同刻释放后进入不冲突；真重叠必须拒绝且不留部分预约。
func TestHalfOpenSameTimeRelease(t *testing.T) {
	tab := NewTable()
	if err := tab.Reserve(Reservation{Train: "T1", Seg: "S", Iv: Interval{0, 10}}); err != nil {
		t.Fatal(err)
	}
	// [10,20) 与 [0,10) 仅端点相接：释放与进入同刻，允许。
	if err := tab.Reserve(Reservation{Train: "T2", Seg: "S", Iv: Interval{10, 20}}); err != nil {
		t.Fatalf("同刻释放后进入应被允许: %v", err)
	}
	if err := tab.Verify(); err != nil {
		t.Fatal(err)
	}
	// 真重叠（同向也不行）必须拒绝。
	if err := tab.Reserve(Reservation{Train: "T3", Seg: "S", Iv: Interval{9, 20}}); err == nil {
		t.Fatal("重叠预约应被拒绝")
	}
	// 多段申请中一段冲突：全部不生效，不留部分预约。
	err := tab.Reserve(
		Reservation{Train: "T4", Seg: "S", Iv: Interval{19, 30}},
		Reservation{Train: "T4", Seg: "S2", Iv: Interval{0, 5}},
	)
	if err == nil {
		t.Fatal("部分冲突的批量预约应被拒绝")
	}
	if !tab.Free("S2", Interval{0, 5}) {
		t.Fatal("失败后留下了部分预约")
	}
	if got := len(tab.Reservations()); got != 2 {
		t.Fatalf("预约数应为 2，得到 %d", got)
	}
}

// 路径申请须全量预约成功才确认：时域不足时失败且不留部分预约。
func TestAtomicPathFailure(t *testing.T) {
	net := lineNetwork()
	tab := NewTable()
	sch := &Scheduler{Net: net, Table: tab, Horizon: 10}
	if _, err := sch.PlanPath("X", eastPath, 0); err == nil {
		t.Fatal("时域不足应规划失败")
	}
	if got := len(tab.Reservations()); got != 0 {
		t.Fatalf("失败后应无任何预约，得到 %d 条", got)
	}
	// 换成足够时域后 X 成功；再让 Y 在紧张时域下失败，X 的预约不受影响。
	sch.Horizon = 40
	mustPlan(t, sch, "X", eastPath, 0)
	sch2 := &Scheduler{Net: net, Table: tab, Horizon: 12}
	if _, err := sch2.PlanPath("Y", westPath, 0); err == nil {
		t.Fatal("Y 在时域 12 内应失败")
	}
	if got := len(tab.Reservations()); got != len(eastPath) {
		t.Fatalf("Y 失败后应只剩 X 的 %d 条预约，得到 %d", len(eastPath), got)
	}
	if err := tab.Verify(); err != nil {
		t.Fatal(err)
	}
}

// 交会：对向两列车共享单轨中段，后申请者须在车站等待；
// 并用区间扫描核对结果，验证同刻释放后进入。
func TestMeetAndWaitAtStation(t *testing.T) {
	net := lineNetwork()
	tab := NewTable()
	sch := &Scheduler{Net: net, Table: tab, Horizon: 40}

	rx := mustPlan(t, sch, "X", eastPath, 0)
	checkIv(t, rx[0], "A", 0, 2)
	checkIv(t, rx[1], "M1", 2, 7)
	checkIv(t, rx[2], "L", 7, 9)
	checkIv(t, rx[3], "M2", 9, 14)
	checkIv(t, rx[4], "B", 14, 16)

	ry := mustPlan(t, sch, "Y", westPath, 0)
	// Y 在起点站 B 等待至 4，使得进入 M2 后能在 L 前等 X 交会。
	checkIv(t, ry[0], "B", 0, 4)
	checkIv(t, ry[1], "M2", 4, 9)
	checkIv(t, ry[2], "L", 9, 11)
	checkIv(t, ry[3], "M1", 11, 16)
	checkIv(t, ry[4], "A", 16, 18)

	// 同刻释放后进入：X 于 9 释放 L，Y 于 9 进入 L。
	if rx[2].Iv.End != ry[2].Iv.Start {
		t.Fatalf("L 段应同刻交接：X 释放于 %d，Y 进入于 %d", rx[2].Iv.End, ry[2].Iv.Start)
	}
	// 站点允许等待：Y 在 B 的占用被延长（4 > 走行时分 2）。
	if ry[0].Iv.Len() <= 2 {
		t.Fatal("Y 应在车站 B 延长停留以等待交会")
	}
	// 非车站轨段不允许等待：占用时长恰为走行时分。
	for _, i := range []int{1, 3} {
		if ry[i].Iv.Len() != 5 {
			t.Fatalf("非车站轨段 %s 不应有停留", ry[i].Seg)
		}
	}
	if err := tab.Verify(); err != nil {
		t.Fatalf("区间扫描发现冲突: %v", err)
	}
	if err := Replay(net, 40, []TrainPlan{asPlan("X", eastPath, rx), asPlan("Y", westPath, ry)}); err != nil {
		t.Fatalf("回放核对失败: %v", err)
	}
}

// 取消：释放后其他列车可使用同一空间；后缀取消只影响未进入部分。
func TestCancel(t *testing.T) {
	net := lineNetwork()
	tab := NewTable()
	sch := &Scheduler{Net: net, Table: tab, Horizon: 40}
	mustPlan(t, sch, "X", eastPath, 0)

	// 后缀取消：仅保留开始时刻早于 7 的预约（A、M1）。
	tab.CancelSuffix("X", 7)
	if got := len(tab.Reservations()); got != 2 {
		t.Fatalf("后缀取消后应剩 2 条预约，得到 %d", got)
	}
	// 全部取消后，对向列车可立即占用原空间。
	tab.CancelTrain("X")
	if got := len(tab.Reservations()); got != 0 {
		t.Fatalf("全部取消后应无预约，得到 %d", got)
	}
	ry := mustPlan(t, sch, "Y", westPath, 0)
	checkIv(t, ry[0], "B", 0, 2)
	checkIv(t, ry[1], "M2", 2, 7)
	if err := tab.Verify(); err != nil {
		t.Fatal(err)
	}
}

// 所有合法起点组合：枚举两车不同出发时刻，逐一规划并回放核对连锁冲突。
func TestStartCombinationsReplay(t *testing.T) {
	net := NewNetwork([]Segment{
		{ID: "S1", Travel: 1, Station: true},
		{ID: "M", Travel: 3},
		{ID: "S2", Travel: 1, Station: true},
	})
	east := []SegmentID{"S1", "M", "S2"}
	west := []SegmentID{"S2", "M", "S1"}
	const horizon = 20
	feasible := 0
	for p := Time(0); p <= 6; p += 2 {
		for q := Time(0); q <= 6; q += 2 {
			tab := NewTable()
			sch := &Scheduler{Net: net, Table: tab, Horizon: horizon}
			rp, err := sch.PlanPath("P", east, p)
			if err != nil {
				t.Fatalf("组合 (%d,%d): P 应可行: %v", p, q, err)
			}
			rq, err := sch.PlanPath("Q", west, q)
			if err != nil {
				t.Fatalf("组合 (%d,%d): Q 应可行: %v", p, q, err)
			}
			plans := []TrainPlan{asPlan("P", east, rp), asPlan("Q", west, rq)}
			if err := Replay(net, horizon, plans); err != nil {
				t.Fatalf("组合 (%d,%d) 回放发现冲突: %v", p, q, err)
			}
			if err := tab.Verify(); err != nil {
				t.Fatalf("组合 (%d,%d) 区间扫描发现冲突: %v", p, q, err)
			}
			feasible++
			if p == 0 && q == 0 {
				// Q 在 S2 等待至 P 释放 M，并于同刻进入。
				checkIv(t, rq[0], "S2", 0, 4)
				if rq[1].Iv.Start != rp[1].Iv.End {
					t.Fatalf("Q 应在 P 释放 M 的同刻进入：%d != %d",
						rq[1].Iv.Start, rp[1].Iv.End)
				}
			}
		}
	}
	if feasible != 16 {
		t.Fatalf("应枚举 16 个合法起点组合，得到 %d", feasible)
	}
}

// 回放器应能检出人为构造的冲突与违规等待。
func TestReplayDetectsViolations(t *testing.T) {
	net := NewNetwork([]Segment{
		{ID: "S1", Travel: 1, Station: true},
		{ID: "M", Travel: 3},
		{ID: "S2", Travel: 1, Station: true},
	})
	path := []SegmentID{"S1", "M", "S2"}
	mk := func(train TrainID, s1, m1, m2 Time) TrainPlan {
		return TrainPlan{Train: train, Path: path, Res: []Reservation{
			{Train: train, Seg: "S1", Iv: Interval{s1, m1}},
			{Train: train, Seg: "M", Iv: Interval{m1, m2}},
			{Train: train, Seg: "S2", Iv: Interval{m2, m2 + 1}},
		}}
	}
	// 区间重叠。
	if err := Replay(net, 20, []TrainPlan{mk("A", 0, 1, 4), mk("B", 2, 3, 6)}); err == nil {
		t.Fatal("重叠占用应被回放检出")
	}
	// 非车站轨段上等待。
	bad := TrainPlan{Train: "C", Path: path, Res: []Reservation{
		{Train: "C", Seg: "S1", Iv: Interval{0, 1}},
		{Train: "C", Seg: "M", Iv: Interval{1, 6}},
		{Train: "C", Seg: "S2", Iv: Interval{6, 7}},
	}}
	if err := Replay(net, 20, []TrainPlan{bad}); err == nil {
		t.Fatal("非车站轨段上的停留应被回放检出")
	}
	// 占用不衔接。
	gap := TrainPlan{Train: "D", Path: path, Res: []Reservation{
		{Train: "D", Seg: "S1", Iv: Interval{0, 1}},
		{Train: "D", Seg: "M", Iv: Interval{2, 5}},
		{Train: "D", Seg: "S2", Iv: Interval{5, 6}},
	}}
	if err := Replay(net, 20, []TrainPlan{gap}); err == nil {
		t.Fatal("不衔接的占用应被回放检出")
	}
}
