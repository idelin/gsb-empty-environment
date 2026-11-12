package netsec

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// recordingCommitter 模拟外部持久化/票据签发，记录每次成功提交。
type recordingCommitter struct {
	mu      sync.Mutex
	commits map[SessionID]SessionID
	failN   int32 // 前 failN 次提交返回失败
	calls   atomic.Int32
}

func newRecordingCommitter() *recordingCommitter {
	return &recordingCommitter{commits: map[SessionID]SessionID{}}
}

var errCommitFailed = errors.New("netsec: test commit failure")

func (c *recordingCommitter) Commit(oldID, newID SessionID, _ AuthResult) error {
	n := c.calls.Add(1)
	if n <= atomic.LoadInt32(&c.failN) {
		return errCommitFailed
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.commits[oldID] = newID
	return nil
}

func (c *recordingCommitter) committedOldIDs() []SessionID {
	c.mu.Lock()
	defer c.mu.Unlock()
	ids := make([]SessionID, 0, len(c.commits))
	for k := range c.commits {
		ids = append(ids, k)
	}
	return ids
}

// 双客户端场景：客户端 A 持匿名标识登录，客户端 B（会话固定攻击者）
// 也持有同一旧标识。轮换后 A 拿到新标识，B 手里的旧标识彻底失效。
func TestRotate_DoubleClient_SessionFixation(t *testing.T) {
	st := NewStore()

	oldID, err := st.NewAnonymous(map[string]string{
		"cart":     "item-42",
		"csrf":     "anon-csrf",
		"internal": "must-not-survive",
	})
	if err != nil {
		t.Fatalf("NewAnonymous: %v", err)
	}

	committer := newRecordingCommitter()
	auth := AuthResult{UserID: "user-1", Role: "member", AuthTime: 1_700_000_000}

	// 客户端 A 完成匿名 -> 登录的轮换。
	newID, err := st.Rotate(oldID, auth, []string{"csrf", "cart"}, committer)
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if newID == oldID {
		t.Fatal("rotation must issue a fresh session identifier")
	}

	// 客户端 A：新标识承载认证结果与白名单状态。
	s, err := st.Get(newID)
	if err != nil {
		t.Fatalf("Get(newID): %v", err)
	}
	if !s.Authenticated() || s.UserID() != "user-1" || s.Role() != "member" {
		t.Fatalf("new session not bound to auth result: %+v", s)
	}
	if v, ok := s.StateValue("csrf"); !ok || v != "anon-csrf" {
		t.Fatalf("whitelisted csrf state missing: %v", s.state)
	}
	if v, ok := s.StateValue("cart"); !ok || v != "item-42" {
		t.Fatalf("whitelisted cart state missing: %v", s.state)
	}
	if _, ok := s.StateValue("internal"); ok {
		t.Fatal("non-whitelisted anonymous state must not be copied")
	}

	// 客户端 B：旧标识只能读到明确的失效结果，且指向同一个新标识。
	_, err = st.Get(oldID)
	var inv *InvalidationError
	if !errors.As(err, &inv) {
		t.Fatalf("old id Get error = %v, want InvalidationError", err)
	}
	if !errors.Is(err, ErrSessionInvalidated) {
		t.Fatalf("errors.Is(ErrSessionInvalidated) failed for %v", err)
	}
	if inv.OldID != oldID || inv.NewID != newID {
		t.Fatalf("invalidation pointer mismatch: %+v", inv)
	}

	// 旧标识不可再写、不可再轮换，攻击者无法复活或接管会话。
	if err := st.PutState(oldID, "cart", "attacker"); !errors.Is(err, ErrSessionInvalidated) {
		t.Fatalf("PutState on old id error = %v, want ErrSessionInvalidated", err)
	}
	if _, err := st.Rotate(oldID, auth, []string{"csrf"}, committer); !errors.Is(err, ErrSessionInvalidated) {
		t.Fatalf("re-Rotate old id error = %v, want ErrSessionInvalidated", err)
	}

	// 身份唯一归属到新标识。
	owner, ok := st.OwnerOf("user-1")
	if !ok || owner != newID {
		t.Fatalf("owner = %q (ok=%v), want %q", owner, ok, newID)
	}
	if got := committer.commits[oldID]; got != newID {
		t.Fatalf("external commit binding = %q, want %q", got, newID)
	}
}

// 提交失败必须整体回滚：旧匿名会话原样可用，不产生半登录/墓碑等中间态；
// 失败后客户端仍可用原标识重试并成功。
func TestRotate_CommitFailure_Rollback(t *testing.T) {
	st := NewStore()
	oldID, err := st.NewAnonymous(map[string]string{"csrf": "c1", "dropme": "x"})
	if err != nil {
		t.Fatalf("NewAnonymous: %v", err)
	}

	committer := newRecordingCommitter()
	atomic.StoreInt32(&committer.failN, 2) // 前两次提交失败

	for attempt := 1; attempt <= 2; attempt++ {
		if _, err := st.Rotate(oldID, AuthResult{UserID: "user-2"}, []string{"csrf"}, committer); !errors.Is(err, errCommitFailed) {
			t.Fatalf("attempt %d error = %v, want errCommitFailed", attempt, err)
		}

		// 回滚后旧会话仍是可读写的匿名会话。
		s, err := st.Get(oldID)
		if err != nil {
			t.Fatalf("after failed commit Get(oldID): %v", err)
		}
		if s.Authenticated() || s.Invalidated() {
			t.Fatalf("old session mutated by failed commit: %+v", s)
		}
		if v, _ := s.StateValue("csrf"); v != "c1" {
			t.Fatalf("anonymous state lost after rollback: %v", s.state)
		}
		if err := st.PutState(oldID, "late", "v"); err != nil {
			t.Fatalf("PutState after rollback: %v", err)
		}
		if _, ok := st.OwnerOf("user-2"); ok {
			t.Fatal("identity must not be bound after failed commit")
		}
	}

	// 第三次提交成功，轮换在同一旧标识上完成，身份唯一归属。
	newID, err := st.Rotate(oldID, AuthResult{UserID: "user-2", Role: "admin"}, []string{"csrf"}, committer)
	if err != nil {
		t.Fatalf("Rotate after retry: %v", err)
	}
	got, err := st.Get(newID)
	if err != nil || got.UserID() != "user-2" {
		t.Fatalf("post-retry session wrong: %v %+v", err, got)
	}
	if _, ok := got.StateValue("late"); ok {
		t.Fatal("state written before rotation must survive only via whitelist; late key not whitelisted")
	}
	if owner, ok := st.OwnerOf("user-2"); !ok || owner != newID {
		t.Fatalf("owner after retry = %q, want %q", owner, newID)
	}

	// 另一个匿名会话不得再绑定到同一身份。
	otherID, _ := st.NewAnonymous(nil)
	_, err = st.Rotate(otherID, AuthResult{UserID: "user-2"}, []string{"csrf"}, newRecordingCommitter())
	if !errors.Is(err, ErrIdentityBoundElsewhere) {
		t.Fatalf("second client bind same identity error = %v, want ErrIdentityBoundElsewhere", err)
	}
}

// 并发轮换同一匿名标识：无论多少客户端同时提交，恰好一个成功，
// 身份只归属一个新标识；旧标识上的并发写入不会污染登录会话。
func TestRotate_Concurrent_UniqueOwnership(t *testing.T) {
	st := NewStore()
	oldID, err := st.NewAnonymous(map[string]string{
		"csrf":   "shared",
		"anchor": "fixed", // 轮换期间不被任何并发写入修改的白名单键
	})
	if err != nil {
		t.Fatalf("NewAnonymous: %v", err)
	}

	const clients = 32
	committer := newRecordingCommitter()
	atomic.StoreInt32(&committer.failN, 5) // 制造提交失败与重试交织

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		success []SessionID
	)

	for i := 0; i < clients; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := st.Rotate(oldID, AuthResult{UserID: "user-3", Role: "member"}, []string{"csrf", "anchor"}, committer)
			if err == nil {
				mu.Lock()
				success = append(success, id)
				mu.Unlock()
			} else if !errors.Is(err, ErrSessionInvalidated) &&
				!errors.Is(err, ErrAlreadyAuthenticated) &&
				!errors.Is(err, errCommitFailed) {
				t.Errorf("unexpected Rotate error: %v", err)
			}
		}()
	}
	wg.Wait()

	if len(success) != 1 {
		t.Fatalf("successful rotations = %d, want exactly 1: %v", len(success), success)
	}
	winner := success[0]

	owner, ok := st.OwnerOf("user-3")
	if !ok || owner != winner {
		t.Fatalf("unique owner = %q (ok=%v), want %q", owner, ok, winner)
	}

	// 登录会话只含轮换瞬间的白名单快照，旧标识上的滞后写入无法覆盖它。
	s, err := st.Get(winner)
	if err != nil {
		t.Fatalf("Get(winner): %v", err)
	}
	if v, _ := s.StateValue("anchor"); v != "fixed" {
		t.Fatalf("whitelisted snapshot altered: anchor=%q", v)
	}
	if _, ok := s.StateValue("poison"); ok {
		t.Fatalf("non-whitelisted stale write leaked into authenticated session: %v", s.state)
	}

	// 轮换完成后，另一批客户端继续拿旧标识写状态：全部必须被墓碑拒绝，
	// 且永远无法影响新登录会话。
	var postWG sync.WaitGroup
	var rejected atomic.Int32
	for i := 0; i < clients; i++ {
		postWG.Add(1)
		go func(i int) {
			defer postWG.Done()
			if errors.Is(st.PutState(oldID, "poison", fmt.Sprintf("post-%d", i)), ErrSessionInvalidated) {
				rejected.Add(1)
			}
		}(i)
	}
	postWG.Wait()
	if got := rejected.Load(); got != clients {
		t.Fatalf("post-rotation stale writes rejected = %d, want %d", got, clients)
	}
	after, err := st.Get(winner)
	if err != nil {
		t.Fatalf("Get(winner) after stale writes: %v", err)
	}
	if _, ok := after.StateValue("poison"); ok {
		t.Fatal("authenticated session mutated through dead identifier")
	}

	// 所有外部提交中，至多一次与存储最终状态一致（失败提交不落存储）。
	if len(committer.committedOldIDs()) > 1 {
		t.Fatalf("external store received %d old-id bindings, want at most 1", len(committer.committedOldIDs()))
	}
	if committer.commits[oldID] != winner {
		t.Fatalf("external binding mismatch: got %q want %q", committer.commits[oldID], winner)
	}

	// 旧标识最终必然是墓碑，任何客户端都只能读到失效结果。
	if _, err := st.Get(oldID); !errors.Is(err, ErrSessionInvalidated) {
		t.Fatalf("old id final error = %v, want ErrSessionInvalidated", err)
	}
}

func TestRotate_WhitelistAndGuards(t *testing.T) {
	st := NewStore()
	id, _ := st.NewAnonymous(map[string]string{"a": "1", "b": "2"})

	// 空白名单直接拒绝，防止整包匿名状态被隐式复制。
	if _, err := st.Rotate(id, AuthResult{UserID: "u"}, nil, newRecordingCommitter()); !errors.Is(err, ErrEmptyWhitelist) {
		t.Fatalf("empty whitelist error = %v", err)
	}

	// 缺失的白名单键跳过，不写入空值。
	newID, err := st.Rotate(id, AuthResult{UserID: "u"}, []string{"a", "missing"}, newRecordingCommitter())
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	s, _ := st.Get(newID)
	if v, ok := s.StateValue("a"); !ok || v != "1" {
		t.Fatalf("whitelisted key missing: %v", s.state)
	}
	if _, ok := s.StateValue("b"); ok {
		t.Fatal("key b not whitelisted but copied")
	}
	if _, ok := s.StateValue("missing"); ok {
		t.Fatal("missing whitelist key must not be materialized")
	}

	// 已认证会话不能重复升级。
	if _, err := st.Rotate(newID, AuthResult{UserID: "u2"}, []string{"a"}, newRecordingCommitter()); !errors.Is(err, ErrAlreadyAuthenticated) {
		t.Fatalf("re-auth error = %v, want ErrAlreadyAuthenticated", err)
	}

	// 未知标识。
	if _, err := st.Get("does-not-exist"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("missing session error = %v", err)
	}
}

func TestNewSessionID_UniqueAndRandom(t *testing.T) {
	seen := map[SessionID]struct{}{}
	for i := 0; i < 256; i++ {
		id, err := NewSessionID()
		if err != nil {
			t.Fatalf("NewSessionID: %v", err)
		}
		if len(id) != 64 {
			t.Fatalf("id length = %d, want 64 hex chars", len(id))
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate session id: %s", id)
		}
		seen[id] = struct{}{}
	}
}
