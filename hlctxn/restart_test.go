package hlctxn

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

const testMaxOffset = 100 * time.Nanosecond

// newSimCluster 构造 n 节点仿真集群：trueClock 为无偏斜基准钟，
// offsets 为各节点注入的物理钟偏移（绝对值 <= maxOffset）。
func newSimCluster(t *testing.T, n int, offsets []time.Duration) (*Cluster, *SimPhysicalClock) {
	t.Helper()
	base := NewSimPhysicalClock(1000)
	phys := make([]PhysicalClock, n)
	for i := 0; i < n; i++ {
		var off time.Duration
		if i < len(offsets) {
			off = offsets[i]
		}
		phys[i] = SkewedPhysicalClock{Base: base, Offset: off}
	}
	return NewCluster(phys, base, testMaxOffset), base
}

// keyOnNode 返回哈希分片到指定节点的键。
func keyOnNode(c *Cluster, node int, prefix string) string {
	for i := 0; ; i++ {
		key := fmt.Sprintf("%s-%d", prefix, i)
		if c.nodeForKey(key) == node {
			return key
		}
	}
}

// probeKey 返回一个路由到 node 的、与 dataKey 不同的探测键，
// 用于让事务先接触节点以采样 observed[node]。
func probeKey(c *Cluster, node int, dataKey string) string {
	for i := 0; ; i++ {
		key := fmt.Sprintf("probe-%d-%d", node, i)
		if c.nodeForKey(key) == node && key != dataKey {
			return key
		}
	}
}

// TestReadRestart_CrossNodeCommittedWrite 精准验证：落在 (readTS, maxTS]
// 且高于 observed[N] 的跨节点已提交写触发一次硬重启，同节点后续读不再重启。
func TestReadRestart_CrossNodeCommittedWrite(t *testing.T) {
	// 零偏斜集群：节点 HLC 差异完全来自真实时间先后，可确定性构造窗口。
	c, base := newSimCluster(t, 3, nil)
	dataKey := keyOnNode(c, 2, "data")
	probe := probeKey(c, 2, dataKey)

	// t=1000：T2 在 N0 启动，readTS=1000，maxTS=1100；接触 N2 采样 observed=1000。
	reader := c.Begin(0, 0)
	readTS0 := reader.ReadTS()
	maxTS0 := reader.MaxTS()
	if _, ok, err := reader.Get(probe); err != nil || ok {
		t.Fatalf("probe read failed: ok=%v err=%v", ok, err)
	}
	obsBefore, _ := reader.Observed(2)

	// t=1050：N2 上 T1 提交，commitTS=1050 ∈ (readTS=1000, maxTS=1100]
	// 且 1050 > observed[N2]=1000，构成真正的读不确定性。
	base.Advance(50 * time.Nanosecond)
	writer := c.Begin(2, 0)
	if err := writer.Put(dataKey, "v1"); err != nil {
		t.Fatalf("put: %v", err)
	}
	commitTS, err := writer.Commit()
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if !readTS0.Less(commitTS) || !commitTS.LessEq(maxTS0) || !commitTS.Greater(obsBefore) {
		t.Fatalf("window setup wrong: readTS=%+v maxTS=%+v obs=%+v commit=%+v",
			readTS0, maxTS0, obsBefore, commitTS)
	}

	// 第一次读必须精准触发 ReadRestartError，且 maxTS 保持不变。
	_, _, err = reader.Get(dataKey)
	var rr *ReadRestartError
	if !errors.As(err, &rr) {
		t.Fatalf("expected ReadRestartError, got %v", err)
	}
	if rr.Node != 2 || rr.NewReadTS.Compare(commitTS) != 0 {
		t.Fatalf("restart payload wrong: %+v", rr)
	}
	if rr.MaxTS.Compare(maxTS0) != 0 || reader.MaxTS().Compare(maxTS0) != 0 {
		t.Fatal("maxTS must remain fixed across restart")
	}
	if reader.ReadTS().Compare(commitTS) != 0 {
		t.Fatalf("readTS not bumped to v.ts: %+v", reader.ReadTS())
	}

	// 新时间戳下重读成功，看到 T1 的写；同节点后续读不再重启（不死循环）。
	value, found, err := reader.Get(dataKey)
	if err != nil || !found || value != "v1" {
		t.Fatalf("reread value=%q found=%v err=%v", value, found, err)
	}
	for i := 0; i < 10; i++ {
		if value, found, err = reader.Get(dataKey); err != nil || !found || value != "v1" {
			t.Fatalf("repeat read %d unstable: %q %v %v", i, value, found, err)
		}
	}

	stats := c.StatsSnapshot()
	if stats.ReadRestarts != 1 {
		t.Fatalf("expected exactly 1 hard restart, got %d", stats.ReadRestarts)
	}
	if _, err := reader.Commit(); err != nil {
		t.Fatalf("reader commit: %v", err)
	}
}

// TestReadRestart_SkewedClocksStillBounded 注入满幅时钟偏斜，
// 验证偏斜 <= maxOffset 时读路径要么本地前推、要么硬重启，永不越界或死循环。
func TestReadRestart_SkewedClocksStillBounded(t *testing.T) {
	c, base := newSimCluster(t, 3, []time.Duration{
		-100 * time.Nanosecond, 0, 100 * time.Nanosecond,
	})
	dataKey := keyOnNode(c, 2, "skew")

	base.Advance(30 * time.Nanosecond)
	reader := c.Begin(0, 0)
	base.Advance(20 * time.Nanosecond)
	writer := c.Begin(2, 0)
	if err := writer.Put(dataKey, "skewed"); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Commit(); err != nil {
		t.Fatal(err)
	}

	value, found, restarts, err := reader.GetWithRestart(dataKey)
	if err != nil {
		t.Fatalf("skewed read: %v", err)
	}
	if found && value != "skewed" {
		t.Fatalf("unexpected value %q", value)
	}
	if restarts > 1 {
		t.Fatalf("same node restarted %d times, must be <= 1", restarts)
	}
	if reader.MaxTS().Compare(reader.ReadTS().Add(testMaxOffset.Nanoseconds())) > 0 {
		t.Fatal("readTS drifted beyond original uncertainty window")
	}
}

func TestReadRestart_VersionBeyondMaxTSIsFuture(t *testing.T) {
	c, base := newSimCluster(t, 2, nil)
	// 数据键放在协调节点 N0 上：事务 Begin 即采样 observed[N0]=readTS，
	// 且之后不接触任何其他节点，保证没有任何 observed 上界能放宽窗口。
	dataKey := keyOnNode(c, 0, "future")

	reader := c.Begin(0, 0)
	readTS := reader.ReadTS()

	// 推进超过 maxOffset 窗口后的提交戳严格 > reader.maxTS：
	// 属确定性未来，读旧值（不存在）且不得触发重启。
	base.Advance(2 * testMaxOffset)
	writer := c.Begin(1, 0)
	if err := writer.Put(dataKey, "future-value"); err != nil {
		t.Fatal(err)
	}
	commitTS, _ := writer.Commit()
	if !commitTS.Greater(reader.MaxTS()) {
		t.Fatalf("setup: %+v should exceed maxTS %+v", commitTS, reader.MaxTS())
	}

	_, found, err := reader.Get(dataKey)
	if err != nil || found {
		t.Fatalf("future version must be invisible: found=%v err=%v", found, err)
	}
	if reader.ReadTS().Compare(readTS) != 0 {
		t.Fatalf("readTS must not move on future version: %+v", reader.ReadTS())
	}
	if c.StatsSnapshot().ReadRestarts != 0 {
		t.Fatal("future version must not trigger restart")
	}

	// 对照：一个新事务接触 N0 后，observed[N0] 已因果前进到 >= commitTS，
	// 同一版本经本地前推立即可见——这正是外部一致性的 observed 规则。
	later := c.Begin(0, 0)
	value, found, err := later.Get(dataKey)
	if err != nil || !found || value != "future-value" {
		t.Fatalf("observed-bounded read should see version: %q %v %v", value, found, err)
	}
	if c.StatsSnapshot().ReadRestarts != 0 {
		t.Fatal("observed-bounded read must not hard-restart")
	}
}

// TestLocalForward_NoRestartWithinObservedBound 验证 observed 上界内的
// 不确定性版本只静默前推 readTS，不产生 ReadRestartError。
func TestLocalForward_NoRestartWithinObservedBound(t *testing.T) {
	// 用偏斜钟让协调节点 HLC 落后：新事务 readTS 落后于 commitTS，
	// 但首次接触存储节点时 observed 已 >= commitTS，应只做本地前推。
	c2, base2 := newSimCluster(t, 2, []time.Duration{-50 * time.Nanosecond, 0})
	key2 := keyOnNode(c2, 1, "lf")
	base2.Advance(50 * time.Nanosecond) // t=1050: N1 HLC=1050
	w2 := c2.Begin(1, 0)
	if err := w2.Put(key2, "lf-value"); err != nil {
		t.Fatal(err)
	}
	c2Commit, _ := w2.Commit() // commitTS=1050

	base2.Advance(10 * time.Nanosecond) // t=1060: N0 HLC=1010, N1 HLC=1060
	r2 := c2.Begin(0, 0)                // readTS=1010, maxTS=1110
	probe2 := probeKey(c2, 1, key2)
	if _, ok, err := r2.Get(probe2); err != nil || ok {
		t.Fatalf("probe2: %v %v", ok, err)
	}
	obs2, _ := r2.Observed(1) // 1060 >= commitTS=1050
	if !r2.ReadTS().Less(c2Commit) || !c2Commit.LessEq(obs2) {
		t.Fatalf("setup: need readTS=%+v < commit=%+v <= observed=%+v",
			r2.ReadTS(), c2Commit, obs2)
	}
	value, found, err := r2.Get(key2)
	if err != nil || !found || value != "lf-value" {
		t.Fatalf("local-forward read: value=%q found=%v err=%v", value, found, err)
	}
	if c2.StatsSnapshot().ReadRestarts != 0 {
		t.Fatal("commit within observed bound must not hard-restart")
	}
	if c2.StatsSnapshot().LocalForwards == 0 || r2.ReadTS().Less(c2Commit) {
		t.Fatalf("readTS should have been locally forwarded to >= %+v, got %+v",
			c2Commit, r2.ReadTS())
	}
}

func TestGetWithRestart_AutomaticRetry(t *testing.T) {
	c, base := newSimCluster(t, 3, nil)
	dataKey := keyOnNode(c, 2, "auto")

	reader := c.Begin(0, 0) // readTS=1000, maxTS=1100
	probe := probeKey(c, 2, dataKey)
	if _, ok, err := reader.Get(probe); err != nil || ok {
		t.Fatalf("probe: %v %v", ok, err)
	}
	base.Advance(60 * time.Nanosecond) // t=1060
	writer := c.Begin(2, 0)
	_ = writer.Put(dataKey, "auto-v")
	commitTS, _ := writer.Commit()
	if !commitTS.Greater(reader.ReadTS()) || !commitTS.LessEq(reader.MaxTS()) {
		t.Fatalf("setup: commitTS %+v outside (readTS=%+v, maxTS=%+v]",
			commitTS, reader.ReadTS(), reader.MaxTS())
	}

	value, found, restarts, err := reader.GetWithRestart(dataKey)
	if err != nil || !found || value != "auto-v" || restarts != 1 {
		t.Fatalf("auto retry: value=%q found=%v restarts=%d err=%v",
			value, found, restarts, err)
	}
	if reader.ReadTS().Compare(commitTS) != 0 {
		t.Fatal("retry left readTS inconsistent")
	}
}
