package telemetry

import (
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestDeterministicReplay(t *testing.T) {
	cfg := testConfig(6, 3, 2*time.Second)
	events := SynthEvents(7, 500)
	s1 := Replay(cfg, events)
	s2 := Replay(cfg, events)
	if !reflect.DeepEqual(s1, s2) {
		t.Fatal("same events replayed twice produced different snapshots")
	}
	if problems := Verify(s1); len(problems) > 0 {
		for _, p := range problems {
			t.Errorf("invariant: %s", p)
		}
	}
}

func TestSynthReplayInvariants(t *testing.T) {
	cfg := testConfig(6, 3, 2*time.Second)
	for seed := int64(1); seed <= 8; seed++ {
		snap := Replay(cfg, SynthEvents(seed, 800))
		if problems := Verify(snap); len(problems) > 0 {
			for _, p := range problems {
				t.Errorf("seed %d: %s", seed, p)
			}
		}
	}
}

// 手工夹具：核验接收量守恒、过期代际隔离与公平分配。
func TestFixtureReplay(t *testing.T) {
	cfg := testConfig(3, 2, time.Second)
	base := time.Unix(1000, 0)
	at := func(sec int) time.Time { return base.Add(time.Duration(sec) * time.Second) }
	sample := func(sec int, metric, inst string, v int64) Event {
		return Event{Kind: EvSample, At: at(sec), Tenant: "t", Metric: metric,
			Labels: lbl("instance", inst), Value: v, Bytes: 10}
	}
	events := []Event{
		sample(0, "m", "a", 1),
		sample(0, "m", "b", 2),
		sample(0, "m", "c", 4),       // 指标额度 2：折叠
		sample(0, "n", "a", 8),       // 另一指标不受 m 影响（公平）
		{Kind: EvReclaim, At: at(3)}, // a/b 过期回收，c 的贡献留在溢出桶
		sample(4, "m", "a", 16),      // 迟到样本：新代际
		sample(4, "m", "c", 32),      // 额度已释放：录取并撤回折叠贡献
		sample(4, "n", "a", 8),       // n 仍有额度：录取
	}
	snap := Replay(cfg, events)
	if problems := Verify(snap); len(problems) > 0 {
		for _, p := range problems {
			t.Errorf("invariant: %s", p)
		}
	}

	m := snap.Tenants["t"].Metrics["m"]
	if m.Received != 5 {
		t.Fatalf("received=%d", m.Received)
	}
	// 守恒：接收 5 = 存活(a 新代际 1 + c 撤回后 2) + 封存(a 旧代际 1 + b 1)。
	var live, arch int64
	for _, s := range m.Series {
		live += s.Samples
	}
	for _, a := range m.Archived {
		arch += a.Samples
	}
	if live+arch+m.OverflowSamples != 5 {
		t.Fatalf("conservation: live=%d archived=%d overflow=%d", live, arch, m.OverflowSamples)
	}
	// 代际隔离：a 的旧代际封存在 1，新代际存活且统计不混。
	keyA := LabelKey(mustCanonical(t, lbl("instance", "a")))
	if got := m.Series[keyA]; got.Samples != 1 || got.Value != 16 {
		t.Fatalf("late series a: %+v", got)
	}
	foundOldGen := false
	for _, a := range m.Archived {
		if a.LabelKey == keyA {
			foundOldGen = true
			if a.Generation == m.Series[keyA].Generation {
				t.Fatal("old and new generations share a generation number")
			}
			if a.Value != 1 {
				t.Fatalf("archived a value=%d", a.Value)
			}
		}
	}
	if !foundOldGen {
		t.Fatal("old generation of a not archived")
	}
	// 撤回：c 的折叠贡献并入新系列。
	keyC := LabelKey(mustCanonical(t, lbl("instance", "c")))
	if got := m.Series[keyC]; got.Samples != 2 || got.Value != 36 {
		t.Fatalf("retracted series c: %+v", got)
	}
	if m.OverflowSamples != 0 {
		t.Fatalf("overflow=%d", m.OverflowSamples)
	}
	// 公平：n 的系列被录取。
	n := snap.Tenants["t"].Metrics["n"]
	if got := n.LiveCounted; got != 1 {
		t.Fatalf("metric n live=%d", got)
	}
	if got := n.Received; got != 2 {
		t.Fatalf("metric n received=%d", got)
	}
}

// 过期回收、迟到样本与标签更正并发时，额度与账本调整保持原子：
// 结束后所有不变量必须成立，接收总数必须等于成功接收的样本数。
func TestConcurrentInvariants(t *testing.T) {
	cfg := testConfig(8, 4, 5*time.Millisecond)
	l := NewLimiter(cfg, time.Now)

	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			tenants := []string{"t1", "t2"}
			metrics := []string{"m1", "m2", "m3"}
			for i := 0; i < 250; i++ {
				tenant := tenants[(w+i)%2]
				metric := metrics[(w+i)%3]
				inst := itoa((w*7 + i) % 10)
				l.Ingest(Sample{
					Tenant: tenant, Metric: metric,
					Labels: lbl("instance", inst, "job", "j"),
					Value:  1, Bytes: 8,
				})
				if i%17 == 0 {
					l.Correct(tenant, metric,
						lbl("instance", inst, "job", "j"),
						lbl("instance", itoa((w+i+1)%10), "job", "j"))
				}
				if i%23 == 0 {
					l.Reclaim()
				}
			}
		}(w)
	}
	wg.Wait()

	snap := l.Snapshot()
	if problems := Verify(snap); len(problems) > 0 {
		for _, p := range problems {
			t.Errorf("invariant: %s", p)
		}
	}
	var received int64
	for _, tn := range snap.Tenants {
		for _, m := range tn.Metrics {
			received += m.Received
		}
	}
	if want := int64(8 * 250); received != want {
		t.Fatalf("received=%d want=%d (samples must never vanish silently)", received, want)
	}
}
