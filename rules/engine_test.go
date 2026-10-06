package rules

import (
	"fmt"
	"math/rand"
	"testing"
)

func ev(id, object string, prio int, seq int64, payload string) Event {
	return Event{ID: id, Object: object, Priority: prio, Seq: seq, Payload: payload}
}

func mustVerify(t *testing.T, e *Engine) {
	t.Helper()
	if ps := e.Verify(); len(ps) != 0 {
		t.Fatalf("全量模型核对失败: %v", ps)
	}
}

func TestPriorityThenSeq(t *testing.T) {
	e := New()
	e.Apply(ev("e1", "o", 1, 10, "低优先"))
	if got, _ := e.Effective("o"); got.ID != "e1" {
		t.Fatalf("got %s", got.ID)
	}
	e.Apply(ev("e2", "o", 5, 1, "高优先")) // 优先级高者胜，无视序号
	if got, _ := e.Effective("o"); got.ID != "e2" {
		t.Fatalf("got %s", got.ID)
	}
	e.Apply(ev("e3", "o", 5, 9, "同优先级大序号")) // 同优先级比序号
	if got, _ := e.Effective("o"); got.ID != "e3" {
		t.Fatalf("got %s", got.ID)
	}
	mustVerify(t, e)
}

func TestRetractBeforeEventArrives(t *testing.T) {
	e := New()
	e.Apply(ev("e1", "o", 1, 1, "旧值"))
	d := e.Retract("e2") // 撤回先到，事件尚未到达
	if d.Changed {
		t.Fatalf("撤回未知事件不应产生差量: %v", d)
	}
	d = e.Apply(ev("e2", "o", 9, 9, "被预埋撤回的高优事件"))
	if d.Changed {
		t.Fatalf("墓碑事件到达不应改变有效值: %v", d)
	}
	if got, _ := e.Effective("o"); got.ID != "e1" {
		t.Fatalf("got %s", got.ID)
	}
	mustVerify(t, e)
}

func TestRetractTargetsSpecificIDOnly(t *testing.T) {
	e := New()
	e.Apply(ev("e1", "o", 5, 1, "A"))
	e.Apply(ev("e2", "o", 5, 2, "B"))
	e.Apply(ev("e3", "x", 5, 3, "C")) // 同优先级同序号风格的其它对象
	d := e.Retract("e2")              // 只撤 e2
	if !d.Changed || d.New.ID != "e1" {
		t.Fatalf("应回退到 e1: %v", d)
	}
	if got, _ := e.Effective("x"); got.ID != "e3" {
		t.Fatalf("其它对象不应受影响: %s", got.ID)
	}
	e.Retract("e2") // 重复撤回幂等
	if got, _ := e.Effective("o"); got.ID != "e1" {
		t.Fatalf("重复撤回应幂等: %s", got.ID)
	}
	mustVerify(t, e)
}

func TestWinnerRetractedRevealsNextCandidate(t *testing.T) {
	e := New()
	e.Apply(ev("e1", "o", 1, 1, "第三候选"))
	e.Apply(ev("e2", "o", 2, 1, "第二候选"))
	e.Apply(ev("e3", "o", 3, 1, "赢家"))

	d := e.Retract("e3") // 赢家被撤，下一候选显露
	if !d.Changed || d.Old.ID != "e3" || d.New.ID != "e2" {
		t.Fatalf("差量应为 e3->e2: %v", d)
	}
	d = e.Retract("e2")
	if !d.Changed || d.New.ID != "e1" {
		t.Fatalf("差量应为 e2->e1: %v", d)
	}
	d = e.Retract("e1") // 全部撤回，有效值清空
	if !d.Changed || d.HasNew {
		t.Fatalf("差量应为 e1->空: %v", d)
	}
	if _, ok := e.Effective("o"); ok {
		t.Fatal("有效值应为空")
	}
	mustVerify(t, e)
}

func TestDuplicateAndConflictingPayload(t *testing.T) {
	e := New()
	e.Apply(ev("e1", "o", 1, 1, "原始载荷"))

	d := e.Apply(ev("e1", "o", 1, 1, "原始载荷")) // 完全重复
	if d.Changed || len(e.Duplicates) != 1 || len(e.Conflicts) != 0 {
		t.Fatalf("重复投递应幂等记录: changed=%v dups=%d conflicts=%d", d.Changed, len(e.Duplicates), len(e.Conflicts))
	}
	d = e.Apply(ev("e1", "o", 9, 9, "冲突载荷")) // 同 ID 不同内容
	if d.Changed || len(e.Conflicts) != 1 {
		t.Fatalf("冲突载荷应被记录并忽略: changed=%v conflicts=%d", d.Changed, len(e.Conflicts))
	}
	if got, _ := e.Effective("o"); got.Payload != "原始载荷" {
		t.Fatalf("应保留先到者: %s", got.Payload)
	}
	mustVerify(t, e)
}

func TestDeltaStream(t *testing.T) {
	e := New()
	var got []string
	record := func(d Delta) {
		if d.Changed {
			got = append(got, d.String())
		}
	}
	record(e.Apply(ev("e1", "o", 1, 1, "v1")))
	record(e.Apply(ev("e2", "o", 2, 1, "v2")))
	record(e.Apply(ev("e3", "o", 2, 2, "v3")))
	record(e.Apply(ev("e4", "o", 0, 9, "v4"))) // 低优先级，无差量
	record(e.Retract("e3"))
	record(e.Retract("e2"))
	record(e.Retract("e1"))
	record(e.Retract("e4")) // 撤掉最后的候选，有效值清空

	want := []string{
		"[o] 有效值: <无> -> v1(id=e1)",
		"[o] 有效值: v1(id=e1) -> v2(id=e2)",
		"[o] 有效值: v2(id=e2) -> v3(id=e3)",
		"[o] 有效值: v3(id=e3) -> v2(id=e2)",
		"[o] 有效值: v2(id=e2) -> v1(id=e1)",
		"[o] 有效值: v1(id=e1) -> v4(id=e4)", // e1 撤回后低优先级的 e4 显露
		"[o] 有效值: v4(id=e4) -> <无>",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("差量流不符:\n got=%v\nwant=%v", got, want)
	}
	mustVerify(t, e)
}

// TestFullModelCrossCheck 随机操作序列下，每步都用全候选排序模型核对。
func TestFullModelCrossCheck(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	e := New()
	objects := []string{"o1", "o2", "o3"}

	for step := 0; step < 3000; step++ {
		id := fmt.Sprintf("e%d", rng.Intn(120))
		if rng.Intn(3) == 0 {
			e.Retract(id) // 约 1/3 为撤回，可命中未到事件
		} else {
			e.Apply(Event{
				ID:       id,
				Object:   objects[rng.Intn(len(objects))],
				Priority: rng.Intn(4),
				Seq:      int64(rng.Intn(10)),
				Payload:  "p" + id,
			})
		}
		if ps := e.Verify(); len(ps) != 0 {
			t.Fatalf("第 %d 步后核对失败: %v", step, ps)
		}
	}
}
