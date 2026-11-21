package wfqsched

import "testing"

// 同一时刻整批到达：批内包按 slice 顺序分配连续全局序号，并在同一次 Arrive
// 调用内全部纳入流体系统与真实等待队列；发包器只启动标签最小的一个。
func TestBatchArrivalSingleInstant(t *testing.T) {
	s := mustNew(t, map[FlowID]uint64{1: 2, 2: 1, 3: 1})

	// 三个包同在 t=5 到达，标签分别为 3/2、3、3；流2、流3 标签相同按序号决胜。
	started, err := s.Arrive(5, []Packet{
		{Flow: 1, ID: 1, Length: 3}, // 标签 0 + 3/2 = 1.5
		{Flow: 2, ID: 2, Length: 3}, // 标签 3，序号 2
		{Flow: 3, ID: 3, Length: 3}, // 标签 3，序号 3
	})
	if err != nil {
		t.Fatal(err)
	}
	if started == nil || started.Flow != 1 || started.ID != 1 {
		t.Fatalf("batch must start lowest-tag packet, got %+v", started)
	}
	if got := s.Waiting(); got != 2 {
		t.Fatalf("two packets remain waiting, got %d", got)
	}
	for id, want := range map[FlowID]string{1: "3/2", 2: "3", 3: "3"} {
		tag, _ := s.LastTag(id)
		if tag.RatString() != want {
			t.Fatalf("flow %d tag = %s, want %s", id, tag, want)
		}
	}

	got := drainAll(t, s)
	want := []Interval{
		iv(1, 1, 5, 8),
		iv(2, 2, 8, 11),
		iv(3, 3, 11, 14),
	}
	if len(got) != len(want) {
		t.Fatalf("Drain = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Drain[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// 到达时刻晚于当前时钟：先空转（流体系统空闲、V 冻结），再纳入整批。
func TestArrivalAfterIdleGap(t *testing.T) {
	s := mustNew(t, map[FlowID]uint64{1: 1})

	started, err := s.Arrive(100, []Packet{{Flow: 1, ID: 1, Length: 5}})
	if err != nil {
		t.Fatal(err)
	}
	if started == nil || started.ID != 1 {
		t.Fatalf("idle-gap arrival must start immediately, got %+v", started)
	}
	if got := s.VirtualTime(); got.Sign() != 0 {
		t.Fatalf("V must stay frozen while idle, got %s", got)
	}
	got := drainAll(t, s)
	want := []Interval{iv(1, 1, 100, 105)}
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("Drain = %+v, want %+v", got, want)
	}
}
