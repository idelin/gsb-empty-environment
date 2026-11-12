package release

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
)

// fakeAgent 是可控区域代理：可注入准备/提交失败、丢失确认、
// 旧版回执以及“崩溃”（取消 ctx 模拟控制器中途退出）。
type fakeAgent struct {
	mu sync.Mutex

	region string

	failPrepare    int  // 接下来 N 次 Prepare 返回错误（不产生副作用）
	failCommit     int  // 接下来 N 次 Commit 返回错误（不产生副作用）
	dropCommitAcks int  // 接下来 N 次 Commit 实际生效但丢失确认（返回错误）
	stale          bool // 回执携带旧版本号与旧摘要
	staleCommit    bool // 仅 Commit 回执携带旧版本号与旧摘要
	oldVersionID   string
	cancelOnCommit bool // Commit 时取消 ctx，模拟控制器崩溃
	cancel         context.CancelFunc

	prepareCalls int
	commitCalls  int
	abortCalls   int

	prepared  map[string]bool // 区域侧真实状态：版本 -> 已准备
	committed map[string]bool // 区域侧真实状态：版本 -> 已生效
	seq       uint64
}

func newFakeAgent(region string) *fakeAgent {
	return &fakeAgent{
		region:    region,
		prepared:  make(map[string]bool),
		committed: make(map[string]bool),
	}
}

func (f *fakeAgent) receipt(versionID, digest string) Receipt {
	f.seq++
	if f.stale {
		return Receipt{Region: f.region, VersionID: f.oldVersionID, Digest: "stale-digest", Seq: f.seq}
	}
	return Receipt{Region: f.region, VersionID: versionID, Digest: digest, Seq: f.seq}
}

func (f *fakeAgent) Prepare(ctx context.Context, v Version) (Receipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prepareCalls++
	if f.failPrepare > 0 {
		f.failPrepare--
		return Receipt{}, errors.New("prepare failed")
	}
	// 幂等：重复准备同一版本不产生额外副作用。
	f.prepared[v.ID] = true
	return f.receipt(v.ID, v.Digest), nil
}

func (f *fakeAgent) Commit(ctx context.Context, versionID string) (Receipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commitCalls++
	if f.cancelOnCommit && f.cancel != nil {
		f.cancelOnCommit = false
		f.cancel() // 模拟控制器在提交阶段崩溃
		return Receipt{}, ctx.Err()
	}
	if f.failCommit > 0 {
		f.failCommit--
		return Receipt{}, errors.New("commit failed")
	}
	if f.dropCommitAcks > 0 && !f.committed[versionID] {
		// 提交实际生效，但确认丢失：控制器稍后会重试并收到重复确认。
		f.dropCommitAcks--
		f.committed[versionID] = true
		return Receipt{}, errors.New("ack lost")
	}
	digest := "digest-" + versionID
	f.committed[versionID] = true
	if f.staleCommit {
		f.seq++
		return Receipt{Region: f.region, VersionID: f.oldVersionID, Digest: "stale-digest", Seq: f.seq}, nil
	}
	return f.receipt(versionID, digest), nil
}

func (f *fakeAgent) Abort(ctx context.Context, versionID string) (Receipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.abortCalls++
	delete(f.prepared, versionID)
	return f.receipt(versionID, "digest-"+versionID), nil
}

func testVersion() Version {
	return Version{ID: "v1", Digest: "digest-v1", Regions: []string{"r1", "r2", "r3"}}
}

func newCluster(v Version) (*MemStore, map[string]Agent, map[string]*fakeAgent) {
	agents := make(map[string]Agent, len(v.Regions))
	fakes := make(map[string]*fakeAgent, len(v.Regions))
	for _, r := range v.Regions {
		fa := newFakeAgent(r)
		agents[r] = fa
		fakes[r] = fa
	}
	return NewMemStore(), agents, fakes
}

func TestReleaseAllRegionsEffective(t *testing.T) {
	v := testVersion()
	store, agents, fakes := newCluster(v)
	c := NewController(store, agents)

	rep, err := c.Release(context.Background(), v)
	if err != nil {
		t.Fatalf("Release: %v", err)
	}
	if !rep.Effective() {
		t.Fatalf("phase = %s, want effective", rep.Phase)
	}
	if len(rep.Committed) != 3 || len(rep.Pending) != 0 {
		t.Fatalf("committed=%v pending=%v", rep.Committed, rep.Pending)
	}
	for r, fa := range fakes {
		if fa.prepareCalls != 1 || fa.commitCalls != 1 {
			t.Fatalf("region %s: prepare=%d commit=%d, want 1/1", r, fa.prepareCalls, fa.commitCalls)
		}
		if fa.abortCalls != 0 {
			t.Fatalf("region %s: unexpected abort", r)
		}
	}
}

func TestPrepareFailureTriggersGlobalAbort(t *testing.T) {
	v := testVersion()
	store, agents, fakes := newCluster(v)
	fakes["r2"].failPrepare = 1
	c := NewController(store, agents)

	rep, err := c.Release(context.Background(), v)
	if err != nil {
		t.Fatalf("Release: %v", err)
	}
	if rep.Phase != PhaseAborted || !rep.Aborted {
		t.Fatalf("phase = %s, want aborted", rep.Phase)
	}
	for r, fa := range fakes {
		if fa.abortCalls != 1 {
			t.Fatalf("region %s: abort=%d, want 1", r, fa.abortCalls)
		}
		if fa.commitCalls != 0 {
			t.Fatalf("region %s: commit called after prepare failure", r)
		}
		if fa.prepared[v.ID] {
			t.Fatalf("region %s: still prepared after abort", r)
		}
	}
}

func TestStaleReceiptDuringPrepareAborts(t *testing.T) {
	v := testVersion()
	store, agents, fakes := newCluster(v)
	fakes["r3"].stale = true
	fakes["r3"].oldVersionID = "v0"
	c := NewController(store, agents)

	rep, err := c.Release(context.Background(), v)
	if err != nil {
		t.Fatalf("Release: %v", err)
	}
	if rep.Phase != PhaseAborted {
		t.Fatalf("phase = %s, want aborted (stale receipt rejected)", rep.Phase)
	}
	if rep.LastError == "" {
		t.Fatal("expected LastError to mention stale receipt")
	}
}

func TestCommitFailureReportsPartialAndConverges(t *testing.T) {
	v := testVersion()
	store, agents, fakes := newCluster(v)
	fakes["r3"].failCommit = 2 // 前两次提交失败，第三次成功
	c := NewController(store, agents)

	rep, err := c.Release(context.Background(), v)
	if err != nil {
		t.Fatalf("Release: %v", err)
	}
	if !rep.Partial() {
		t.Fatalf("phase = %s, want partially_effective", rep.Phase)
	}
	if rep.Aborted {
		t.Fatal("must never claim rollback after commit started")
	}
	if len(rep.Committed) != 2 || len(rep.Pending) != 1 || rep.Pending[0] != "r3" {
		t.Fatalf("committed=%v pending=%v", rep.Committed, rep.Pending)
	}

	// 第一轮收敛：仍失败，保持部分生效。
	rep, err = c.Converge(context.Background())
	if err != nil {
		t.Fatalf("Converge: %v", err)
	}
	if !rep.Partial() || rep.Aborted {
		t.Fatalf("phase = %s aborted=%v, want still partial", rep.Phase, rep.Aborted)
	}

	// 第二轮收敛：全部生效。
	rep, err = c.Converge(context.Background())
	if err != nil {
		t.Fatalf("Converge: %v", err)
	}
	if !rep.Effective() {
		t.Fatalf("phase = %s, want effective", rep.Phase)
	}
	// 已成功的区域不得被重复提交。
	if fakes["r1"].commitCalls != 1 || fakes["r2"].commitCalls != 1 {
		t.Fatalf("healthy regions re-committed: r1=%d r2=%d",
			fakes["r1"].commitCalls, fakes["r2"].commitCalls)
	}
}

func TestDuplicateCommitAckIsIdempotent(t *testing.T) {
	v := testVersion()
	store, agents, fakes := newCluster(v)
	fakes["r2"].dropCommitAcks = 1 // 提交生效但确认丢失
	c := NewController(store, agents)

	rep, err := c.Release(context.Background(), v)
	if err != nil {
		t.Fatalf("Release: %v", err)
	}
	if !rep.Partial() {
		t.Fatalf("phase = %s, want partial (ack lost)", rep.Phase)
	}

	// 收敛重试：区域返回同一版本的重复确认，控制器必须幂等接受。
	rep, err = c.Converge(context.Background())
	if err != nil {
		t.Fatalf("Converge: %v", err)
	}
	if !rep.Effective() {
		t.Fatalf("phase = %s, want effective", rep.Phase)
	}
	if len(rep.Committed) != 3 {
		t.Fatalf("committed=%v, want 3 unique regions", rep.Committed)
	}
	// 区域侧只生效一次，尽管控制器调用了两次 Commit。
	if got := fakes["r2"].commitCalls; got != 2 {
		t.Fatalf("r2 commit calls = %d, want 2 (retry after lost ack)", got)
	}
}

func TestStaleReceiptDuringCommitKeepsPartial(t *testing.T) {
	v := testVersion()
	store, agents, fakes := newCluster(v)
	fakes["r3"].staleCommit = true
	fakes["r3"].oldVersionID = "v0"
	c := NewController(store, agents)

	rep, err := c.Release(context.Background(), v)
	if err != nil {
		t.Fatalf("Release: %v", err)
	}
	if !rep.Partial() || rep.Aborted {
		t.Fatalf("phase = %s aborted=%v, want partial", rep.Phase, rep.Aborted)
	}

	// 区域修复后（回执恢复为当前版本），收敛完成。
	fakes["r3"].mu.Lock()
	fakes["r3"].staleCommit = false
	fakes["r3"].mu.Unlock()

	rep, err = c.Converge(context.Background())
	if err != nil {
		t.Fatalf("Converge: %v", err)
	}
	if !rep.Effective() {
		t.Fatalf("phase = %s, want effective", rep.Phase)
	}
}

func TestControllerRestartDuringPrepareAborts(t *testing.T) {
	v := testVersion()
	path := filepath.Join(t.TempDir(), "state.json")
	store := NewFileStore(path)
	_, agents, fakes := newCluster(v)

	// 第一个控制器实例：r3 准备时“崩溃”（取消 ctx）。
	ctx, cancel := context.WithCancel(context.Background())
	crash := &crashOnPrepare{fakeAgent: fakes["r3"], cancel: cancel}
	agents["r3"] = crash

	c1 := NewController(store, agents)
	_, err := c1.Release(ctx, v)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Release err = %v, want context.Canceled", err)
	}

	// 第二个控制器实例：从同一存储恢复。提交未开始 -> 安全全局撤回。
	c2 := NewController(store, agents)
	rep, err := c2.Recover(context.Background())
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if rep.Phase != PhaseAborted {
		t.Fatalf("phase = %s, want aborted after restart during prepare", rep.Phase)
	}
	if fakes["r1"].abortCalls == 0 || fakes["r2"].abortCalls == 0 {
		t.Fatal("prepared regions must receive Abort after recovery")
	}
}

// crashOnPrepare 在 Prepare 调用中取消 ctx，模拟控制器进程崩溃。
type crashOnPrepare struct {
	*fakeAgent
	cancel context.CancelFunc
	once   sync.Once
}

func (c *crashOnPrepare) Prepare(ctx context.Context, v Version) (Receipt, error) {
	c.once.Do(func() { c.cancel() })
	if ctx.Err() != nil {
		return Receipt{}, ctx.Err()
	}
	return c.fakeAgent.Prepare(ctx, v)
}

func TestControllerRestartDuringCommitConvergesForward(t *testing.T) {
	v := testVersion()
	path := filepath.Join(t.TempDir(), "state.json")
	store := NewFileStore(path)
	_, agents, fakes := newCluster(v)

	// 第一个控制器实例：提交阶段中“崩溃”。
	ctx, cancel := context.WithCancel(context.Background())
	fakes["r3"].cancelOnCommit = true
	fakes["r3"].cancel = cancel

	c1 := NewController(store, agents)
	_, err := c1.Release(ctx, v)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Release err = %v, want context.Canceled", err)
	}

	// 第二个控制器实例：恢复后只能继续收敛，绝不能回滚。
	c2 := NewController(store, agents)
	rep, err := c2.Recover(context.Background())
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if !rep.Partial() || rep.Aborted {
		t.Fatalf("phase = %s aborted=%v, want partial (no rollback after commit)", rep.Phase, rep.Aborted)
	}
	if len(rep.Committed) != 2 || len(rep.Pending) != 1 {
		t.Fatalf("committed=%v pending=%v", rep.Committed, rep.Pending)
	}

	rep, err = c2.Converge(context.Background())
	if err != nil {
		t.Fatalf("Converge: %v", err)
	}
	if !rep.Effective() {
		t.Fatalf("phase = %s, want effective", rep.Phase)
	}
	// 重启前已提交的区域不得重复提交。
	if fakes["r1"].commitCalls != 1 || fakes["r2"].commitCalls != 1 {
		t.Fatalf("re-committed after restart: r1=%d r2=%d",
			fakes["r1"].commitCalls, fakes["r2"].commitCalls)
	}
	for r, fa := range fakes {
		if fa.abortCalls != 0 {
			t.Fatalf("region %s: abort after commit started", r)
		}
	}
}

func TestRecoverWithoutStateIsIdle(t *testing.T) {
	c := NewController(NewMemStore(), map[string]Agent{})
	rep, err := c.Recover(context.Background())
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if rep.Phase != PhaseIdle {
		t.Fatalf("phase = %s, want idle", rep.Phase)
	}
}

func TestVersionValidation(t *testing.T) {
	c := NewController(NewMemStore(), map[string]Agent{"r1": newFakeAgent("r1")})
	cases := []Version{
		{ID: "", Digest: "d", Regions: []string{"r1"}},
		{ID: "v1", Digest: "", Regions: []string{"r1"}},
		{ID: "v1", Digest: "d"},
		{ID: "v1", Digest: "d", Regions: []string{"r1", "r1"}},
		{ID: "v1", Digest: "d", Regions: []string{"unknown"}},
	}
	for i, v := range cases {
		if _, err := c.Release(context.Background(), v); err == nil {
			t.Fatalf("case %d: expected validation error", i)
		}
	}
}
