package trainsim

import "testing"

func checkChanged(t *testing.T, got []TrainID, want ...TrainID) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("受改列车应为 %v，得到 %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("受改列车应为 %v，得到 %v", want, got)
		}
	}
}

// 延误后优先最少受改列车数：仅延误车改签到 30 到达（延误 14），
// 优于"两车都改、总延误 8"的方案。
func TestReplanMinChangedTrains(t *testing.T) {
	net := lineNetwork()
	tab := NewTable()
	sch := &Scheduler{Net: net, Table: tab, Horizon: 40}
	rx := mustPlan(t, sch, "X", eastPath, 0)
	ry := mustPlan(t, sch, "Y", westPath, 0)

	req := ReplanRequest{
		Now: 0, Horizon: 40,
		Plans:   []TrainPlan{asPlan("X", eastPath, rx), asPlan("Y", westPath, ry)},
		Delayed: "X", ReadyAt: 6,
	}
	res, err := Replan(net, req)
	if err != nil {
		t.Fatal(err)
	}
	checkChanged(t, res.Changed, "X")
	if res.TotalDelay != 14 {
		t.Fatalf("总延误应为 14，得到 %d", res.TotalDelay)
	}
	if got := res.Plans[0].Arrival(); got != 30 {
		t.Fatalf("X 应于 30 到达，得到 %d", got)
	}
	if !equalReservations(res.Plans[1].Res, ry) {
		t.Fatal("Y 的方案不应被改动")
	}
	if err := Replay(net, 40, res.Plans); err != nil {
		t.Fatalf("回放核对失败: %v", err)
	}
}

// 已承诺前缀不可移动 + 连锁冲突：Y 在车站被扣至 8，
// 其后缀须绕开 X 的进行中占用与 Z 的已确认预约。
func TestReplanLockedPrefixAndCascade(t *testing.T) {
	net := lineNetwork()
	tab := NewTable()
	sch := &Scheduler{Net: net, Table: tab, Horizon: 40}
	rx := mustPlan(t, sch, "X", eastPath, 0)
	ry := mustPlan(t, sch, "Y", westPath, 0)
	rz := mustPlan(t, sch, "Z", eastPath, 0)

	req := ReplanRequest{
		Now: 3, Horizon: 40,
		Plans: []TrainPlan{
			asPlan("X", eastPath, rx),
			asPlan("Y", westPath, ry),
			asPlan("Z", eastPath, rz),
		},
		Delayed: "Y", ReadyAt: 8,
	}
	res, err := Replan(net, req)
	if err != nil {
		t.Fatal(err)
	}
	checkChanged(t, res.Changed, "Y")
	if res.TotalDelay != 10 {
		t.Fatalf("总延误应为 10，得到 %d", res.TotalDelay)
	}
	// 未受改列车的方案（含 X 已进入的 M1 [2,7)）完全不动。
	if !equalReservations(res.Plans[0].Res, rx) {
		t.Fatal("X 的方案（含已承诺前缀）不应被改动")
	}
	if !equalReservations(res.Plans[2].Res, rz) {
		t.Fatal("Z 的方案不应被改动")
	}
	// Y 已进入的 B 段占用起点不变，仅延长停留（车站允许等待）。
	yb := res.Plans[1].Res[0]
	checkIv(t, yb, "B", 0, 14)
	if got := res.Plans[1].Arrival(); got != 28 {
		t.Fatalf("Y 应于 28 到达，得到 %d", got)
	}
	if err := Replay(net, 40, res.Plans); err != nil {
		t.Fatalf("回放核对失败: %v", err)
	}
}

// 受改列车数相同再比总延误：X 单独改签在时域内不可行，
// 必须同时调整 Y；两种放置次序中应选出总延误最小者（3）。
func TestReplanMinTotalDelay(t *testing.T) {
	net := lineNetwork()
	const horizon = 36
	mkPlan := func(train TrainID, path []SegmentID, ivs ...Interval) TrainPlan {
		rs := make([]Reservation, len(path))
		for i, seg := range path {
			rs[i] = Reservation{Train: train, Seg: seg, Iv: ivs[i]}
		}
		return TrainPlan{Train: train, Path: path, Res: rs}
	}
	xPlan := mkPlan("X", eastPath,
		Interval{0, 2}, Interval{2, 7}, Interval{7, 9}, Interval{9, 14}, Interval{14, 16})
	// Y 在 L 有超长停站计划，把 X 单独改签的路堵死。
	yPlan := mkPlan("Y", westPath,
		Interval{0, 4}, Interval{4, 9}, Interval{9, 29}, Interval{29, 34}, Interval{34, 36})
	plans := []TrainPlan{xPlan, yPlan}
	if err := Replay(net, horizon, plans); err != nil {
		t.Fatalf("原方案应合法: %v", err)
	}

	req := ReplanRequest{Now: 0, Horizon: horizon, Plans: plans, Delayed: "X", ReadyAt: 3}
	res, err := Replan(net, req)
	if err != nil {
		t.Fatal(err)
	}
	checkChanged(t, res.Changed, "X", "Y")
	if res.TotalDelay != 3 {
		t.Fatalf("总延误应为 3，得到 %d", res.TotalDelay)
	}
	if got := res.Plans[0].Arrival(); got != 19 {
		t.Fatalf("X 应于 19 到达，得到 %d", got)
	}
	if err := Replay(net, horizon, res.Plans); err != nil {
		t.Fatalf("回放核对失败: %v", err)
	}
}

// 暴力枚举交叉核对：在小线路上，Replan 的目标值须与
// 枚举全部合法后缀方案组合得到的字典序最优一致。
func TestReplanMatchesBruteForce(t *testing.T) {
	net := NewNetwork([]Segment{
		{ID: "P", Travel: 1, Station: true},
		{ID: "Q", Travel: 2},
		{ID: "R", Travel: 1, Station: true},
	})
	east := []SegmentID{"P", "Q", "R"}
	west := []SegmentID{"R", "Q", "P"}
	const horizon = 12
	tab := NewTable()
	sch := &Scheduler{Net: net, Table: tab, Horizon: horizon}
	ru := mustPlan(t, sch, "U", east, 0)
	rv := mustPlan(t, sch, "V", west, 0)

	req := ReplanRequest{
		Now: 0, Horizon: horizon,
		Plans:   []TrainPlan{asPlan("U", east, ru), asPlan("V", west, rv)},
		Delayed: "U", ReadyAt: 2,
	}
	res, err := Replan(net, req)
	if err != nil {
		t.Fatal(err)
	}
	checkChanged(t, res.Changed, "U")
	if res.TotalDelay != 4 {
		t.Fatalf("总延误应为 4，得到 %d", res.TotalDelay)
	}
	bc, bd, ok := BruteForceReplan(net, req, 100000)
	if !ok {
		t.Fatal("暴力枚举应找到可行解")
	}
	if bc != len(res.Changed) || bd != res.TotalDelay {
		t.Fatalf("与暴力枚举不一致: Replan=(%d,%d) 暴力=(%d,%d)",
			len(res.Changed), res.TotalDelay, bc, bd)
	}
	if err := Replay(net, horizon, res.Plans); err != nil {
		t.Fatalf("回放核对失败: %v", err)
	}
}

// 多起点组合 + 延误场景下的批量交叉核对。
func TestReplanBruteForceAcrossStarts(t *testing.T) {
	net := NewNetwork([]Segment{
		{ID: "P", Travel: 1, Station: true},
		{ID: "Q", Travel: 2},
		{ID: "R", Travel: 1, Station: true},
	})
	east := []SegmentID{"P", "Q", "R"}
	west := []SegmentID{"R", "Q", "P"}
	const horizon = 12
	for su := Time(0); su <= 2; su++ {
		for sv := Time(0); sv <= 2; sv++ {
			tab := NewTable()
			sch := &Scheduler{Net: net, Table: tab, Horizon: horizon}
			ru, err := sch.PlanPath("U", east, su)
			if err != nil {
				t.Fatalf("(%d,%d): U 应可行: %v", su, sv, err)
			}
			rv, err := sch.PlanPath("V", west, sv)
			if err != nil {
				t.Fatalf("(%d,%d): V 应可行: %v", su, sv, err)
			}
			req := ReplanRequest{
				Now: 0, Horizon: horizon,
				Plans:   []TrainPlan{asPlan("U", east, ru), asPlan("V", west, rv)},
				Delayed: "U", ReadyAt: su + 2,
			}
			res, err := Replan(net, req)
			if err != nil {
				t.Fatalf("(%d,%d): %v", su, sv, err)
			}
			bc, bd, ok := BruteForceReplan(net, req, 100000)
			if !ok {
				t.Fatalf("(%d,%d): 暴力枚举应找到可行解", su, sv)
			}
			if bc != len(res.Changed) || bd != res.TotalDelay {
				t.Fatalf("(%d,%d) 不一致: Replan=(%d,%d) 暴力=(%d,%d)",
					su, sv, len(res.Changed), res.TotalDelay, bc, bd)
			}
			if err := Replay(net, horizon, res.Plans); err != nil {
				t.Fatalf("(%d,%d) 回放核对失败: %v", su, sv, err)
			}
		}
	}
}

// 超过四车不予受理。
func TestReplanTooManyTrains(t *testing.T) {
	net := NewNetwork([]Segment{{ID: "S", Travel: 1, Station: true}})
	var plans []TrainPlan
	for _, id := range []TrainID{"a", "b", "c", "d", "e"} {
		plans = append(plans, TrainPlan{
			Train: id, Path: []SegmentID{"S"},
			Res: []Reservation{{Train: id, Seg: "S", Iv: Interval{0, 1}}},
		})
	}
	_, err := Replan(net, ReplanRequest{Now: 0, Horizon: 10, Plans: plans, Delayed: "a", ReadyAt: 1})
	if err == nil {
		t.Fatal("超过四车应返回错误")
	}
}
