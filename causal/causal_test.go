package causal

import (
	"reflect"
	"testing"
)

func ids(msgs []Message) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.ID
	}
	return out
}

// 同源顺序：A 的第 2 条先于第 1 条到达，必须挂起并报告精确缺口；
// 第 1 条到达后两条按序交付。
func TestSameSourceOrder(t *testing.T) {
	b := New()

	ready, gaps := b.Add(Message{ID: "a2", Sender: "A", Clock: map[string]uint64{"A": 2}})
	if len(ready) != 0 {
		t.Fatalf("a2 不应交付, got %v", ids(ready))
	}
	wantGaps := []Gap{{Sender: "A", From: 1, To: 1}}
	if !reflect.DeepEqual(gaps, wantGaps) {
		t.Fatalf("缺口 = %v, want %v", gaps, wantGaps)
	}
	if b.Pending() != 1 {
		t.Fatalf("待决数 = %d, want 1", b.Pending())
	}

	ready, gaps = b.Add(Message{ID: "a1", Sender: "A", Clock: map[string]uint64{"A": 1}})
	if gaps != nil {
		t.Fatalf("a1 不应有缺口, got %v", gaps)
	}
	if got, want := ids(ready), []string{"a1", "a2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("交付顺序 = %v, want %v", got, want)
	}
	if b.Pending() != 0 || b.Delivered("A") != 2 {
		t.Fatalf("状态错误: pending=%d delivered[A]=%d", b.Pending(), b.Delivered("A"))
	}
}

// 跨源依赖：B 的消息依赖 A:1，必须先挂起，A:1 交付后级联唤醒。
func TestCrossSourceDependency(t *testing.T) {
	b := New()

	ready, gaps := b.Add(Message{ID: "b1", Sender: "B", Clock: map[string]uint64{"A": 1, "B": 1}})
	if len(ready) != 0 {
		t.Fatalf("b1 不应交付, got %v", ids(ready))
	}
	wantGaps := []Gap{{Sender: "A", From: 1, To: 1}}
	if !reflect.DeepEqual(gaps, wantGaps) {
		t.Fatalf("缺口 = %v, want %v", gaps, wantGaps)
	}

	ready, _ = b.Add(Message{ID: "a1", Sender: "A", Clock: map[string]uint64{"A": 1}})
	if got, want := ids(ready), []string{"a1", "b1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("交付顺序 = %v, want %v", got, want)
	}
}

// 多源精确缺口：C 的消息同时缺 A 的两条和 B 的三条。
func TestPreciseMultiSourceGaps(t *testing.T) {
	b := New()
	b.Add(Message{ID: "a1", Sender: "A", Clock: map[string]uint64{"A": 1}})

	_, gaps := b.Add(Message{ID: "c1", Sender: "C", Clock: map[string]uint64{"A": 3, "B": 2, "C": 1}})
	want := []Gap{
		{Sender: "A", From: 2, To: 3},
		{Sender: "B", From: 1, To: 2},
	}
	if !reflect.DeepEqual(gaps, want) {
		t.Fatalf("缺口 = %v, want %v", gaps, want)
	}
}

// 级联链：c1 依赖 b1，b1 依赖 a1；a1 到达后三条依次交付。
func TestTransitiveCascade(t *testing.T) {
	b := New()
	b.Add(Message{ID: "c1", Sender: "C", Clock: map[string]uint64{"B": 1, "C": 1}})
	b.Add(Message{ID: "b1", Sender: "B", Clock: map[string]uint64{"A": 1, "B": 1}})
	if b.Pending() != 2 {
		t.Fatalf("待决数 = %d, want 2", b.Pending())
	}

	ready, _ := b.Add(Message{ID: "a1", Sender: "A", Clock: map[string]uint64{"A": 1}})
	if got, want := ids(ready), []string{"a1", "b1", "c1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("交付顺序 = %v, want %v", got, want)
	}
	if b.Pending() != 0 {
		t.Fatalf("待决数 = %d, want 0", b.Pending())
	}
}

// 重复消息造成的唤醒：
//  1. 待决消息的重复副本不得产生重复等待者，唤醒后只交付一次；
//  2. 已交付消息的重复副本不得再次交付，也不得误唤醒其他待决消息。
func TestDuplicateMessages(t *testing.T) {
	b := New()

	dep := Message{ID: "b1", Sender: "B", Clock: map[string]uint64{"A": 1, "B": 1}}
	b.Add(dep)
	// 待决消息的重复副本：静默忽略。
	if ready, gaps := b.Add(dep); ready != nil || gaps != nil {
		t.Fatalf("重复待决消息应被忽略, ready=%v gaps=%v", ready, gaps)
	}
	if b.Pending() != 1 {
		t.Fatalf("重复副本不应新增待决项, pending=%d", b.Pending())
	}

	// 无关消息，依赖 A:2，用来验证后面的重复副本不会误唤醒它。
	b.Add(Message{ID: "d1", Sender: "D", Clock: map[string]uint64{"A": 2, "D": 1}})

	a1 := Message{ID: "a1", Sender: "A", Clock: map[string]uint64{"A": 1}}
	ready, _ := b.Add(a1)
	if got, want := ids(ready), []string{"a1", "b1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("交付顺序 = %v, want %v", got, want)
	}

	// 已交付消息的重复副本：不得再次交付，也不得误唤醒 d1（它缺的是 A:2）。
	if ready, gaps := b.Add(a1); ready != nil || gaps != nil {
		t.Fatalf("重复已交付消息应被忽略, ready=%v gaps=%v", ready, gaps)
	}
	if ready, gaps := b.Add(dep); ready != nil || gaps != nil {
		t.Fatalf("重复已交付消息应被忽略, ready=%v gaps=%v", ready, gaps)
	}
	if b.Pending() != 1 {
		t.Fatalf("d1 应仍在待决, pending=%d", b.Pending())
	}

	// 补上 A:2 后 d1 被唤醒，且只交付一次。
	ready, _ = b.Add(Message{ID: "a2", Sender: "A", Clock: map[string]uint64{"A": 2}})
	if got, want := ids(ready), []string{"a2", "d1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("交付顺序 = %v, want %v", got, want)
	}
	if b.Pending() != 0 {
		t.Fatalf("待决数 = %d, want 0", b.Pending())
	}
}

// 同源大缺口：A:5 先到达，缺口为 [1,4]，补齐中间三条后按序交付。
func TestLargeSameSourceGap(t *testing.T) {
	b := New()
	_, gaps := b.Add(Message{ID: "a5", Sender: "A", Clock: map[string]uint64{"A": 5}})
	want := []Gap{{Sender: "A", From: 1, To: 4}}
	if !reflect.DeepEqual(gaps, want) {
		t.Fatalf("缺口 = %v, want %v", gaps, want)
	}

	var delivered []string
	for i := uint64(1); i <= 4; i++ {
		id := "a" + string(rune('0'+i))
		ready, _ := b.Add(Message{ID: id, Sender: "A", Clock: map[string]uint64{"A": i}})
		delivered = append(delivered, ids(ready)...)
	}
	if got, want := delivered, []string{"a1", "a2", "a3", "a4", "a5"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("交付顺序 = %v, want %v", got, want)
	}
}
