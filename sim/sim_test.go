package sim

import (
	"testing"

	"reorder"
)

func TestSchedulerTickOrderingAndClock(t *testing.T) {
	s := NewScheduler()
	var log []string

	// 乱序安排：验证按 (tick, 入队次序) 触发。
	mustSched(t, s.Schedule(2, func(int64) []Event { log = append(log, "t2"); return nil }))
	mustSched(t, s.Schedule(0, func(int64) []Event { log = append(log, "t0a"); return nil }))
	mustSched(t, s.Schedule(0, func(now int64) []Event {
		log = append(log, "t0b")
		// 在 tick 0 追加 tick 1 的事件。
		return []Event{{At: now + 1, Fn: func(int64) []Event { log = append(log, "t1"); return nil }}}
	}))

	var hooks []int64
	if err := s.RunUntil(2, func(tick int64) { hooks = append(hooks, tick) }); err != nil {
		t.Fatal(err)
	}
	want := []string{"t0a", "t0b", "t1", "t2"}
	if len(log) != len(want) {
		t.Fatalf("events=%v want %v", log, want)
	}
	for i := range want {
		if log[i] != want[i] {
			t.Fatalf("events[%d]=%q want %q (full=%v)", i, log[i], want[i], log)
		}
	}
	if len(hooks) != 3 || hooks[0] != 0 || hooks[2] != 2 {
		t.Fatalf("hooks=%v want ticks 0..2", hooks)
	}
	if s.Now() != 2 || s.Pending() != 0 {
		t.Fatalf("now=%d pending=%d", s.Now(), s.Pending())
	}
}

func TestSchedulerRejectsPastEvents(t *testing.T) {
	s := NewScheduler()
	mustSched(t, s.RunUntil(5, nil))
	if err := s.Schedule(4, func(int64) []Event { return nil }); err == nil {
		t.Fatal("scheduling into the past must error")
	}
	if err := s.After(-1, func(int64) []Event { return nil }); err == nil {
		t.Fatal("negative After must error")
	}
}

func TestScriptLinkReordersAndDrops(t *testing.T) {
	s := NewScheduler()
	link := ScriptLink{
		Offset: func(p reorder.Packet) int64 {
			if p.Seq == 1 {
				return 5 // 1 比 2 晚到
			}
			return 1
		},
		Drop: func(p reorder.Packet) bool { return p.Seq == 3 },
	}
	var got []int64
	recv := func(p reorder.Packet) { got = append(got, p.Seq) }
	// 发送顺序 1,2,3。
	for seq := int64(1); seq <= 3; seq++ {
		if err := link.Send(s, seq, reorder.Packet{Epoch: 0, Seq: seq, Tick: seq}, recv); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RunUntil(10, nil); err != nil {
		t.Fatal(err)
	}
	// seq2 在 tick3 到达，seq1 在 tick6 到达，seq3 丢弃。
	if len(got) != 2 || got[0] != 2 || got[1] != 1 {
		t.Fatalf("arrival order=%v want [2 1]", got)
	}
}

func TestRandomLinkDeterministicAndEndToEnd(t *testing.T) {
	// 固定种子下两次运行结果必须完全一致；同时做一次与重排器的
	// 端到端集成：丢失若干包后流仍能因超时而推进，不永久阻塞。
	run := func() (released int64, sides int) {
		s := NewScheduler()
		link, err := NewLink(LinkConfig{MinDelay: 1, MaxDelay: 4, LossProb: 0.4, DupProb: 0.1}, 42)
		if err != nil {
			t.Fatal(err)
		}
		rec, err := reorder.New(reorder.Config{MaxWaitTicks: 6, CacheCap: 8}, 0)
		if err != nil {
			t.Fatal(err)
		}
		recv := func(p reorder.Packet) {
			if err := rec.Handle(p, s.Now()); err != nil {
				t.Fatal(err)
			}
		}
		const n = 40
		for seq := int64(1); seq <= n; seq++ {
			p := reorder.Packet{Epoch: 0, Seq: seq, Tick: seq}
			if err := link.Send(s, seq, p, recv); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.RunUntil(n+12, func(tick int64) {
			if err := rec.Tick(tick); err != nil {
				t.Fatal(err)
			}
		}); err != nil {
			t.Fatal(err)
		}
		rec.Close()
		var rel int64
		for it := range rec.Out() {
			rel++
			_ = it
		}
		var sd int
		for range rec.Side() {
			sd++
		}
		// 每个序号恰好以“包”或“缺口”形式出现一次，覆盖到 seq n。
		st := rec.Stats()
		if st.Released+st.MissingSeq != n {
			t.Fatalf("coverage released=%d missing=%d want total %d", st.Released, st.MissingSeq, n)
		}
		return rel, sd
	}

	r1, s1 := run()
	r2, s2 := run()
	if r1 != r2 || s1 != s2 {
		t.Fatalf("nondeterministic: run1=(%d,%d) run2=(%d,%d)", r1, s1, r2, s2)
	}
	t.Logf("deterministic run: output items=%d side items=%d", r1, s1)
}

func TestNewLinkValidatesConfig(t *testing.T) {
	if _, err := NewLink(LinkConfig{MinDelay: 5, MaxDelay: 1}, 1); err == nil {
		t.Fatal("invalid delay range must error")
	}
	if _, err := NewLink(LinkConfig{MinDelay: 0, MaxDelay: 1, LossProb: 2}, 1); err == nil {
		t.Fatal("invalid probability must error")
	}
}

func mustSched(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
