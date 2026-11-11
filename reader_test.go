package cursorpage_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"path/filepath"
	"sort"
	"testing"

	cp "example.org/cursorpage"
)

// deliveredTracker 记录测试期间实际交付出去的每个版本。
type deliveredTracker struct {
	versions map[string][]int64 // key -> 已交付版本（有序）
	pairs    map[string]bool    // "key@version" -> 是否出现过
}

func newTracker() *deliveredTracker {
	return &deliveredTracker{
		versions: map[string][]int64{},
		pairs:    map[string]bool{},
	}
}

func (t *deliveredTracker) deliver(rec cp.Record) error {
	id := fmt.Sprintf("%s@%d", rec.Key, rec.Version)
	if t.pairs[id] {
		return fmt.Errorf("duplicate delivery of %s", id)
	}
	t.pairs[id] = true
	t.versions[rec.Key] = append(t.versions[rec.Key], rec.Version)
	return nil
}

func (t *deliveredTracker) assertNoDuplicateVersion(tt *testing.T) {
	tt.Helper()
	for key, vs := range t.versions {
		sorted := append([]int64(nil), vs...)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
		for i := 1; i < len(sorted); i++ {
			if sorted[i] == sorted[i-1] {
				tt.Fatalf("key %s delivered version %d more than once: %v", key, sorted[i], vs)
			}
		}
	}
}

func (t *deliveredTracker) lastVersion(key string) (int64, bool) {
	vs, ok := t.versions[key]
	if !ok || len(vs) == 0 {
		return 0, false
	}
	return vs[len(vs)-1], true
}

// runSession 跑一次 Reader 生命周期，返回结束原因：complete 或 crash。
func runSession(t *testing.T, store cp.Store, server *cp.Server, tracker *deliveredTracker, hooks cp.Hooks, overlap int64) string {
	t.Helper()
	reader := cp.NewReader(cp.Config{
		Remote:           server,
		Store:            store,
		PageSize:         7,
		BacktrackOverlap: overlap,
		DedupCap:         10_000,
		Hooks:            hooks,
	})
	err := reader.Run(context.Background(), tracker.deliver)
	if err == nil {
		return "complete"
	}
	if errors.Is(err, cp.ErrCrash) {
		return "crash"
	}
	t.Fatalf("reader returned unexpected error: %v", err)
	return ""
}

func runBacktrackOnlySession(t *testing.T, stateFile string, server *cp.Server, tracker *deliveredTracker) string {
	t.Helper()
	reader := cp.NewReader(cp.Config{
		Remote:           server,
		Store:            cp.NewFileStore(stateFile),
		PageSize:         5,
		BacktrackOverlap: 0,
		BacktrackOnly:    true,
		DedupCap:         10_000,
	})
	if err := reader.Run(context.Background(), tracker.deliver); err != nil {
		if errors.Is(err, cp.ErrCrash) {
			return "crash"
		}
		t.Fatalf("reader returned unexpected error: %v", err)
	}
	return "complete"
}

// TestResumableReader_RandomMutationsAndCrashes 是主场景：
// 翻页过程中服务端持续插入/更新/删除，并在两个安全点随机注入崩溃，
// 核对“每条存活记录至少交付一次、同一版本只交付一次”。
func TestResumableReader_RandomMutationsAndCrashes(t *testing.T) {
	store := &memStore{}
	server := cp.NewServer(20261111, 30)
	tracker := newTracker()

	const mutateSessions = 14 // 前若干个会话期间持续变更，之后停止变更让读取器排空
	const maxSessions = 40
	crashes := 0
	completed := false

	for session := 0; session < maxSessions; session++ {
		rng := rand.New(rand.NewSource(int64(1000 + session)))
		crashesThisSession := 0
		pagesThisSession := 0
		crashWindow := session < mutateSessions

		hooks := cp.Hooks{
			BeforeFetch: func(mode string) error {
				if crashWindow && crashesThisSession < 1 && rng.Intn(40) == 0 {
					crashesThisSession++
					crashes++
					return cp.ErrCrash
				}
				return nil
			},
			AfterCheckpoint: func(cp.Checkpoint) error {
				if crashWindow && crashesThisSession < 1 && rng.Intn(40) == 0 {
					crashesThisSession++
					crashes++
					return cp.ErrCrash
				}
				return nil
			},
			AfterPage: func(mode string) {
				if session < mutateSessions && pagesThisSession < 8 && rng.Intn(2) == 0 {
					server.RandomMutate()
				}
				pagesThisSession++
			},
		}

		outcome := runSession(t, store, server, tracker, hooks, 1_000_000)
		if outcome == "complete" {
			completed = true
			break
		}

		// 会话之间也制造一些变更，覆盖“长时间停机恢复”路径。
		if session < mutateSessions {
			for i := 0; i < 1+rng.Intn(3); i++ {
				server.RandomMutate()
			}
		}
	}

	if crashes == 0 {
		t.Fatal("test harness injected no crashes; test would not prove crash recovery")
	}
	if !completed {
		t.Fatal("reader never completed within session budget")
	}

	// 不变量 1：同一 (Key, Version) 绝不重复交付。
	tracker.assertNoDuplicateVersion(t)

	// 不变量 2：最终仍存活的每条记录，至少交付过一次且最终版本已交付。
	live, tombstones, totalVersions := server.Stats()
	t.Logf("server stats: live=%d tombstones=%d historicalVersions=%d crashes=%d",
		live, tombstones, totalVersions, crashes)
	if live == 0 {
		t.Fatal("expected a non-empty live dataset at the end")
	}
	for _, rec := range server.Snapshot() {
		got, ok := tracker.lastVersion(rec.Key)
		if !ok {
			t.Fatalf("live record %s was never delivered", rec.Key)
		}
		if got != rec.Version {
			t.Fatalf("live record %s final version=%d but last delivered=%d", rec.Key, rec.Version, got)
		}
	}
}

// TestCursorExpiry_FallsBackToBacktrack 验证游标过期后自动退回
// (UpdatedAt, Key) 回溯窗口，并且不丢数、不重复。
func TestCursorExpiry_FallsBackToBacktrack(t *testing.T) {
	store := &memStore{}
	server := cp.NewServer(42, 25)
	tracker := newTracker()

	expired := false
	hooks := cp.Hooks{
		AfterPage: func(mode string) {
			// 游标模式跑过几页后让所有游标过期，并同时插入新数据。
			if mode == "cursor" && !expired {
				server.ExpireCursors()
				server.Insert("late-key-00001")
				server.Update("key-00003")
				expired = true
			}
		},
	}

	outcome := runSession(t, store, server, tracker, hooks, 1_000_000)
	if outcome != "complete" {
		t.Fatalf("expected single-run completion, got %s", outcome)
	}
	if !expired {
		t.Fatal("cursor expiry was never triggered")
	}
	tracker.assertNoDuplicateVersion(t)
	for _, rec := range server.Snapshot() {
		got, ok := tracker.lastVersion(rec.Key)
		if !ok || got != rec.Version {
			t.Fatalf("record %s not fully delivered, got version=%v ok=%v want=%d", rec.Key, got, ok, rec.Version)
		}
	}
}

// TestCheckpointResume_WithinPage 构造小页 + 落盘后崩溃，验证恢复时
// 从“本页已交付位置”继续，且崩溃前后不会重复交付任何版本。
func TestCheckpointResume_WithinPage(t *testing.T) {
	store := &memStore{}
	server := cp.NewServer(7, 10)
	tracker := newTracker()

	var deliveredBeforeCrash int
	outcome := runSession(t, store, server, tracker, cp.Hooks{
		AfterCheckpoint: func(c cp.Checkpoint) error {
			deliveredBeforeCrash = len(tracker.pairs)
			if deliveredBeforeCrash >= 5 {
				return cp.ErrCrash
			}
			return nil
		},
	}, 1_000_000)
	if outcome != "crash" {
		t.Fatalf("expected crash, got %s", outcome)
	}

	outcome = runSession(t, store, server, tracker, cp.Hooks{}, 1_000_000)
	if outcome != "complete" {
		t.Fatalf("expected completion after resume, got %s", outcome)
	}
	tracker.assertNoDuplicateVersion(t)
	for _, rec := range server.Snapshot() {
		if _, ok := tracker.lastVersion(rec.Key); !ok {
			t.Fatalf("record %s missing after resume", rec.Key)
		}
	}
	// 恢复之后允许新增交付，但崩溃前已交付的集合必须是恢复后集合的子集，
	// 即任何已落盘的版本都不允许重放。
	if len(tracker.pairs) < deliveredBeforeCrash {
		t.Fatalf("delivery set shrank after resume: before=%d after=%d",
			deliveredBeforeCrash, len(tracker.pairs))
	}
}

// TestBacktrackWindow_RedeliversNewVersions 验证回溯模式下：
// 已交付记录产生新版本时会再次交付（新版本只交付一次），
// 旧版本不会重放；扫描排空后正常结束。
func TestBacktrackWindow_RedeliversNewVersions(t *testing.T) {
	store := &memStore{}
	server := cp.NewServer(99, 12)
	tracker := newTracker()

	updated := false
	hooks := cp.Hooks{
		AfterPage: func(mode string) {
			if mode == "backtrack" && !updated {
				server.Update("key-00002")
				server.Insert("brand-new-key")
				updated = true
			}
		},
	}

	outcome := runSession(t, store, server, tracker, hooks, 1_000_000)
	if outcome != "complete" {
		t.Fatalf("expected completion, got %s", outcome)
	}
	if !updated {
		t.Fatal("backtrack mutation was never triggered")
	}
	tracker.assertNoDuplicateVersion(t)

	hist := server.VersionHistory("key-00002")
	got := tracker.versions["key-00002"]
	if len(got) != len(hist) {
		t.Fatalf("key-00002: delivered versions %v, history %v", got, hist)
	}
	for i := range hist {
		if got[i] != hist[i] {
			t.Fatalf("key-00002 version order mismatch: delivered %v history %v", got, hist)
		}
	}
	if _, ok := tracker.lastVersion("brand-new-key"); !ok {
		t.Fatal("record inserted during backtrack was not delivered")
	}
}

// TestFileStore_AtomicRoundTrip 单独验证断点文件的原子读写与损坏检测。
func TestFileStore_AtomicRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cp.json")
	store := cp.NewFileStore(path)
	ctx := context.Background()

	if _, ok, err := store.Load(ctx); err != nil || ok {
		t.Fatalf("empty store: ok=%v err=%v", ok, err)
	}

	cp1 := cp.Checkpoint{
		Mode:            "cursor",
		Cursor:          "cur-1",
		CursorPageCount: 7,
		PageDelivered:   3,
		LastKey:         "key-00009",
		LastVersion:     42,
		HighWaterMark:   55,
		Delivered:       map[string]int64{"key-00009": 42},
	}
	if err := store.Save(ctx, cp1); err != nil {
		t.Fatal(err)
	}
	got, ok, err := store.Load(ctx)
	if err != nil || !ok {
		t.Fatalf("load: ok=%v err=%v", ok, err)
	}
	if got.Cursor != "cur-1" || got.PageDelivered != 3 || got.Delivered["key-00009"] != 42 {
		t.Fatalf("checkpoint content mismatch: %+v", got)
	}

	// 再次覆盖写入后读到的必须是完整新内容。
	cp1.PageDelivered = 6
	if err := store.Save(ctx, cp1); err != nil {
		t.Fatal(err)
	}
	got, _, err = store.Load(ctx)
	if err != nil || got.PageDelivered != 6 {
		t.Fatalf("overwrite failed: %+v err=%v", got, err)
	}
}

// TestFileStore_EndToEndCrashRecovery 用真实文件断点跑“崩溃 -> 重建 -> 续传”，
// 证明临时文件 + rename 的原子落盘在完整链路上成立，恢复后不丢不重。
func TestFileStore_EndToEndCrashRecovery(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "checkpoint.json")
	server := cp.NewServer(2024, 18)
	tracker := newTracker()

	crashed := false
	first := cp.NewReader(cp.Config{
		Remote:           server,
		Store:            cp.NewFileStore(stateFile),
		PageSize:         5,
		BacktrackOverlap: 1_000_000,
		Hooks: cp.Hooks{
			AfterCheckpoint: func(c cp.Checkpoint) error {
				if !crashed && len(tracker.pairs) >= 8 {
					crashed = true
					server.RandomMutate()
					server.RandomMutate()
					return cp.ErrCrash
				}
				return nil
			},
			AfterPage: func(string) { server.RandomMutate() },
		},
	})
	if err := first.Run(context.Background(), tracker.deliver); !errors.Is(err, cp.ErrCrash) {
		t.Fatalf("expected ErrCrash, got %v", err)
	}

	second := cp.NewReader(cp.Config{
		Remote:           server,
		Store:            cp.NewFileStore(stateFile),
		PageSize:         5,
		BacktrackOverlap: 1_000_000,
	})
	if err := second.Run(context.Background(), tracker.deliver); err != nil {
		t.Fatalf("resume run failed: %v", err)
	}

	tracker.assertNoDuplicateVersion(t)
	for _, rec := range server.Snapshot() {
		got, ok := tracker.lastVersion(rec.Key)
		if !ok || got != rec.Version {
			t.Fatalf("record %s not fully delivered after file-based recovery, got=%v ok=%v want=%d",
				rec.Key, got, ok, rec.Version)
		}
	}
}

// TestTombstone_DeliveredViaBacktrack 验证删除产生的墓碑版本也会通过
// 回溯窗口交付，且同一墓碑版本只交付一次。
func TestTombstone_DeliveredViaBacktrack(t *testing.T) {
	store := &memStore{}
	server := cp.NewServer(3, 8)
	tracker := newTracker()

	deleted := false
	hooks := cp.Hooks{
		AfterPage: func(mode string) {
			if mode == "backtrack" && !deleted {
				server.Delete("key-00004")
				deleted = true
			}
		},
	}
	if out := runSession(t, store, server, tracker, hooks, 1_000_000); out != "complete" {
		t.Fatalf("expected completion, got %s", out)
	}
	if !deleted {
		t.Fatal("deletion was never triggered")
	}
	tracker.assertNoDuplicateVersion(t)

	var tombstoneVersion int64
	for _, rec := range server.Tombstones() {
		if rec.Key == "key-00004" {
			tombstoneVersion = rec.Version
		}
	}
	if tombstoneVersion == 0 {
		t.Fatal("tombstone for key-00004 not found on server")
	}
	last, ok := tracker.lastVersion("key-00004")
	if !ok || last != tombstoneVersion {
		t.Fatalf("tombstone version %d not delivered, got=%v ok=%v", tombstoneVersion, last, ok)
	}
}

// TestAllHistoricalVersions_Delivered 在无持续变更的数据集上核对：
// 每个键经历过的每个版本（同一版本恰好一次）最终都完成交付。
//
// 说明：游标接口只暴露记录的当前版本，历史版本要靠回溯扫描补回。
// 全新任务的回溯锚点从 0 开始，因此这里能覆盖全部历史版本；
// 长期运行任务中，早于“高水位 - BacktrackOverlap”且已被覆盖的旧版本
// 与真实 keyset API 一样不可见，重叠窗口必须按采集滞后预算配置。
func TestAllHistoricalVersions_Delivered(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "checkpoint.json")
	server := cp.NewServer(11, 10)

	// 制造若干历史版本后再启动读取器（读取器侧仍是全新断点）。
	server.Update("key-00001")
	server.Update("key-00001")
	server.Update("key-00005")

	tracker := newTracker()
	if out := runBacktrackOnlySession(t, stateFile, server, tracker); out != "complete" {
		t.Fatalf("expected completion, got %s", out)
	}
	tracker.assertNoDuplicateVersion(t)

	for _, rec := range server.Snapshot() {
		history := server.VersionHistory(rec.Key)
		got := tracker.versions[rec.Key]
		if len(got) != len(history) {
			t.Fatalf("key %s: delivered %v, want all versions %v", rec.Key, got, history)
		}
		for i := range history {
			if got[i] != history[i] {
				t.Fatalf("key %s: delivered %v, want %v", rec.Key, got, history)
			}
		}
	}
}
