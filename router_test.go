package causalrouter

import "testing"

func ids(msgs []*Message) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.ID
	}
	return out
}

func equalIDs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// 验收边界：X 发送 (A:1)，Y 收到后发送 (A:1, B:1)；
// 接收端先收到 Y 的消息时必须暂存，直到收到 X 的 (A:1) 后按因果顺序交付。
func TestAcceptanceCausalOrder(t *testing.T) {
	r := NewRouter()
	x := NewClient("A")
	y := NewClient("B")

	m1 := x.Send("hello from A") // (A:1)
	if got := m1.Clock.String(); got != "(A:1)" {
		t.Fatalf("m1 clock = %s, want (A:1)", got)
	}

	y.Observe(m1)                // Y 先收到 X 的消息
	m2 := y.Send("reply from B") // (A:1, B:1)
	if got := m2.Clock.String(); got != "(A:1, B:1)" {
		t.Fatalf("m2 clock = %s, want (A:1, B:1)", got)
	}

	// 乱序到达：Y 的消息先到，必须暂存，不得交付。
	if d := r.Submit(m2); len(d) != 0 {
		t.Fatalf("m2 delivered before its dependency: %v", ids(d))
	}
	if got := ids(r.Pending()); !equalIDs(got, []string{"B:1"}) {
		t.Fatalf("pending = %v, want [B:1]", got)
	}

	// X 的消息到达后，两条消息按因果顺序交付。
	d := r.Submit(m1)
	if !equalIDs(ids(d), []string{"A:1", "B:1"}) {
		t.Fatalf("delivered = %v, want [A:1 B:1]", ids(d))
	}
	if len(r.Pending()) != 0 {
		t.Fatalf("pending not drained: %v", ids(r.Pending()))
	}
}

func TestSameSenderGapWaits(t *testing.T) {
	r := NewRouter()
	a := NewClient("A")
	m1 := a.Send("first")
	m2 := a.Send("second")

	if d := r.Submit(m2); len(d) != 0 {
		t.Fatalf("gap message delivered early: %v", ids(d))
	}
	if d := r.Submit(m1); !equalIDs(ids(d), []string{"A:1", "A:2"}) {
		t.Fatalf("delivered = %v, want [A:1 A:2]", ids(d))
	}
}

func TestDuplicateDropped(t *testing.T) {
	r := NewRouter()
	a := NewClient("A")
	m1 := a.Send("once")

	if d := r.Submit(m1); !equalIDs(ids(d), []string{"A:1"}) {
		t.Fatalf("delivered = %v, want [A:1]", ids(d))
	}
	if d := r.Submit(m1); len(d) != 0 {
		t.Fatalf("duplicate delivered: %v", ids(d))
	}
	if got := len(r.Delivered()); got != 1 {
		t.Fatalf("delivered count = %d, want 1", got)
	}
}

func TestConcurrentMessagesDeliverIndependently(t *testing.T) {
	r := NewRouter()
	a := NewClient("A")
	b := NewClient("B")
	ma := a.Send("from A") // (A:1)
	mb := b.Send("from B") // (B:1)，与 ma 并发

	if Compare(ma.Clock, mb.Clock) != Concurrent {
		t.Fatalf("expected concurrent clocks")
	}
	if d := r.Submit(mb); !equalIDs(ids(d), []string{"B:1"}) {
		t.Fatalf("delivered = %v, want [B:1]", ids(d))
	}
	if d := r.Submit(ma); !equalIDs(ids(d), []string{"A:1"}) {
		t.Fatalf("delivered = %v, want [A:1]", ids(d))
	}
}

// 循环因果：A 的消息依赖 B:1，B 的消息依赖 A:1，互相等待，永远无法交付。
// 路由器必须生成不可达证据并隔离两条消息。
func TestCyclicCausalityQuarantined(t *testing.T) {
	r := NewRouter()
	mA := &Message{ID: "A:1", Sender: "A", Clock: VectorClock{"A": 1, "B": 1}, Payload: "waits on B"}
	mB := &Message{ID: "B:1", Sender: "B", Clock: VectorClock{"A": 1, "B": 1}, Payload: "waits on A"}

	if d := r.Submit(mA); len(d) != 0 {
		t.Fatalf("cyclic message delivered: %v", ids(d))
	}
	r.Submit(mB)

	if got := ids(r.Quarantined()); !equalIDs(got, []string{"A:1", "B:1"}) {
		t.Fatalf("quarantined = %v, want [A:1 B:1]", got)
	}
	if len(r.Pending()) != 0 {
		t.Fatalf("cycle left in pending: %v", ids(r.Pending()))
	}
	ev := r.Evidence()
	if len(ev) != 2 {
		t.Fatalf("evidence count = %d, want 2", len(ev))
	}
	for _, e := range ev {
		if len(e.Cycle) == 0 {
			t.Fatalf("evidence for %s missing cycle: %+v", e.MessageID, e)
		}
	}
}

// 依赖被隔离消息的暂存消息同样不可达，应被传染式隔离。
func TestDependentOfQuarantinedAlsoQuarantined(t *testing.T) {
	r := NewRouter()
	mA := &Message{ID: "A:1", Sender: "A", Clock: VectorClock{"A": 1, "B": 1}}
	mB := &Message{ID: "B:1", Sender: "B", Clock: VectorClock{"A": 1, "B": 1}}
	// C 的第一条消息因果依赖于 A:1（被隔离成员）。
	mC := &Message{ID: "C:1", Sender: "C", Clock: VectorClock{"A": 1, "C": 1}}

	r.Submit(mA)
	r.Submit(mC) // 先入队等待 A:1
	r.Submit(mB) // 触发环检测：A:1、B:1 被隔离，C:1 依赖 A:1 也被隔离

	if got := ids(r.Quarantined()); !equalIDs(got, []string{"A:1", "B:1", "C:1"}) {
		t.Fatalf("quarantined = %v, want [A:1 B:1 C:1]", got)
	}
	ev := r.Evidence()
	if len(ev) != 3 {
		t.Fatalf("evidence count = %d, want 3", len(ev))
	}
	last := ev[2]
	if last.MessageID != "C:1" || len(last.Cycle) != 0 {
		t.Fatalf("unexpected evidence for C:1: %+v", last)
	}
}

func TestVectorClockCompare(t *testing.T) {
	cases := []struct {
		a, b VectorClock
		want Relation
	}{
		{VectorClock{"A": 1}, VectorClock{"A": 1}, Equal},
		{VectorClock{"A": 1}, VectorClock{"A": 1, "B": 1}, Before},
		{VectorClock{"A": 2, "B": 1}, VectorClock{"A": 1}, After},
		{VectorClock{"A": 1}, VectorClock{"B": 1}, Concurrent},
		{VectorClock{"A": 2, "B": 1}, VectorClock{"A": 1, "B": 2}, Concurrent},
	}
	for i, c := range cases {
		if got := Compare(c.a, c.b); got != c.want {
			t.Errorf("case %d: Compare(%s, %s) = %v, want %v", i, c.a, c.b, got, c.want)
		}
		if got := Compare(c.b, c.a); got != -c.want && !(got == Equal && c.want == Equal) {
			// Before/After 互为反向；Equal/Concurrent 自反。
			if c.want == Before && got != After {
				t.Errorf("case %d: reverse Compare = %v, want after", i, got)
			}
			if c.want == After && got != Before {
				t.Errorf("case %d: reverse Compare = %v, want before", i, got)
			}
			if c.want == Concurrent && got != Concurrent {
				t.Errorf("case %d: reverse Compare = %v, want concurrent", i, got)
			}
		}
	}
}

func TestOnDeliverHook(t *testing.T) {
	r := NewRouter()
	var got []string
	r.OnDeliver = func(m *Message) { got = append(got, m.ID) }

	x := NewClient("A")
	y := NewClient("B")
	m1 := x.Send("x")
	y.Observe(m1)
	m2 := y.Send("y")

	r.Submit(m2)
	r.Submit(m1)
	if !equalIDs(got, []string{"A:1", "B:1"}) {
		t.Fatalf("hook deliveries = %v, want [A:1 B:1]", got)
	}
}
