package netsec

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func newTestPage(size int) *Page {
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i)
	}
	return NewPage(data)
}

func mustMap(t *testing.T, h *Host, tenant string, page *Page) MappingRef {
	t.Helper()
	ref, err := h.Map(tenant, page)
	if err != nil {
		t.Fatalf("Map(%q) unexpected error: %v", tenant, err)
	}
	return ref
}

func mustConfirm(t *testing.T, h *Host, ref MappingRef, success bool, cause error) {
	t.Helper()
	if err := h.ConfirmInvalidation(ref, success, cause); err != nil {
		t.Fatalf("ConfirmInvalidation(%s, success=%v) unexpected error: %v", ref, success, err)
	}
}

// 场景一：失效确认乱序到达，事务必须等全部映射确认后才成功。
func TestRevokeOutOfOrderConfirmations(t *testing.T) {
	h := NewHost()
	page := newTestPage(32)
	refs := []MappingRef{
		mustMap(t, h, "tenant-a", page),
		mustMap(t, h, "tenant-b", page),
		mustMap(t, h, "tenant-c", page),
	}

	h.BeginRevocation()
	if phase := h.RevocationPhase(); phase != PhaseBarrier {
		t.Fatalf("phase after begin = %q, want %q", phase, PhaseBarrier)
	}

	// 屏障升起后，任何读取都必须立即失败，不能留下仍可读的映射。
	for _, ref := range refs {
		if _, err := h.Read(ref); !errors.Is(err, ErrBarrier) {
			t.Fatalf("Read(%s) during revocation = %v, want ErrBarrier", ref, err)
		}
	}

	// 乱序确认：先确认最后一条，再确认第一条，最后中间一条。
	mustConfirm(t, h, refs[2], true, nil)
	if phase := h.RevocationPhase(); phase != PhaseDraining {
		t.Fatalf("phase after first confirmation = %q, want %q", phase, PhaseDraining)
	}
	mustConfirm(t, h, refs[0], true, nil)

	if pending := h.pendingInvalidations(); pending != 1 {
		t.Fatalf("pending invalidations = %d, want 1", pending)
	}
	if !h.inRevocation() {
		t.Fatalf("barrier dropped before all confirmations, phase = %q", h.RevocationPhase())
	}

	mustConfirm(t, h, refs[1], true, nil)

	if err := h.Revoke(context.Background()); err != nil {
		t.Fatalf("Revoke = %v, want nil", err)
	}

	if phase := h.RevocationPhase(); phase != PhaseIdle {
		t.Fatalf("phase after revoke = %q, want idle", phase)
	}
	for _, ref := range refs {
		if _, err := h.Read(ref); !errors.Is(err, ErrInvalidMapping) {
			t.Fatalf("Read(%s) after revoke = %v, want ErrInvalidMapping", ref, err)
		}
	}
}

// 场景二：成功撤权后映射编号可以重用，但旧代际引用的确认必须被拒绝。
func TestMappingIDReuseRejectsStaleGeneration(t *testing.T) {
	h := NewHost()
	page1 := newTestPage(16)
	oldRef := mustMap(t, h, "tenant-a", page1)
	if oldRef.Generation() != 1 {
		t.Fatalf("first generation = %d, want 1", oldRef.Generation())
	}

	h.BeginRevocation()
	mustConfirm(t, h, oldRef, true, nil)
	if err := h.Revoke(context.Background()); err != nil {
		t.Fatalf("Revoke = %v, want nil", err)
	}

	page2 := newTestPage(8)
	newRef, err := h.Map("tenant-b", page2)
	if err != nil {
		t.Fatalf("Map after revoke: %v", err)
	}
	if newRef.ID() != oldRef.ID() {
		t.Fatalf("reused id = %d, want %d", newRef.ID(), oldRef.ID())
	}
	if newRef.Generation() != 2 {
		t.Fatalf("reused generation = %d, want 2", newRef.Generation())
	}

	data, err := h.Read(newRef)
	if err != nil {
		t.Fatalf("Read reused mapping: %v", err)
	}
	if len(data) != page2.Size() {
		t.Fatalf("reused mapping reads %d bytes, want %d", len(data), page2.Size())
	}

	// 旧代际的迟到确认绝不能影响新映射。
	if err := h.ConfirmInvalidation(oldRef, true, nil); !errors.Is(err, ErrStaleMapping) {
		t.Fatalf("stale confirmation = %v, want ErrStaleMapping", err)
	}
	if _, err := h.Read(oldRef); !errors.Is(err, ErrStaleMapping) {
		t.Fatalf("stale read = %v, want ErrStaleMapping", err)
	}

	// 新代际的撤权只对新引用生效，旧引用继续被视为过期。
	h.BeginRevocation()
	mustConfirm(t, h, newRef, true, nil)
	if err := h.Revoke(context.Background()); err != nil {
		t.Fatalf("second Revoke = %v, want nil", err)
	}
	if _, err := h.Read(oldRef); !errors.Is(err, ErrStaleMapping) {
		t.Fatalf("stale read after second revoke = %v, want ErrStaleMapping", err)
	}
}

// 场景三：撤权事务期间新租户的映射申请必须被屏障拒绝，事务结束后恢复。
func TestNewTenantBlockedDuringRevocation(t *testing.T) {
	h := NewHost()
	page := newTestPage(24)
	existing := mustMap(t, h, "tenant-a", page)

	h.BeginRevocation()

	if _, err := h.Map("tenant-new", newTestPage(4)); !errors.Is(err, ErrBarrier) {
		t.Fatalf("Map during revocation = %v, want ErrBarrier", err)
	}

	// 并发申请同样全部被拒绝。
	const applicants = 16
	var wg sync.WaitGroup
	errs := make([]error, applicants)
	for i := 0; i < applicants; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, errs[idx] = h.Map(fmt.Sprintf("tenant-%d", idx), newTestPage(4))
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if !errors.Is(err, ErrBarrier) {
			t.Fatalf("concurrent Map[%d] = %v, want ErrBarrier", i, err)
		}
	}

	mustConfirm(t, h, existing, true, nil)
	if err := h.Revoke(context.Background()); err != nil {
		t.Fatalf("Revoke = %v, want nil", err)
	}

	after, err := h.Map("tenant-new", newTestPage(4))
	if err != nil {
		t.Fatalf("Map after revocation = %v, want nil", err)
	}
	if _, err := h.Read(after); err != nil {
		t.Fatalf("Read post-revocation mapping: %v", err)
	}
}

// 场景四：失效确认失败的映射进入永久隔离，编号不回收、不可复用。
func TestFailedInvalidationStaysIsolated(t *testing.T) {
	h := NewHost()
	page := newTestPage(16)
	badRef := mustMap(t, h, "tenant-bad", page)
	goodRef := mustMap(t, h, "tenant-good", page)

	h.BeginRevocation()

	invalidationErr := errors.New("runner failed to unmap page")
	mustConfirm(t, h, badRef, false, invalidationErr)
	mustConfirm(t, h, goodRef, true, nil)

	var err error
	err = h.Revoke(context.Background())
	if !errors.Is(err, ErrInvalidationFailed) {
		t.Fatalf("Revoke = %v, want ErrInvalidationFailed", err)
	}

	// 失败映射保持隔离：读、确认、复用全部被拒绝。
	if _, err := h.Read(badRef); !errors.Is(err, ErrIsolatedMapping) {
		t.Fatalf("Read isolated = %v, want ErrIsolatedMapping", err)
	}
	if err := h.ConfirmInvalidation(badRef, true, nil); !errors.Is(err, ErrIsolatedMapping) {
		t.Fatalf("re-confirm isolated = %v, want ErrIsolatedMapping", err)
	}

	isolated := h.IsolatedIDs()
	if len(isolated) != 1 || isolated[0] != badRef.ID() {
		t.Fatalf("IsolatedIDs = %v, want [%d]", isolated, badRef.ID())
	}

	// 成功映射的编号被回收，隔离编号不会被再次发放。
	reused, err := h.Map("tenant-c", page)
	if err != nil {
		t.Fatalf("Map after partial failure: %v", err)
	}
	if reused.ID() == badRef.ID() {
		t.Fatalf("isolated id %d was reused", badRef.ID())
	}

	// 后续撤权事务中，隔离编号依然不可复用。
	h.BeginRevocation()
	mustConfirm(t, h, reused, true, nil)
	if err := h.Revoke(context.Background()); err != nil {
		t.Fatalf("second Revoke = %v, want nil", err)
	}
	if ids := h.IsolatedIDs(); len(ids) != 1 || ids[0] != badRef.ID() {
		t.Fatalf("IsolatedIDs after second revoke = %v, want [%d]", ids, badRef.ID())
	}

	// 被隔离的旧引用在编号表中已无槽位，但仍通过隔离集合被识别。
	if _, err := h.Read(badRef); !errors.Is(err, ErrIsolatedMapping) {
		t.Fatalf("Read isolated after second revoke = %v, want ErrIsolatedMapping", err)
	}
}

// 场景五：首个撤权调用者被取消时事务继续推进，后续调用者可等待同一事务并拿到结果。
func TestRevokeCancellationDoesNotAbortTransaction(t *testing.T) {
	h := NewHost()
	page := newTestPage(12)
	refs := []MappingRef{
		mustMap(t, h, "tenant-a", page),
		mustMap(t, h, "tenant-b", page),
	}

	ctx, cancel := context.WithCancel(context.Background())
	canceledCall := make(chan error, 1)
	h.BeginRevocation()
	go func() { canceledCall <- h.Revoke(ctx) }()
	select {
	case <-time.After(50 * time.Millisecond):
	case err := <-canceledCall:
		t.Fatalf("Revoke returned before cancellation: %v", err)
	}

	cancel()
	select {
	case err := <-canceledCall:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled Revoke = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled Revoke did not return")
	}

	// 屏障仍然有效，新申请继续被拒绝。
	if _, err := h.Map("tenant-late", page); !errors.Is(err, ErrBarrier) {
		t.Fatalf("Map after cancel = %v, want ErrBarrier", err)
	}

	// 第二个调用者加入同一事务，确认到达后收敛到同一结果。
	secondCall := make(chan error, 1)
	go func() { secondCall <- h.Revoke(context.Background()) }()
	select {
	case <-time.After(50 * time.Millisecond):
	case err := <-secondCall:
		t.Fatalf("second Revoke returned before confirmations: %v", err)
	}

	failure := errors.New("device busy")
	mustConfirm(t, h, refs[0], false, failure)
	mustConfirm(t, h, refs[1], true, nil)

	select {
	case err := <-secondCall:
		if !errors.Is(err, ErrInvalidationFailed) {
			t.Fatalf("second Revoke = %v, want ErrInvalidationFailed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second Revoke did not return")
	}
	if h.RevocationPhase() != PhaseIdle {
		t.Fatalf("phase = %q, want idle", h.RevocationPhase())
	}
}

// 场景六：没有已登记映射时撤权立即成功，且不留下事务状态。
func TestRevokeWithNoMappings(t *testing.T) {
	h := NewHost()
	if err := h.Revoke(context.Background()); err != nil {
		t.Fatalf("Revoke empty host = %v, want nil", err)
	}
	if phase := h.RevocationPhase(); phase != PhaseIdle {
		t.Fatalf("phase = %q, want idle", phase)
	}
	if refs := h.ActiveMappings(); len(refs) != 0 {
		t.Fatalf("ActiveMappings = %v, want empty", refs)
	}
}

// 重复确认必须被拒绝，避免同一映射被重复计入事务。
func TestDuplicateConfirmationRejected(t *testing.T) {
	h := NewHost()
	first := mustMap(t, h, "tenant-a", newTestPage(8))
	second := mustMap(t, h, "tenant-b", newTestPage(8))

	h.BeginRevocation()
	if pending := h.pendingInvalidations(); pending != 2 {
		t.Fatalf("pending invalidations = %d, want 2", pending)
	}

	mustConfirm(t, h, first, true, nil)
	if err := h.ConfirmInvalidation(first, true, nil); !errors.Is(err, ErrAlreadyInvalidated) {
		t.Fatalf("duplicate confirmation = %v, want ErrAlreadyInvalidated", err)
	}

	// 事务仍在等待另一条映射，第一条的重复确认没有改变事务状态。
	if pending := h.pendingInvalidations(); pending != 1 {
		t.Fatalf("pending invalidations = %d, want 1", pending)
	}
	mustConfirm(t, h, second, true, nil)
	if err := h.Revoke(context.Background()); err != nil {
		t.Fatalf("Revoke = %v, want nil", err)
	}
}
