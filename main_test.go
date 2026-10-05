package main

import (
	"math"
	"testing"
)

func almostEq(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

// 两任务串行链：验证总耗时范围是精确界而不是简单相减。
func TestSpanRange(t *testing.T) {
	in := &Input{
		Events: []Event{
			{Name: "s1", Lo: 0, Hi: 1},
			{Name: "e1", Lo: 4, Hi: 6},
			{Name: "s2", Lo: 5, Hi: 8},
			{Name: "e2", Lo: 9, Hi: 10},
		},
		Tasks: []Task{
			{Name: "t1", Start: "s1", End: "e1"},
			{Name: "t2", Start: "s2", End: "e2"},
		},
		Deps:      []Dep{{After: "e1", Before: "s2"}},
		SpanBegin: "s1",
		SpanEnd:   "e2",
	}
	sys := buildSystem(in)
	if _, cycle := bellmanFord(sys.n(), sys.edges); cycle != nil {
		t.Fatalf("期望可行，得到负环: %+v", cycle)
	}
	minSpan, maxSpan := spanRange(sys, "s1", "e2")
	// min: e2 最早 9，s1 最晚 1 => 8；max: e2 最晚 10，s1 最早 0 => 10
	if !almostEq(minSpan, 8) {
		t.Errorf("minSpan = %v, 期望 8", minSpan)
	}
	if !almostEq(maxSpan, 10) {
		t.Errorf("maxSpan = %v, 期望 10", maxSpan)
	}
}

// 结束区间整体早于开始区间：必须报矛盾环，而不是给出负时长。
func TestConflictCycleLocated(t *testing.T) {
	in := &Input{
		Events: []Event{
			{Name: "s", Lo: 2, Hi: 3},
			{Name: "e", Lo: 0, Hi: 1},
		},
		Tasks:     []Task{{Name: "bad", Start: "s", End: "e"}},
		SpanBegin: "s",
		SpanEnd:   "e",
	}
	sys := buildSystem(in)
	_, cycle := bellmanFord(sys.n(), sys.edges)
	if cycle == nil {
		t.Fatal("期望检测到矛盾负环")
	}
	sum := 0.0
	for _, e := range cycle {
		sum += e.w
	}
	if sum >= 0 {
		t.Errorf("负环权重和应 < 0，得到 %v", sum)
	}
	if len(cycle) < 2 {
		t.Errorf("负环应包含多条约束以定位，得到 %d 条", len(cycle))
	}
}

// 依赖 + 时钟区间冲突：依赖要求 test.start >= build.end，但区间不允许。
func TestDependencyConflict(t *testing.T) {
	in := &Input{
		Events: []Event{
			{Name: "build.end", Lo: 105, Hi: 106},
			{Name: "test.start", Lo: 98, Hi: 100},
		},
		Deps:      []Dep{{After: "build.end", Before: "test.start"}},
		SpanBegin: "build.end",
		SpanEnd:   "test.start",
	}
	sys := buildSystem(in)
	if _, cycle := bellmanFord(sys.n(), sys.edges); cycle == nil {
		t.Fatal("期望检测到依赖与区间的矛盾")
	}
}

// 系统可行但 span 被强制为负：maxSpan < 0，需走定位分支。
func TestForcedNegativeSpan(t *testing.T) {
	in := &Input{
		Events: []Event{
			{Name: "begin", Lo: 5, Hi: 6},
			{Name: "end", Lo: 0, Hi: 1},
		},
		SpanBegin: "begin",
		SpanEnd:   "end",
	}
	sys := buildSystem(in)
	if _, cycle := bellmanFord(sys.n(), sys.edges); cycle != nil {
		t.Fatal("系统应当可行")
	}
	_, maxSpan := spanRange(sys, "begin", "end")
	if maxSpan >= 0 {
		t.Errorf("期望 maxSpan < 0（强制负耗时），得到 %v", maxSpan)
	}
	if !almostEq(maxSpan, -4) {
		t.Errorf("maxSpan = %v, 期望 -4", maxSpan)
	}
}

// 排队 vs 计算分解：排队应被识别为主导。
func TestQueueDominates(t *testing.T) {
	in := &Input{
		Events: []Event{
			{Name: "req", Lo: 0, Hi: 0},
			{Name: "acq", Lo: 9, Hi: 9},
			{Name: "s", Lo: 9, Hi: 9},
			{Name: "e", Lo: 10, Hi: 10},
		},
		Tasks:     []Task{{Name: "job", Request: "req", Acquire: "acq", Start: "s", End: "e"}},
		SpanBegin: "req",
		SpanEnd:   "e",
	}
	sys := buildSystem(in)
	minSpan, maxSpan := spanRange(sys, "req", "e")
	if !almostEq(minSpan, 10) || !almostEq(maxSpan, 10) {
		t.Fatalf("span = [%v, %v], 期望 [10, 10]", minSpan, maxSpan)
	}
	a := sys.idx["req"]
	b := sys.idx["e"]
	edges := append(append([]cedge{}, sys.edges...), cedge{from: a, to: b, w: minSpan})
	dist, cycle := bellmanFord(sys.n(), edges)
	if cycle != nil {
		t.Fatal("最短界应可达")
	}
	times := map[string]float64{}
	for i, name := range sys.names {
		times[name] = dist[i]
	}
	chain, totals := criticalChain(in, times)
	if chain == nil {
		t.Fatal("应找到关键链")
	}
	if !almostEq(totals[kindWait], 9) {
		t.Errorf("排队 = %v, 期望 9", totals[kindWait])
	}
	if !almostEq(totals[kindCompute], 1) {
		t.Errorf("计算 = %v, 期望 1", totals[kindCompute])
	}
}

// 加载示例文件应成功。
func TestLoadExamples(t *testing.T) {
	for _, f := range []string{"examples/pipeline.json", "examples/conflict.json", "examples/forced_negative_span.json"} {
		if _, err := LoadInput(f); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
}

// witness 时间线必须落在每个事件的时钟区间内（超级源点归零）。
func TestWitnessWithinIntervals(t *testing.T) {
	in, err := LoadInput("examples/pipeline.json")
	if err != nil {
		t.Fatal(err)
	}
	sys := buildSystem(in)
	if _, cycle := bellmanFord(sys.n(), sys.edges); cycle != nil {
		t.Fatal("期望可行")
	}
	minSpan, maxSpan := spanRange(sys, in.SpanBegin, in.SpanEnd)
	a := sys.idx[in.SpanBegin]
	b := sys.idx[in.SpanEnd]
	for _, tc := range []struct {
		name string
		edge cedge
	}{
		{"min", cedge{from: a, to: b, w: minSpan}},
		{"max", cedge{from: b, to: a, w: -maxSpan}},
	} {
		edges := append(append([]cedge{}, sys.edges...), tc.edge)
		dist, cycle := bellmanFord(sys.n(), edges)
		if cycle != nil {
			t.Fatalf("%s: 界不可达", tc.name)
		}
		times := shiftedTimes(sys, dist)
		for _, e := range in.Events {
			x := times[e.Name]
			if x < e.Lo-1e-6 || x > e.Hi+1e-6 {
				t.Errorf("%s: 事件 %s 时刻 %v 不在区间 [%v, %v]", tc.name, e.Name, x, e.Lo, e.Hi)
			}
		}
		span := times[in.SpanEnd] - times[in.SpanBegin]
		want := minSpan
		if tc.name == "max" {
			want = maxSpan
		}
		if !almostEq(span, want) {
			t.Errorf("%s: witness 总耗时 %v, 期望 %v", tc.name, span, want)
		}
	}
}
