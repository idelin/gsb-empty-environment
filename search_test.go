package netsec

import (
	"reflect"
	"testing"
)

// classicABAScripts 是经典 ABA 场景：T0 弹出栈顶时被抢占，T1 弹出并释放节点，
// T2 复用槽位压入新告警，T0 恢复后裸地址 CAS 误成功。
func classicABAScripts() [][]Op {
	return [][]Op{
		{ // T0: 建栈 [事件2 -> 事件1]，然后开始弹出栈顶
			Alloc("a", 1),
			Alloc("b", 2),
			SetNext("b", "a"),
			Read("z"),
			CAS("z", "a"),
			Read("h"),
			CAS("h", "b"),
			Read("h"),
			Next("n", "h"),
			CAS("h", "n"),
		},
		{ // T1: 弹出栈顶并释放槽位
			Read("h"),
			Next("n", "h"),
			CAS("h", "n"),
			Free("h"),
			Read("h"),
			Next("n", "h"),
			CAS("h", "n"),
			Free("h"),
		},
		{ // T2: 复用槽位，压入新告警
			Alloc("x", 3),
			Alloc("y", 4),
			Read("z"),
			CAS("z", "y"),
		},
	}
}

func TestFindShortestLossClassicABA(t *testing.T) {
	res, err := FindShortestLoss(classicABAScripts())
	if err != nil {
		t.Fatalf("FindShortestLoss: %v", err)
	}
	w := res.Witness
	if w == nil {
		t.Fatal("未找到首次分歧且丢事件的交错")
	}
	if got, want := w.Length(), 18; got != want {
		t.Errorf("交错长度 = %d, 期望 %d", got, want)
	}
	if got, want := w.Lost, []int{4}; !reflect.DeepEqual(got, want) {
		t.Errorf("丢失事件 ID = %v, 期望 %v", got, want)
	}
	last := w.Steps[len(w.Steps)-1]
	if !last.IsCAS || !last.Diverged {
		t.Errorf("末步应为首次分歧的 CAS, 得到 %+v", last)
	}
	if !last.RawOK || last.TaggedOK {
		t.Errorf("分歧方向应为裸地址成功/带代次失败, 得到 raw=%v tagged=%v", last.RawOK, last.TaggedOK)
	}
	t.Logf("\n%s", w.String())
}

// TestEnumerateAllCrossCheck 用完整状态枚举核对：见证长度等于全部
// 首次分歧丢事件交错的最小长度，且不存在反向分歧（带代次成功而裸地址失败）。
func TestEnumerateAllCrossCheck(t *testing.T) {
	scripts := classicABAScripts()
	short, err := FindShortestLoss(scripts)
	if err != nil {
		t.Fatalf("FindShortestLoss: %v", err)
	}
	full, err := EnumerateAll(scripts, DefaultMaxStates)
	if err != nil {
		t.Fatalf("EnumerateAll: %v", err)
	}
	if full.Truncated {
		t.Fatal("状态枚举被截断，核对不完整")
	}
	if len(full.Losses) == 0 {
		t.Fatal("完整枚举未找到任何丢事件交错")
	}
	minLen := full.Losses[0].Length
	for _, l := range full.Losses {
		if len(l.Lost) == 0 {
			t.Errorf("丢事件记录缺少事件 ID: %+v", l)
		}
		if l.Length < minLen {
			minLen = l.Length
		}
	}
	if short.Witness == nil {
		t.Fatal("FindShortestLoss 未找到见证")
	}
	if short.Witness.Length() != minLen {
		t.Errorf("见证长度 %d 与完整枚举最短长度 %d 不一致", short.Witness.Length(), minLen)
	}
	found := false
	for _, l := range full.Losses {
		if l.Length == short.Witness.Length() && reflect.DeepEqual(l.Lost, short.Witness.Lost) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("见证丢失事件 %v 未在完整枚举记录中出现", short.Witness.Lost)
	}
	if full.ReverseDivergences != 0 {
		t.Errorf("存在 %d 处反向分歧（带代次成功而裸地址失败），违反代次严格性强假设", full.ReverseDivergences)
	}
}

// TestRunScheduleReplaysWitness 用 RunSchedule 重放见证调度，
// 核对逐步算出的两种 CAS 结果与枚举器一致。
func TestRunScheduleReplaysWitness(t *testing.T) {
	scripts := classicABAScripts()
	res, err := FindShortestLoss(scripts)
	if err != nil {
		t.Fatalf("FindShortestLoss: %v", err)
	}
	var sched []int
	for _, s := range res.Witness.Steps {
		sched = append(sched, s.Thread)
	}
	traces, err := RunSchedule(scripts, sched)
	if err != nil {
		t.Fatalf("RunSchedule: %v", err)
	}
	if len(traces) != len(res.Witness.Steps) {
		t.Fatalf("重放步数 %d != 见证步数 %d", len(traces), len(res.Witness.Steps))
	}
	for i := range traces {
		got, want := traces[i], res.Witness.Steps[i]
		if got.IsCAS != want.IsCAS || got.RawOK != want.RawOK || got.TaggedOK != want.TaggedOK ||
			got.Diverged != want.Diverged || !reflect.DeepEqual(got.Lost, want.Lost) {
			t.Errorf("第 %d 步重放不一致: got %+v want %+v", i+1, got, want)
		}
	}
	last := traces[len(traces)-1]
	if !reflect.DeepEqual(last.Lost, []int{4}) {
		t.Errorf("重放末步丢失事件 = %v, 期望 [4]", last.Lost)
	}
}

func TestParseScript(t *testing.T) {
	text := `
# 注释行
T0 alloc a 1
T0 read h
T1 alloc b 2
T0 cas h b
T1 free b
`
	got, err := ParseScript(text)
	if err != nil {
		t.Fatalf("ParseScript: %v", err)
	}
	want := [][]Op{
		{Alloc("a", 1), Read("h"), CAS("h", "b")},
		{Alloc("b", 2), Free("b")},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("解析结果 = %v, 期望 %v", got, want)
	}
}

func TestParseScriptErrors(t *testing.T) {
	for _, text := range []string{
		"T0 alloc a 1\nT0 alloc b 1\n", // 事件 ID 重复
		"T0 alloc a 0\n",               // 事件 ID 非正
		"T3 read h\n",                  // 线程号越界
		"T1 read h\n",                  // 线程号不连续
		"T0 frobnicate x\n",            // 未知指令
		"read h\n",                     // 缺少线程前缀
	} {
		if _, err := ParseScript(text); err == nil {
			t.Errorf("脚本 %q 应报错", text)
		}
	}
}

func TestNoLossWhenNoReuse(t *testing.T) {
	scripts := [][]Op{
		{Alloc("a", 1), Read("z"), CAS("z", "a")},
	}
	res, err := FindShortestLoss(scripts)
	if err != nil {
		t.Fatalf("FindShortestLoss: %v", err)
	}
	if res.Witness != nil {
		t.Errorf("单线程无复用场景不应有见证, 得到 %v", res.Witness)
	}
}

func TestValidateThreadLimit(t *testing.T) {
	scripts := make([][]Op, MaxThreads+1)
	for i := range scripts {
		scripts[i] = []Op{Read("h")}
	}
	if err := Validate(scripts); err == nil {
		t.Error("超过三线程应报错")
	}
}
