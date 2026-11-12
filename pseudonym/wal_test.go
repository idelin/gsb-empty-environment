package pseudonym

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWALTornTailIsDroppedAndCommittedCorruptionErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pseudo.wal")

	store, _, _, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("open wal: %v", err)
	}
	first := newCollisionAllocator(t, store)
	if _, err := first.Allocate("alice"); err != nil {
		t.Fatalf("allocate alice: %v", err)
	}
	if _, err := first.Allocate("bob"); err != nil {
		t.Fatalf("allocate bob: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// 追加一行没有换行符的半条记录，模拟崩溃时被截断的写入。
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open for append: %v", err)
	}
	if _, err := f.WriteString(`{"binding":{"identity":"ghost"`); err != nil {
		t.Fatalf("write torn tail: %v", err)
	}
	if err := f.Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	reopened, entries, dropped, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("reopen with torn tail: %v", err)
	}
	if dropped != 1 {
		t.Fatalf("dropped = %d, want 1", dropped)
	}
	a, err := New(reopened, &scriptedDigester{digests: collisionDigests()}, WithMinCodeLen(3))
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got, lookupErr := a.Lookup("alice"); lookupErr != nil || got != "aaa" {
		t.Fatalf("alice after torn restart = %q, %v", got, lookupErr)
	}
	if _, lookupErr := a.Lookup("ghost"); lookupErr != ErrUnknownIdentity {
		t.Fatalf("ghost identity should not exist, got %v", lookupErr)
	}

	// 截断半行被丢弃后，新的追加仍能正常提交并再次恢复。
	carolCode, err := a.Allocate("carol")
	if err != nil {
		t.Fatalf("allocate carol: %v", err)
	}
	if len(carolCode) != 5 {
		t.Fatalf("carol code = %q, want length 5", carolCode)
	}
	if err := reopened.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if len(entries) < 2 {
		t.Fatalf("recovered committed entries = %d, want >= 2", len(entries))
	}

	// 已提交区域（带换行的完整行）出现坏 JSON 必须报错，不能静默吞掉。
	corrupt := filepath.Join(t.TempDir(), "corrupt.wal")
	badLine := append([]byte(`{"not-json":`), '\n')
	if err := os.WriteFile(corrupt, badLine, 0o600); err != nil {
		t.Fatalf("write corrupt wal: %v", err)
	}
	if _, _, _, err := OpenWAL(corrupt); err != ErrCorrupt {
		t.Fatalf("corrupt wal err = %v, want ErrCorrupt", err)
	}
}

func TestReplayConflictOnTamperedBinding(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pseudo.wal")
	store, _, _, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("open wal: %v", err)
	}
	a := newCollisionAllocator(t, store)
	if _, err := a.Allocate("alice"); err != nil {
		t.Fatalf("allocate: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// 同一身份在日志里出现不同短码，属于状态冲突，回放必须拒绝。
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open append: %v", err)
	}
	conflict := `{"binding":{"identity":"alice","code":"zzzz","digest":"AA==","created":"2026-11-13T00:00:00Z"}}` + "\n"
	if _, err := f.WriteString(conflict); err != nil {
		t.Fatalf("write conflict: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	reopened, _, _, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if _, err := New(reopened, &scriptedDigester{digests: collisionDigests()}, WithMinCodeLen(3)); err != ErrConflict {
		t.Fatalf("replay conflict err = %v, want ErrConflict", err)
	}
}

func TestHMACDigesterIsDeterministicAndBoundToKey(t *testing.T) {
	d1 := NewHMACDigester([]byte("server-secret-key"))
	d2 := NewHMACDigester([]byte("server-secret-key"))
	other := NewHMACDigester([]byte("different-key"))

	sum1 := d1.Digest("user-42")
	sum2 := d2.Digest("user-42")
	sumOtherKey := other.Digest("user-42")
	sumOtherUser := d1.Digest("user-43")

	if len(sum1) != 32 {
		t.Fatalf("digest length = %d, want 32", len(sum1))
	}
	if !equalBytes(sum1, sum2) {
		t.Fatal("same key and identity must produce the same digest after restart")
	}
	if equalBytes(sum1, sumOtherKey) {
		t.Fatal("digests with different keys must differ")
	}
	if equalBytes(sum1, sumOtherUser) {
		t.Fatal("digests for different identities must differ")
	}

	// 短码必须只是完整摘要的前缀索引。
	encoded := encodeDigest(sum1)
	a, err := New(NewMemoryStore(), d1, WithMinCodeLen(8))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	code, err := a.Allocate("user-42")
	if err != nil {
		t.Fatalf("allocate: %v", err)
	}
	if len(code) != 8 || encoded[:8] != code {
		t.Fatalf("code = %q, want prefix %q of %q", code, encoded[:8], encoded)
	}
	if !a.Confirm(code, sum1) {
		t.Fatal("Confirm must accept the stored full HMAC")
	}

	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("empty HMAC key must panic")
			}
		}()
		NewHMACDigester(nil)
	}()
}

func TestAddLegacyAliasKeepsOldEventsQueryable(t *testing.T) {
	a := newCollisionAllocator(t, NewMemoryStore())
	code, err := a.Allocate("alice")
	if err != nil {
		t.Fatalf("allocate: %v", err)
	}

	if err := a.AddAlias("alice", "old-short", AliasLegacy); err != nil {
		t.Fatalf("add alias: %v", err)
	}
	// 重复登记同一条别名是幂等的。
	if err := a.AddAlias("alice", "old-short", AliasLegacy); err != nil {
		t.Fatalf("re-add alias: %v", err)
	}

	owners, err := a.Resolve("old-short")
	if err != nil || len(owners) != 1 || owners[0] != "alice" {
		t.Fatalf("resolve legacy code = %v, %v; want [alice]", owners, err)
	}
	aliases := a.AliasesOf("old-short")
	if len(aliases) != 1 || aliases[0].Kind != AliasLegacy {
		t.Fatalf("legacy aliases = %+v", aliases)
	}

	// 别名关系不改变已发布短码。
	if got, _ := a.Lookup("alice"); got != code {
		t.Fatalf("published code changed after alias: %q vs %q", got, code)
	}
	if err := a.AddAlias("", "x", AliasLegacy); err != ErrEmptyIdentity {
		t.Fatalf("empty identity err = %v, want ErrEmptyIdentity", err)
	}
}

func TestAllocateFailsWhenCodeSpaceExhausted(t *testing.T) {
	// 用与 alice 完全相同的摘要替身，并预填该摘要的每一个前缀，
	// 使分配器无任何未占用前缀可用，必须明确报错而不是复用他人短码。
	store := NewMemoryStore()
	encoded := encodeDigest(collisionDigests()["alice"])
	a := newCollisionAllocator(t, store)

	for n := a.minCodeLen; n <= maxCodeChars && n <= len(encoded); n++ {
		record := Record{
			Identity: "occupant-" + string(rune(n)),
			Code:     encoded[:n],
			Digest:   []byte{byte(n)},
		}
		if err := a.replay(Entry{Binding: &record}); err != nil {
			t.Fatalf("seed prefix %d: %v", n, err)
		}
	}

	if _, err := a.Allocate("alice"); err != ErrCodeSpaceExhausted {
		t.Fatalf("exhausted allocate err = %v, want ErrCodeSpaceExhausted", err)
	}
}

func TestEmptyIdentityRejected(t *testing.T) {
	a := newCollisionAllocator(t, NewMemoryStore())
	if _, err := a.Allocate(""); err != ErrEmptyIdentity {
		t.Fatalf("empty allocate err = %v, want ErrEmptyIdentity", err)
	}
	if _, err := a.Lookup("missing"); err != ErrUnknownIdentity {
		t.Fatalf("lookup missing err = %v, want ErrUnknownIdentity", err)
	}
	if _, err := a.Digest("missing"); err != ErrUnknownIdentity {
		t.Fatalf("digest missing err = %v, want ErrUnknownIdentity", err)
	}
}
