package routemig

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

// buildTopologies 构造旧拓扑（4 节点）与新拓扑（6 节点）。
func buildTopologies(t *testing.T) (*Topology, *Topology) {
	t.Helper()
	oldCfg := ClusterConfig{Name: "Top_Old"}
	for i := 0; i < 4; i++ {
		oldCfg.Nodes = append(oldCfg.Nodes, NodeSpec{
			ID:   fmt.Sprintf("old-node-%d", i),
			Addr: fmt.Sprintf("10.0.0.%d:7000", i),
		})
	}
	newCfg := ClusterConfig{Name: "Top_New"}
	for i := 0; i < 6; i++ {
		newCfg.Nodes = append(newCfg.Nodes, NodeSpec{
			ID:   fmt.Sprintf("new-node-%d", i),
			Addr: fmt.Sprintf("10.1.0.%d:7000", i),
		})
	}
	oldTopo, err := NewTopology(oldCfg)
	if err != nil {
		t.Fatalf("build old topology: %v", err)
	}
	newTopo, err := NewTopology(newCfg)
	if err != nil {
		t.Fatalf("build new topology: %v", err)
	}
	return oldTopo, newTopo
}

// TestGraySplit10Percent 验证 10% 灰度时流量分布符合预期。
func TestGraySplit10Percent(t *testing.T) {
	oldTopo, newTopo := buildTopologies(t)
	c := NewController(oldTopo)
	c.PublishNewTopology(newTopo)
	if err := c.SetGrayPercent(10); err != nil {
		t.Fatalf("set gray percent: %v", err)
	}

	const total = 100_000
	for i := 0; i < total; i++ {
		c.Write(fmt.Sprintf("key-%d", i), "v")
	}
	dist := c.VersionDistribution()
	got := float64(dist[VersionNew]) / total * 100
	t.Logf("new topology share: %.2f%% (old=%d new=%d)", got, dist[VersionOld], dist[VersionNew])
	if got < 8.5 || got > 11.5 {
		t.Fatalf("new topology share %.2f%% outside [8.5%%, 11.5%%]", got)
	}
}

// TestNoDualWriteSplit 验证任一键的所有写入永远落在同一代拓扑。
func TestNoDualWriteSplit(t *testing.T) {
	oldTopo, newTopo := buildTopologies(t)
	c := NewController(oldTopo)
	c.PublishNewTopology(newTopo)

	const keys = 20_000
	firstSeen := make(map[string]Version, keys)
	for gray := 0; gray <= 100; gray += 10 {
		if err := c.SetGrayPercent(gray); err != nil {
			t.Fatalf("set gray %d: %v", gray, err)
		}
		for i := 0; i < keys; i++ {
			key := fmt.Sprintf("key-%d", i)
			_, v := c.Write(key, fmt.Sprintf("gen-%d", gray))
			if fv, ok := firstSeen[key]; ok && fv != v {
				t.Fatalf("key %s routed to %s after %s: dual-write split", key, v, fv)
			}
			firstSeen[key] = v
		}
	}
}

// TestReadYourWritesNoLoss 验证迁移全程写后读不丢数据（无丢包）。
func TestReadYourWritesNoLoss(t *testing.T) {
	oldTopo, newTopo := buildTopologies(t)
	c := NewController(oldTopo)

	// 迁移前先在旧拓扑写入一批存量数据。
	const preexisting = 5_000
	for i := 0; i < preexisting; i++ {
		c.Write(fmt.Sprintf("pre-%d", i), fmt.Sprintf("pre-val-%d", i))
	}

	c.PublishNewTopology(newTopo)
	if err := c.SetGrayPercent(10); err != nil {
		t.Fatalf("set gray 10: %v", err)
	}

	expected := make(map[string]string, preexisting*2)
	for i := 0; i < preexisting; i++ {
		expected[fmt.Sprintf("pre-%d", i)] = fmt.Sprintf("pre-val-%d", i)
	}

	lost := 0
	step := 0
	for gray := 10; gray <= 100; gray += 10 {
		if err := c.SetGrayPercent(gray); err != nil {
			t.Fatalf("set gray %d: %v", gray, err)
		}
		// 每个灰度阶段：写入新键 + 覆写存量键 + 校验全部键可读。
		for i := 0; i < 2_000; i++ {
			key := fmt.Sprintf("step%d-key-%d", step, i)
			val := fmt.Sprintf("step%d-val-%d", step, i)
			c.Write(key, val)
			expected[key] = val
		}
		for i := 0; i < 500; i++ {
			key := fmt.Sprintf("pre-%d", i)
			val := fmt.Sprintf("pre-val-%d-overwritten@%d", i, gray)
			c.Write(key, val)
			expected[key] = val
		}
		for key, want := range expected {
			got, ok, _ := c.Read(key)
			if !ok || got != want {
				lost++
				if lost <= 5 {
					t.Errorf("gray=%d: read %q got (%q,%v), want %q", gray, key, got, ok, want)
				}
			}
		}
		step++
	}
	if lost > 0 {
		t.Fatalf("lost %d reads during migration", lost)
	}
	t.Logf("migration 10%%->100%% complete, %d keys verified, 0 lost", len(expected))
}

// TestKeyVersionStickiness 验证灰度变化不影响已固化的路由决策。
func TestKeyVersionStickiness(t *testing.T) {
	oldTopo, newTopo := buildTopologies(t)
	c := NewController(oldTopo)
	c.PublishNewTopology(newTopo)
	if err := c.SetGrayPercent(10); err != nil {
		t.Fatal(err)
	}

	const keys = 10_000
	pinned := make(map[string]Version, keys)
	for i := 0; i < keys; i++ {
		key := fmt.Sprintf("key-%d", i)
		_, v := c.Write(key, "v1")
		pinned[key] = v
	}

	for _, gray := range []int{50, 100, 0, 30} {
		if err := c.SetGrayPercent(gray); err != nil {
			t.Fatal(err)
		}
		for key, want := range pinned {
			got := c.routeVersion(key)
			if got != want {
				t.Fatalf("key %s drifted from %s to %s at gray=%d", key, want, got, gray)
			}
		}
	}
}

// TestReadPinsVersion 验证读未存在的键也会固化代际，防止后续写漂移。
func TestReadPinsVersion(t *testing.T) {
	oldTopo, newTopo := buildTopologies(t)
	c := NewController(oldTopo)
	c.PublishNewTopology(newTopo)
	if err := c.SetGrayPercent(50); err != nil {
		t.Fatal(err)
	}

	if _, ok, _ := c.Read("ghost-key"); ok {
		t.Fatal("unexpected hit for ghost-key")
	}
	v, ok := c.KeyVersion("ghost-key")
	if !ok {
		t.Fatal("read did not pin version")
	}
	// 灰度推到 100% 后，该键仍应路由到固化代际。
	if err := c.SetGrayPercent(100); err != nil {
		t.Fatal(err)
	}
	if got := c.routeVersion("ghost-key"); got != v {
		t.Fatalf("ghost-key drifted from %s to %s", v, got)
	}
}

// TestSetGrayPercentValidation 验证非法配置被拒绝。
func TestSetGrayPercentValidation(t *testing.T) {
	oldTopo, _ := buildTopologies(t)
	c := NewController(oldTopo)
	if err := c.SetGrayPercent(10); err == nil {
		t.Fatal("expected error: new topology not published")
	}
	if _, err := c.StepUp(10); err == nil {
		t.Fatal("expected error: step before publish")
	}
	newTopo := oldTopo
	c.PublishNewTopology(newTopo)
	for _, p := range []int{-1, 101} {
		if err := c.SetGrayPercent(p); err == nil {
			t.Fatalf("expected error for gray=%d", p)
		}
	}
	if _, err := c.StepUp(0); err == nil {
		t.Fatal("expected error for non-positive step")
	}
}

// TestStepUpReachesExactly100 验证按 10% 步长推进精确到达 100%。
func TestStepUpReachesExactly100(t *testing.T) {
	oldTopo, newTopo := buildTopologies(t)
	c := NewController(oldTopo)
	c.PublishNewTopology(newTopo)
	for want := 10; want <= 100; want += 10 {
		got, err := c.StepUp(10)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("gray=%d, want %d", got, want)
		}
	}
	// 继续推进应钳制在 100%。
	if got, _ := c.StepUp(10); got != 100 {
		t.Fatalf("gray=%d after overshoot, want 100", got)
	}
}

// TestConcurrentNoSplit 在并发读写下验证无分裂、无丢失（配合 -race）。
func TestConcurrentNoSplit(t *testing.T) {
	oldTopo, newTopo := buildTopologies(t)
	c := NewController(oldTopo)
	c.PublishNewTopology(newTopo)

	const workers = 16
	const keysPerWorker = 2_000
	var wg sync.WaitGroup
	errs := make(chan error, workers*keysPerWorker)

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(w)))
			for i := 0; i < keysPerWorker; i++ {
				key := fmt.Sprintf("w%d-key-%d", w, i)
				val := fmt.Sprintf("w%d-val-%d", w, i)
				c.Write(key, val)
				if rng.Intn(3) == 0 {
					if got, ok, _ := c.Read(key); !ok || got != val {
						errs <- fmt.Errorf("key %s: got (%q,%v), want %q", key, got, ok, val)
					}
				}
			}
		}(w)
	}

	// 并发推进灰度比例。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for gray := 10; gray <= 100; gray += 10 {
			_ = c.SetGrayPercent(gray)
		}
	}()

	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if t.Failed() {
		t.FailNow()
	}

	// 全量校验：每个键都能读回最后写入的值。
	for w := 0; w < workers; w++ {
		for i := 0; i < keysPerWorker; i++ {
			key := fmt.Sprintf("w%d-key-%d", w, i)
			want := fmt.Sprintf("w%d-val-%d", w, i)
			if got, ok, _ := c.Read(key); !ok || got != want {
				t.Fatalf("final check: key %s got (%q,%v), want %q", key, got, ok, want)
			}
		}
	}
	t.Logf("concurrent migration verified: %d keys, 0 lost", workers*keysPerWorker)
}

// TestMetricsAccounting 验证请求统计准确。
func TestMetricsAccounting(t *testing.T) {
	oldTopo, newTopo := buildTopologies(t)
	c := NewController(oldTopo)
	c.PublishNewTopology(newTopo)
	if err := c.SetGrayPercent(50); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1_000; i++ {
		c.Write(fmt.Sprintf("key-%d", i), "v")
	}
	for i := 0; i < 1_000; i++ {
		c.Read(fmt.Sprintf("key-%d", i))
	}
	m := c.Metrics()
	if m.Writes != 1_000 || m.Reads != 1_000 {
		t.Fatalf("metrics: writes=%d reads=%d, want 1000/1000", m.Writes, m.Reads)
	}
	if m.OldRouted+m.NewRouted != 2_000 {
		t.Fatalf("metrics: routed total=%d, want 2000", m.OldRouted+m.NewRouted)
	}
}
