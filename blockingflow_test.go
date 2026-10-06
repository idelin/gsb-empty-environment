package blockingflow

import (
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

func sortPauses(ps []PauseRecord) {
	sort.Slice(ps, func(i, j int) bool {
		if ps[i].JobID != ps[j].JobID {
			return ps[i].JobID < ps[j].JobID
		}
		if ps[i].Machine != ps[j].Machine {
			return ps[i].Machine < ps[j].Machine
		}
		return ps[i].Start < ps[j].Start
	})
}

// assertSameSchedule 比较两个引擎给出的方案是否一致（忽略证据顺序）。
func assertSameSchedule(t *testing.T, a, b Schedule) {
	t.Helper()
	if a.Makespan != b.Makespan {
		t.Fatalf("makespan mismatch: recurrence=%d des=%d", a.Makespan, b.Makespan)
	}
	blocksA := append([]BlockRecord(nil), a.Blocks...)
	blocksB := append([]BlockRecord(nil), b.Blocks...)
	sort.Slice(blocksA, func(i, j int) bool { return blocksA[i].JobID < blocksA[j].JobID })
	sort.Slice(blocksB, func(i, j int) bool { return blocksB[i].JobID < blocksB[j].JobID })
	if !reflect.DeepEqual(blocksA, blocksB) {
		t.Fatalf("blocks mismatch: recurrence=%v des=%v", blocksA, blocksB)
	}
	pausesA := append([]PauseRecord(nil), a.Pauses...)
	pausesB := append([]PauseRecord(nil), b.Pauses...)
	sortPauses(pausesA)
	sortPauses(pausesB)
	if !reflect.DeepEqual(pausesA, pausesB) {
		t.Fatalf("pauses mismatch: recurrence=%v des=%v", pausesA, pausesB)
	}
}

func TestHandComputedNoDowntime(t *testing.T) {
	jobs := []Job{{ID: 1, P1: 2, P2: 3}, {ID: 2, P1: 4, P2: 1}, {ID: 3, P1: 1, P2: 2}}
	// 顺序 [2,1,3]：工件 3 在机器一上 7 时刻完工，机器二要到 9 才释放。
	sched, err := Simulate(jobs, []int{2, 1, 3}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if sched.Makespan != 11 {
		t.Fatalf("makespan = %d, want 11", sched.Makespan)
	}
	wantBlocks := []BlockRecord{{JobID: 3, Start: 7, End: 9}}
	if !reflect.DeepEqual(sched.Blocks, wantBlocks) {
		t.Fatalf("blocks = %v, want %v", sched.Blocks, wantBlocks)
	}
	if len(sched.Pauses) != 0 {
		t.Fatalf("pauses = %v, want none", sched.Pauses)
	}
	des, err := SimulateEvents(jobs, []int{2, 1, 3}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertSameSchedule(t, sched, des)
}

func TestDowntimePauseOnMachine1(t *testing.T) {
	jobs := []Job{{ID: 1, P1: 4, P2: 1}, {ID: 2, P1: 2, P2: 3}}
	// 机器一在 [3,5) 停机：工件 1 加工 3 个单位后暂停，5 时刻恢复，6 时刻完工。
	sched, err := Simulate(jobs, []int{1, 2}, []Interval{{Start: 3, End: 5}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if sched.Makespan != 11 {
		t.Fatalf("makespan = %d, want 11", sched.Makespan)
	}
	wantPauses := []PauseRecord{{JobID: 1, Machine: 1, Start: 3, End: 5}}
	if !reflect.DeepEqual(sched.Pauses, wantPauses) {
		t.Fatalf("pauses = %v, want %v", sched.Pauses, wantPauses)
	}
	if len(sched.Blocks) != 0 {
		t.Fatalf("blocks = %v, want none", sched.Blocks)
	}
	des, err := SimulateEvents(jobs, []int{1, 2}, []Interval{{Start: 3, End: 5}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertSameSchedule(t, sched, des)
}

func TestBlockingWithDowntimeOnMachine2(t *testing.T) {
	jobs := []Job{{ID: 1, P1: 2, P2: 10}, {ID: 2, P1: 3, P2: 1}}
	// 机器二在 [6,8) 停机：工件 1 在机器二上暂停；工件 2 在机器一上
	// 5 时刻完工后被阻塞到 14。
	down2 := []Interval{{Start: 6, End: 8}}
	sched, err := Simulate(jobs, []int{1, 2}, nil, down2)
	if err != nil {
		t.Fatal(err)
	}
	if sched.Makespan != 15 {
		t.Fatalf("makespan = %d, want 15", sched.Makespan)
	}
	wantBlocks := []BlockRecord{{JobID: 2, Start: 5, End: 14}}
	if !reflect.DeepEqual(sched.Blocks, wantBlocks) {
		t.Fatalf("blocks = %v, want %v", sched.Blocks, wantBlocks)
	}
	wantPauses := []PauseRecord{{JobID: 1, Machine: 2, Start: 6, End: 8}}
	if !reflect.DeepEqual(sched.Pauses, wantPauses) {
		t.Fatalf("pauses = %v, want %v", sched.Pauses, wantPauses)
	}
	des, err := SimulateEvents(jobs, []int{1, 2}, nil, down2)
	if err != nil {
		t.Fatal(err)
	}
	assertSameSchedule(t, sched, des)
}

func TestZeroProcessingTimes(t *testing.T) {
	jobs := []Job{{ID: 1, P1: 0, P2: 2}, {ID: 2, P1: 1, P2: 0}}
	sched, err := Simulate(jobs, []int{1, 2}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if sched.Makespan != 2 {
		t.Fatalf("makespan = %d, want 2", sched.Makespan)
	}
	wantBlocks := []BlockRecord{{JobID: 2, Start: 1, End: 2}}
	if !reflect.DeepEqual(sched.Blocks, wantBlocks) {
		t.Fatalf("blocks = %v, want %v", sched.Blocks, wantBlocks)
	}
	des, err := SimulateEvents(jobs, []int{1, 2}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertSameSchedule(t, sched, des)

	// 零加工时间的工件落在停机的机器二上：不需要可用机时，立即通过。
	one := []Job{{ID: 1, P1: 1, P2: 0}}
	sched, err = Simulate(one, []int{1}, nil, []Interval{{Start: 0, End: 3}})
	if err != nil {
		t.Fatal(err)
	}
	if sched.Makespan != 1 {
		t.Fatalf("makespan = %d, want 1", sched.Makespan)
	}
	des, err = SimulateEvents(one, []int{1}, nil, []Interval{{Start: 0, End: 3}})
	if err != nil {
		t.Fatal(err)
	}
	assertSameSchedule(t, sched, des)
}

func TestOptimizeTieBreakLexicographic(t *testing.T) {
	// 三件完全相同的工件：所有排列 makespan 相同，应选 id 字典序最小者。
	jobs := []Job{{ID: 3, P1: 1, P2: 1}, {ID: 1, P1: 1, P2: 1}, {ID: 2, P1: 1, P2: 1}}
	sched, err := Optimize(jobs)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sched.Seq, []int{1, 2, 3}) {
		t.Fatalf("seq = %v, want [1 2 3]", sched.Seq)
	}
	if sched.Makespan != 4 {
		t.Fatalf("makespan = %d, want 4", sched.Makespan)
	}
}

func TestReplanKeepsPrefix(t *testing.T) {
	jobs := []Job{{ID: 1, P1: 1, P2: 5}, {ID: 2, P1: 5, P2: 1}}
	opt, err := Optimize(jobs)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(opt.Seq, []int{1, 2}) || opt.Makespan != 7 {
		t.Fatalf("optimize = %v (mk %d), want [1 2] (mk 7)", opt.Seq, opt.Makespan)
	}
	// 工件 2 已开工且不可重排：只能接受 [2,1]。
	plan, err := Replan(jobs, []int{2}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plan.Seq, []int{2, 1}) || plan.Makespan != 11 {
		t.Fatalf("replan = %v (mk %d), want [2 1] (mk 11)", plan.Seq, plan.Makespan)
	}
	vfy, err := Verify(jobs, []int{2}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertSameSchedule(t, plan, vfy)
}

// permutations 枚举 ids 的全部排列。
func permutations(ids []int) [][]int {
	var out [][]int
	perm := append([]int(nil), ids...)
	sort.Ints(perm)
	for {
		out = append(out, append([]int(nil), perm...))
		i := len(perm) - 2
		for i >= 0 && perm[i] >= perm[i+1] {
			i--
		}
		if i < 0 {
			break
		}
		j := len(perm) - 1
		for perm[i] >= perm[j] {
			j--
		}
		perm[i], perm[j] = perm[j], perm[i]
		for lo, hi := i+1, len(perm)-1; lo < hi; lo, hi = lo+1, hi-1 {
			perm[lo], perm[hi] = perm[hi], perm[lo]
		}
	}
	return out
}

// TestExhaustiveTwoEngines 对每个排列核对递推式与离散事件模拟完全一致，
// 并核对 Optimize/Verify 给出的全排列最优值一致。
func TestExhaustiveTwoEngines(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 20; trial++ {
		n := 2 + rng.Intn(4) // 2..5 件工件
		jobs := make([]Job, n)
		ids := rand.Perm(60)[:n]
		for i := range jobs {
			jobs[i] = Job{ID: ids[i] + 1, P1: rng.Intn(8), P2: rng.Intn(8)}
		}
		var down1, down2 []Interval
		if trial%2 == 1 {
			down1 = randomDowntimes(rng)
			down2 = randomDowntimes(rng)
		}
		sortedIDs := make([]int, n)
		for i, j := range jobs {
			sortedIDs[i] = j.ID
		}
		for _, seq := range permutations(sortedIDs) {
			rec, err := Simulate(jobs, seq, down1, down2)
			if err != nil {
				t.Fatal(err)
			}
			des, err := SimulateEvents(jobs, seq, down1, down2)
			if err != nil {
				t.Fatal(err)
			}
			assertSameSchedule(t, rec, des)
		}
		opt, err := Replan(jobs, nil, down1, down2)
		if err != nil {
			t.Fatal(err)
		}
		vfy, err := Verify(jobs, nil, down1, down2)
		if err != nil {
			t.Fatal(err)
		}
		assertSameSchedule(t, opt, vfy)
		if !reflect.DeepEqual(opt.Seq, vfy.Seq) {
			t.Fatalf("trial %d: optimal seq mismatch: recurrence=%v des=%v", trial, opt.Seq, vfy.Seq)
		}
	}
}

func randomDowntimes(rng *rand.Rand) []Interval {
	var out []Interval
	t := rng.Intn(4)
	for k := 0; k < 3; k++ {
		length := 1 + rng.Intn(3)
		out = append(out, Interval{Start: t, End: t + length})
		t += length + rng.Intn(5)
	}
	return out
}

// TestReplanWithPrefixAndDowntime 在带停机与已开工前缀的情形下，
// 核对 Replan（递推）与 Verify（事件模拟全排列）一致，且前缀保持不变。
func TestReplanWithPrefixAndDowntime(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 10; trial++ {
		n := 6
		jobs := make([]Job, n)
		ids := rand.Perm(90)[:n]
		for i := range jobs {
			jobs[i] = Job{ID: ids[i] + 1, P1: 1 + rng.Intn(8), P2: 1 + rng.Intn(8)}
		}
		down1 := randomDowntimes(rng)
		down2 := randomDowntimes(rng)
		prefix := []int{jobs[rng.Intn(n)].ID, jobs[rng.Intn(n)].ID}
		if prefix[0] == prefix[1] {
			prefix = prefix[:1]
		}
		plan, err := Replan(jobs, prefix, down1, down2)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(plan.Seq[:len(prefix)], prefix) {
			t.Fatalf("trial %d: prefix not preserved: seq=%v prefix=%v", trial, plan.Seq, prefix)
		}
		vfy, err := Verify(jobs, prefix, down1, down2)
		if err != nil {
			t.Fatal(err)
		}
		assertSameSchedule(t, plan, vfy)
		if !reflect.DeepEqual(plan.Seq, vfy.Seq) {
			t.Fatalf("trial %d: seq mismatch: %v vs %v", trial, plan.Seq, vfy.Seq)
		}
	}
}

// TestOptimizeTenJobs 上限规模：10 件工件全排列（362 万）求最优，
// 并用事件模拟核对最优顺序的评估结果。
func TestOptimizeTenJobs(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	jobs := make([]Job, 10)
	ids := rand.Perm(200)[:10]
	for i := range jobs {
		jobs[i] = Job{ID: ids[i] + 1, P1: 1 + rng.Intn(20), P2: 1 + rng.Intn(20)}
	}
	opt, err := Optimize(jobs)
	if err != nil {
		t.Fatal(err)
	}
	if len(opt.Seq) != 10 {
		t.Fatalf("seq length = %d, want 10", len(opt.Seq))
	}
	des, err := SimulateEvents(jobs, opt.Seq, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertSameSchedule(t, opt, des)
	// 抽样核对：随机排列的 makespan 不小于最优值。
	check := rand.New(rand.NewSource(2))
	for k := 0; k < 200; k++ {
		perm := check.Perm(10)
		seq := make([]int, 10)
		for i, p := range perm {
			seq[i] = jobs[p].ID
		}
		s, err := Simulate(jobs, seq, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if s.Makespan < opt.Makespan {
			t.Fatalf("found better seq %v (mk %d) than optimum %v (mk %d)", seq, s.Makespan, opt.Seq, opt.Makespan)
		}
	}
}

// TestDowntimeEndCoincidesWithLoad 回归测试：工件恰在停机结束时刻装上
// 机器，机器应视为已恢复，不应产生零长度暂停记录。
func TestDowntimeEndCoincidesWithLoad(t *testing.T) {
	jobs := []Job{{ID: 1, P1: 9, P2: 2}}
	down2 := []Interval{{Start: 0, End: 9}}
	sched, err := Simulate(jobs, []int{1}, nil, down2)
	if err != nil {
		t.Fatal(err)
	}
	if sched.Makespan != 11 {
		t.Fatalf("makespan = %d, want 11", sched.Makespan)
	}
	if len(sched.Pauses) != 0 {
		t.Fatalf("pauses = %v, want none", sched.Pauses)
	}
	des, err := SimulateEvents(jobs, []int{1}, nil, down2)
	if err != nil {
		t.Fatal(err)
	}
	assertSameSchedule(t, sched, des)
}

func TestValidationErrors(t *testing.T) {
	jobs := []Job{{ID: 1, P1: 1, P2: 1}, {ID: 2, P1: 2, P2: 2}}
	if _, err := Optimize(nil); err == nil {
		t.Fatal("want error for empty jobs")
	}
	if _, err := Optimize([]Job{{ID: 1, P1: 1, P2: 1}, {ID: 1, P1: 2, P2: 2}}); err == nil {
		t.Fatal("want error for duplicate ids")
	}
	if _, err := Optimize([]Job{{ID: 1, P1: -1, P2: 1}}); err == nil {
		t.Fatal("want error for negative processing time")
	}
	if _, err := Optimize(make([]Job, MaxJobs+1)); err == nil {
		t.Fatal("want error for too many jobs")
	}
	if _, err := Replan(jobs, []int{9}, nil, nil); err == nil {
		t.Fatal("want error for unknown prefix id")
	}
	if _, err := Replan(jobs, []int{1, 1}, nil, nil); err == nil {
		t.Fatal("want error for duplicate prefix id")
	}
	if _, err := Replan(jobs, nil, []Interval{{Start: 5, End: 5}}, nil); err == nil {
		t.Fatal("want error for empty downtime interval")
	}
	if _, err := Replan(jobs, nil, []Interval{{Start: -1, End: 2}}, nil); err == nil {
		t.Fatal("want error for negative downtime start")
	}
	if _, err := Simulate(jobs, []int{1, 1}, nil, nil); err == nil {
		t.Fatal("want error for duplicate seq id")
	}
	if _, err := Simulate(jobs, []int{1}, nil, nil); err == nil {
		t.Fatal("want error for short seq")
	}
}
