package netsec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// brokenCounter 模拟可信计数器不可用（硬件掉线/无法访问）。
type brokenCounter struct{}

func (brokenCounter) Value(context.Context) (uint64, error) {
	return 0, io.ErrUnexpectedEOF
}

func (brokenCounter) Increment(context.Context) (uint64, error) {
	return 0, io.ErrUnexpectedEOF
}

// staticSigner 对每个产物返回确定性签名。
type staticSigner struct{}

func (staticSigner) Sign(_ context.Context, artifact []byte) ([]byte, error) {
	return append([]byte("sig:"), artifact...), nil
}

// timeoutSigner 模拟签名器超时/取消。每次调用都消耗额度且不产生回执。
type timeoutSigner struct{}

func (timeoutSigner) Sign(context.Context, []byte) ([]byte, error) {
	return nil, context.DeadlineExceeded
}

// flakySigner 前 fail 次调用失败，之后成功；用于验证失败也不退额。
type flakySigner struct {
	fail int
}

func (s *flakySigner) Sign(_ context.Context, artifact []byte) ([]byte, error) {
	if s.fail > 0 {
		s.fail--
		return nil, errors.New("signer backend failure")
	}
	return append([]byte("sig:"), artifact...), nil
}

func newTestHost(t *testing.T, counter Counter, signer Signer, limit uint64) (*Host, *FileStore) {
	t.Helper()
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return NewHost(counter, signer, store, limit), store
}

// cloneDir 把普通存储目录整体克隆一份，模拟节点磁盘快照/克隆。
func cloneDir(t *testing.T, src string) string {
	t.Helper()
	dst := t.TempDir()
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(src, entry.Name()))
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dst, entry.Name()), data, 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
	return dst
}

func reopenStore(t *testing.T, dir string) *FileStore {
	t.Helper()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("reopen NewFileStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestCounterMonotonic(t *testing.T) {
	counter := NewMemoryCounter()
	ctx := context.Background()

	var prev uint64
	for i := 0; i < 100; i++ {
		v, err := counter.Increment(ctx)
		if err != nil {
			t.Fatalf("Increment: %v", err)
		}
		if v <= prev {
			t.Fatalf("counter not monotonic: got %d after %d", v, prev)
		}
		prev = v
	}

	got, err := counter.Value(ctx)
	if err != nil {
		t.Fatalf("Value: %v", err)
	}
	if got != 100 {
		t.Fatalf("Value = %d, want 100", got)
	}

	// 已取消的上下文必须被拒绝，且不能推进计数。
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := counter.Increment(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Increment err = %v, want context.Canceled", err)
	}
	if got, _ := counter.Value(ctx); got != 100 {
		t.Fatalf("counter advanced on rejected call: %d", got)
	}
}

func TestIssueConsumesBeforeSign(t *testing.T) {
	counter := NewMemoryCounter()
	host, store := newTestHost(t, counter, staticSigner{}, 10)

	receipt, err := host.Issue(context.Background(), []byte("artifact-1"))
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if receipt.Seq != 1 {
		t.Fatalf("receipt Seq = %d, want 1", receipt.Seq)
	}
	gotArtifact, err := DecodeArtifact(receipt.Artifact)
	if err != nil || !bytes.Equal(gotArtifact, []byte("artifact-1")) {
		t.Fatalf("artifact round trip = %q, %v", receipt.Artifact, err)
	}

	value, _ := counter.Value(context.Background())
	if value != 1 {
		t.Fatalf("counter after one issue = %d, want 1", value)
	}
	receipts, err := store.List()
	if err != nil || len(receipts) != 1 || receipts[0].Seq != 1 {
		t.Fatalf("store state = %v (err %v), want one receipt with seq 1", receipts, err)
	}

	remaining, err := host.Remaining(context.Background())
	if err != nil || remaining != 9 {
		t.Fatalf("Remaining = %d, %v, want 9", remaining, err)
	}
}

func TestQuotaExhaustion(t *testing.T) {
	const limit uint64 = 5
	counter := NewMemoryCounter()
	host, store := newTestHost(t, counter, staticSigner{}, limit)

	for i := uint64(1); i <= limit; i++ {
		receipt, err := host.Issue(context.Background(), []byte(fmt.Sprintf("a%d", i)))
		if err != nil {
			t.Fatalf("Issue %d: %v", i, err)
		}
		if receipt.Seq != i {
			t.Fatalf("receipt Seq = %d, want %d", receipt.Seq, i)
		}
	}

	// 额度用尽后的每次尝试都被拒绝。
	for i := 0; i < 3; i++ {
		if _, err := host.Issue(context.Background(), []byte("overflow")); !errors.Is(err, ErrQuotaExhausted) {
			t.Fatalf("overflow issue err = %v, want ErrQuotaExhausted", err)
		}
	}

	value, _ := counter.Value(context.Background())
	if value != limit+3 {
		t.Fatalf("counter = %d, want %d (exhaustion attempts still consume, no refund)", value, limit+3)
	}
	receipts, _ := store.List()
	if len(receipts) != int(limit) {
		t.Fatalf("stored receipts = %d, want %d", len(receipts), limit)
	}
	if remaining, err := host.Remaining(context.Background()); err != nil || remaining != 0 {
		t.Fatalf("Remaining = %d, %v, want 0", remaining, err)
	}
}

func TestZeroQuota(t *testing.T) {
	counter := NewMemoryCounter()
	host, store := newTestHost(t, counter, staticSigner{}, 0)

	if _, err := host.Issue(context.Background(), []byte("x")); !errors.Is(err, ErrQuotaExhausted) {
		t.Fatalf("zero-quota issue err = %v, want ErrQuotaExhausted", err)
	}
	value, _ := counter.Value(context.Background())
	if value != 1 {
		t.Fatalf("counter = %d, want 1: consume happens before quota check", value)
	}
	receipts, _ := store.List()
	if len(receipts) != 0 {
		t.Fatalf("zero-quota node stored %d receipts, want 0", len(receipts))
	}
}

func TestConcurrentIssuance(t *testing.T) {
	const limit uint64 = 50
	counter := NewMemoryCounter()
	host, store := newTestHost(t, counter, staticSigner{}, limit)

	const goroutines = 16
	const perGoroutine = 8

	var wg sync.WaitGroup
	var successMu sync.Mutex
	successes := 0
	start := make(chan struct{})

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			<-start
			for j := 0; j < perGoroutine; j++ {
				_, err := host.Issue(context.Background(), []byte(fmt.Sprintf("g%d-j%d", id, j)))
				if err == nil {
					successMu.Lock()
					successes++
					successMu.Unlock()
				} else if !errors.Is(err, ErrQuotaExhausted) {
					t.Errorf("unexpected issue error: %v", err)
					return
				}
			}
		}(g)
	}
	close(start)
	wg.Wait()

	if successes != int(limit) {
		t.Fatalf("successful issues = %d, want exactly %d", successes, limit)
	}

	value, _ := counter.Value(context.Background())
	if value != uint64(goroutines*perGoroutine) {
		t.Fatalf("counter = %d, want %d attempts", value, goroutines*perGoroutine)
	}

	receipts, err := store.List()
	if err != nil {
		t.Fatalf("store.List: %v", err)
	}
	if len(receipts) != int(limit) {
		t.Fatalf("receipts = %d, want %d", len(receipts), limit)
	}

	seen := make(map[uint64]bool, limit)
	for _, receipt := range receipts {
		if receipt.Seq < 1 || receipt.Seq > limit {
			t.Fatalf("receipt seq %d outside [1,%d]", receipt.Seq, limit)
		}
		if seen[receipt.Seq] {
			t.Fatalf("duplicate seq %d in store", receipt.Seq)
		}
		seen[receipt.Seq] = true
	}

	// 额度已尽，再来的请求必须被拒绝。
	if _, err := host.Issue(context.Background(), []byte("late")); !errors.Is(err, ErrQuotaExhausted) {
		t.Fatalf("post-drain issue err = %v, want ErrQuotaExhausted", err)
	}
}

func TestCounterUnavailableRefusesService(t *testing.T) {
	host, store := newTestHost(t, brokenCounter{}, staticSigner{}, 10)

	receipt, err := host.Issue(context.Background(), []byte("artifact"))
	if !errors.Is(err, ErrCounterUnavailable) {
		t.Fatalf("Issue err = %v, want ErrCounterUnavailable", err)
	}
	if receipt != (Receipt{}) {
		t.Fatalf("receipt should be zero value, got %+v", receipt)
	}
	if remaining, err := host.Remaining(context.Background()); !errors.Is(err, ErrCounterUnavailable) || remaining != 0 {
		t.Fatalf("Remaining = %d, %v, want 0 with ErrCounterUnavailable", remaining, err)
	}
	receipts, _ := store.List()
	if len(receipts) != 0 {
		t.Fatalf("store must stay empty when counter unavailable, got %d", len(receipts))
	}
}

func TestSignerTimeoutNoRefund(t *testing.T) {
	counter := NewMemoryCounter()
	host, store := newTestHost(t, counter, timeoutSigner{}, 3)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	if _, err := host.Issue(ctx, []byte("slow")); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Issue err = %v, want context.DeadlineExceeded", err)
	}

	value, _ := counter.Value(context.Background())
	if value != 1 {
		t.Fatalf("counter = %d, want 1: timeout must not refund the consumed count", value)
	}
	receipts, _ := store.List()
	if len(receipts) != 0 {
		t.Fatalf("no receipt may be stored on signer failure, got %d", len(receipts))
	}

	// 换一个会恢复的签名器，失败一次后成功；失败同样不退额。
	host.signer = &flakySigner{fail: 1}
	if _, err := host.Issue(context.Background(), []byte("retry-1")); err == nil {
		t.Fatal("expected flaky signer failure on first call")
	}
	if value, _ := counter.Value(context.Background()); value != 2 {
		t.Fatalf("counter = %d, want 2: signer failure still consumes quota", value)
	}
	receipt, err := host.Issue(context.Background(), []byte("retry-2"))
	if err != nil {
		t.Fatalf("Issue after signer recovery: %v", err)
	}
	if receipt.Seq != 3 {
		t.Fatalf("receipt Seq = %d, want 3", receipt.Seq)
	}
	if _, err := host.Issue(context.Background(), []byte("retry-3")); !errors.Is(err, ErrQuotaExhausted) {
		t.Fatalf("Issue err = %v, want ErrQuotaExhausted", err)
	}
	receipts, _ = store.List()
	if len(receipts) != 1 {
		t.Fatalf("stored receipts = %d, want 1 (only successful signs are logged)", len(receipts))
	}
}

func TestSnapshotRollbackDoesNotRestoreQuota(t *testing.T) {
	const limit uint64 = 4
	counter := NewMemoryCounter()
	dir := t.TempDir()

	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	host := NewHost(counter, staticSigner{}, store, limit)

	for i := 0; i < 2; i++ {
		if _, err := host.Issue(context.Background(), []byte(fmt.Sprintf("before-%d", i))); err != nil {
			t.Fatalf("Issue before snapshot: %v", err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// 对普通存储做快照，然后把额度全部用完。
	snapshot := cloneDir(t, dir)
	store = reopenStore(t, dir)
	host = NewHost(counter, staticSigner{}, store, limit)
	for i := uint64(0); i < limit-2; i++ {
		if _, err := host.Issue(context.Background(), []byte(fmt.Sprintf("after-%d", i))); err != nil {
			t.Fatalf("Issue after snapshot: %v", err)
		}
	}
	if value, _ := counter.Value(context.Background()); value != limit {
		t.Fatalf("counter = %d, want %d", value, limit)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// 回滚普通磁盘到快照并重启宿主：计数器是独立的，不会跟着回退。
	store = reopenStore(t, snapshot)
	host = NewHost(counter, staticSigner{}, store, limit)

	if receipts, err := store.List(); err != nil || len(receipts) != 2 {
		t.Fatalf("rolled-back store has %d receipts (err %v), want 2", len(receipts), err)
	}

	// 磁盘虽然回滚了，计数器仍停在 limit：签发必须被拒绝且额度不会恢复。
	if _, err := host.Issue(context.Background(), []byte("post-rollback")); !errors.Is(err, ErrQuotaExhausted) {
		t.Fatalf("post-rollback Issue err = %v, want ErrQuotaExhausted", err)
	}
	if value, _ := counter.Value(context.Background()); value != limit+1 {
		t.Fatalf("counter = %d, want %d (counter never rolls back)", value, limit+1)
	}
	if remaining, err := host.Remaining(context.Background()); err != nil || remaining != 0 {
		t.Fatalf("Remaining after rollback = %d, %v, want 0", remaining, err)
	}
}

func TestDiskCloneCannotIncreaseQuota(t *testing.T) {
	const limit uint64 = 6
	counter := NewMemoryCounter()
	dir := t.TempDir()

	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	host := NewHost(counter, staticSigner{}, store, limit)
	for i := 0; i < 3; i++ {
		if _, err := host.Issue(context.Background(), []byte(fmt.Sprintf("orig-%d", i))); err != nil {
			t.Fatalf("original Issue: %v", err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// 克隆普通磁盘，模拟整节点磁盘复制；计数器不在磁盘上，不会被克隆。
	clonedDir := cloneDir(t, dir)

	t.Run("cloned node without counter is refused", func(t *testing.T) {
		clonedStore := reopenStore(t, clonedDir)
		clonedHost := NewHost(brokenCounter{}, staticSigner{}, clonedStore, limit)

		if _, err := clonedHost.Issue(context.Background(), []byte("cloned")); !errors.Is(err, ErrCounterUnavailable) {
			t.Fatalf("cloned Issue err = %v, want ErrCounterUnavailable", err)
		}
		receipts, _ := clonedStore.List()
		if len(receipts) != 3 {
			t.Fatalf("cloned store must retain only original 3 receipts, got %d", len(receipts))
		}
	})

	t.Run("cloned node sharing original counter stays within one quota", func(t *testing.T) {
		clonedStore := reopenStore(t, clonedDir)
		clonedHost := NewHost(counter, staticSigner{}, clonedStore, limit)

		issued := 0
		for {
			_, err := clonedHost.Issue(context.Background(), []byte("cloned"))
			if errors.Is(err, ErrQuotaExhausted) {
				break
			}
			if err != nil {
				t.Fatalf("cloned Issue: %v", err)
			}
			issued++
		}
		if issued != int(limit-3) {
			t.Fatalf("cloned node issued %d, want %d: clone cannot add quota", issued, limit-3)
		}

		if value, _ := counter.Value(context.Background()); value != limit+1 {
			t.Fatalf("counter = %d, want %d (the rejected call also consumes)", value, limit+1)
		}

		// 原节点与克隆节点的成功签发总数（按 seq 去重）恰为 limit。
		originalStore := reopenStore(t, dir)
		allReceipts, err := originalStore.List()
		if err != nil {
			t.Fatalf("original store List: %v", err)
		}
		clonedReceipts, err := clonedStore.List()
		if err != nil {
			t.Fatalf("cloned store List: %v", err)
		}
		seqs := make(map[uint64]bool, limit)
		for _, receipt := range allReceipts {
			seqs[receipt.Seq] = true
		}
		for _, receipt := range clonedReceipts {
			seqs[receipt.Seq] = true
		}
		if len(seqs) != int(limit) {
			t.Fatalf("distinct issued seqs across clone = %d, want %d", len(seqs), limit)
		}
	})
}

func TestFileStorePersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()

	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	for i := uint64(1); i <= 3; i++ {
		if err := store.Append(Receipt{Seq: i, Artifact: EncodeArtifact([]byte{byte(i)}), Signature: "sig"}); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	reopened := reopenStore(t, dir)
	receipts, err := reopened.List()
	if err != nil {
		t.Fatalf("List after reopen: %v", err)
	}
	if len(receipts) != 3 {
		t.Fatalf("persisted receipts = %d, want 3", len(receipts))
	}
	for i, receipt := range receipts {
		if receipt.Seq != uint64(i+1) {
			t.Fatalf("receipt %d seq = %d, want %d", i, receipt.Seq, i+1)
		}
	}
}
