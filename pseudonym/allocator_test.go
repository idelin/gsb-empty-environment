package pseudonym

import (
	"path/filepath"
	"sync"
	"testing"
)

// scriptedDigester 是“故意碰撞的摘要替身”：
// 不依赖真实标识符分布，直接返回预置的 32 字节摘要，
// 从而可以精确构造共享前缀的 HMAC 替身来压测碰撞扩展逻辑。
type scriptedDigester struct {
	digests map[string][]byte
}

func (d *scriptedDigester) Digest(identity string) []byte {
	sum := d.digests[identity]
	return append([]byte(nil), sum...)
}

func collisionDigests() map[string][]byte {
	alice := make([]byte, 32) // 全 0 -> base32 全 'a'

	bob := make([]byte, 32)
	bob[1] = 0x01
	for i := 2; i < 32; i++ {
		bob[i] = 0xFF
	}

	// carol 与 bob 共享前 3 字节，即共享 4 个 base32 字符，
	// 迫使分配器连续跳过两个被占用的前缀。
	carol := make([]byte, 32)
	carol[1] = 0x01
	carol[2] = 0xFF

	return map[string][]byte{
		"alice": alice,
		"bob":   bob,
		"carol": carol,
	}
}

func newCollisionAllocator(t *testing.T, store Store) *Allocator {
	t.Helper()
	a, err := New(store, &scriptedDigester{digests: collisionDigests()}, WithMinCodeLen(3))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return a
}

func TestAllocateCollisionExtensionNeverMerges(t *testing.T) {
	a := newCollisionAllocator(t, NewMemoryStore())

	aliceCode, err := a.Allocate("alice")
	if err != nil {
		t.Fatalf("allocate alice: %v", err)
	}
	bobCode, err := a.Allocate("bob")
	if err != nil {
		t.Fatalf("allocate bob: %v", err)
	}
	carolCode, err := a.Allocate("carol")
	if err != nil {
		t.Fatalf("allocate carol: %v", err)
	}

	if aliceCode != "aaa" {
		t.Fatalf("alice code = %q, want aaa", aliceCode)
	}
	if bobCode != "aaa7" {
		t.Fatalf("bob code = %q, want aaa7", bobCode)
	}
	if len(carolCode) != 5 || carolCode != "aaa76" {
		// carol 与 bob 共享前 4 字符（aaa7），扩展到 5 字符 aaa76 才能避让。
		t.Fatalf("carol code = %q, want aaa76", carolCode)
	}

	if bobCode == aliceCode || carolCode == bobCode || carolCode == aliceCode {
		t.Fatalf("colliding identities must not share a published code: %q %q %q", aliceCode, bobCode, carolCode)
	}

	// 已发布短码永不改变，重复分配是幂等的。
	again, err := a.Allocate("alice")
	if err != nil || again != aliceCode {
		t.Fatalf("re-allocate alice = %q, %v; want %q", again, err, aliceCode)
	}

	// 旧短码 aaa 下登记了 bob 与 carol 的碰撞扩展别名，旧事件仍可查询。
	resolved, err := a.Resolve("aaa")
	if err != nil {
		t.Fatalf("resolve aaa: %v", err)
	}
	want := []string{"alice", "bob", "carol"}
	if len(resolved) != len(want) {
		t.Fatalf("resolve aaa = %v, want %v", resolved, want)
	}
	for i := range want {
		if resolved[i] != want[i] {
			t.Fatalf("resolve aaa = %v, want %v", resolved, want)
		}
	}

	// 扩展后的短码只指向各自的持有者，不会串到别人身上。
	bobOwners, err := a.Resolve(bobCode)
	if err != nil || len(bobOwners) != 1 || bobOwners[0] != "bob" {
		t.Fatalf("resolve bob code = %v, %v; want [bob]", bobOwners, err)
	}
	carolOwners, err := a.Resolve(carolCode)
	if err != nil || len(carolOwners) != 1 || carolOwners[0] != "carol" {
		t.Fatalf("resolve carol code = %v, %v; want [carol]", carolOwners, err)
	}

	// bob 与 carol 的旧事件都可能挂在最初发布的最短码 aaa 下，
	// 因此 aaa 必须保留两人的碰撞扩展别名。
	if aliases := a.AliasesOf("aaa"); len(aliases) != 2 {
		t.Fatalf("aliases of aaa = %d, want 2", len(aliases))
	}
	// aaa7 是 bob 自己的已发布短码，carol 从未在那里写过事件，不应挂别名。
	if got := a.AliasesOf("aaa7"); len(got) != 0 {
		t.Fatalf("alias of aaa7 = %+v, want none", got)
	}
}

func TestConfirmUsesFullDigestNotShortCode(t *testing.T) {
	a := newCollisionAllocator(t, NewMemoryStore())

	codes := map[string]string{}
	for _, id := range []string{"alice", "bob", "carol"} {
		code, err := a.Allocate(id)
		if err != nil {
			t.Fatalf("allocate %s: %v", id, err)
		}
		codes[id] = code
	}

	digests := collisionDigests()
	for id, code := range codes {
		if !a.Confirm(code, digests[id]) {
			t.Fatalf("Confirm(%s, own digest) = false, want true", id)
		}
		// 拿到同一个短码索引但 HMAC 不匹配，必须拒绝，防止串接他人事件。
		for other, otherDigest := range digests {
			if other == id {
				continue
			}
			if a.Confirm(code, otherDigest) {
				t.Fatalf("Confirm(%s, %s digest) = true, want false", id, other)
			}
		}
		if a.Confirm(code, []byte("tampered")) {
			t.Fatalf("Confirm with tampered digest = true, want false")
		}
	}

	if _, err := a.Resolve("does-not-exist"); err != ErrUnknownCode {
		t.Fatalf("Resolve unknown code err = %v, want ErrUnknownCode", err)
	}
}

// 模拟事件流：事件只按短码建索引、携带完整 HMAC。
// 即便短码碰撞，查询也必须靠完整摘要把每个人的事件分开。
func TestEventsAreNotMergedOnCollision(t *testing.T) {
	a := newCollisionAllocator(t, NewMemoryStore())

	type event struct {
		code   string
		digest []byte
		who    string
	}
	var events []event
	attach := func(id, payload string) {
		code, err := a.Allocate(id)
		if err != nil {
			t.Fatalf("allocate: %v", err)
		}
		digest, err := a.Digest(id)
		if err != nil {
			t.Fatalf("digest: %v", err)
		}
		events = append(events, event{code: code, digest: digest, who: id + ":" + payload})
	}

	attach("alice", "login")
	attach("bob", "login") // bob 的旧事件挂在碰撞短码 aaa 下
	attach("carol", "login")

	candidates, err := a.Resolve("aaa")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	got := map[string]string{}
	for _, ev := range events {
		if ev.code != "aaa" {
			continue
		}
		for _, id := range candidates {
			digest, digestErr := a.Digest(id)
			if digestErr != nil {
				t.Fatalf("digest %s: %v", id, digestErr)
			}
			if a.Confirm(ev.code, digest) {
				got[id] = ev.who
			}
		}
	}

	if len(got) != 1 || got["alice"] != "alice:login" {
		t.Fatalf("events under colliding code merged incorrectly: %+v", got)
	}
}

func TestConcurrentAllocateIsAtomicAndStable(t *testing.T) {
	const identities = 40
	digests := make(map[string][]byte, identities)
	ids := make([]string, identities)
	for i := 0; i < identities; i++ {
		id := "user-" + string(rune('a'+i%26)) + "-" + string(rune('a'+i/26))
		ids[i] = id
		sum := make([]byte, 32)
		sum[0] = byte(i)
		sum[31] = byte(255 - i)
		digests[id] = sum
	}

	a, err := New(NewMemoryStore(), &scriptedDigester{digests: digests}, WithMinCodeLen(2))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	const goroutines = 16
	var wg sync.WaitGroup
	results := make([][]string, goroutines)
	errs := make(chan error, goroutines*identities)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			codes := make([]string, len(ids))
			for i, id := range ids {
				code, allocErr := a.Allocate(id)
				if allocErr != nil {
					errs <- allocErr
					return
				}
				codes[i] = code
			}
			results[idx] = codes
		}(g)
	}
	wg.Wait()
	close(errs)
	for allocErr := range errs {
		t.Fatalf("concurrent allocate: %v", allocErr)
	}

	// 所有 goroutine 观察到的短码必须完全一致，且不同身份短码唯一。
	baseline := results[0]
	seen := map[string]string{}
	for i, id := range ids {
		code := baseline[i]
		for _, codes := range results[1:] {
			if codes[i] != code {
				t.Fatalf("unstable code for %s: %q vs %q", id, codes[i], code)
			}
		}
		if owner, dup := seen[code]; dup {
			t.Fatalf("code %q assigned to both %s and %s", code, owner, id)
		}
		seen[code] = id
	}
}

func TestWALRestartRecoversState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pseudo.wal")

	store, entries, dropped, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("open wal: %v", err)
	}
	if len(entries) != 0 || dropped != 0 {
		t.Fatalf("fresh wal = %d entries, %d dropped", len(entries), dropped)
	}

	first := newCollisionAllocator(t, store)
	aliceCode, err := first.Allocate("alice")
	if err != nil {
		t.Fatalf("allocate alice: %v", err)
	}
	bobCode, err := first.Allocate("bob")
	if err != nil {
		t.Fatalf("allocate bob: %v", err)
	}
	if err := first.AddAlias("alice", "legacy-ax7", AliasLegacy); err != nil {
		t.Fatalf("add legacy alias: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// 重启：重新打开 WAL 并回放，短码、摘要、别名都必须恢复。
	reopened, recovered, dropped, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("reopen wal: %v", err)
	}
	if dropped != 0 {
		t.Fatalf("dropped = %d, want 0", dropped)
	}
	second, err := New(reopened, &scriptedDigester{digests: collisionDigests()}, WithMinCodeLen(3))
	if err != nil {
		t.Fatalf("reload allocator: %v", err)
	}

	if got, lookupErr := second.Lookup("alice"); lookupErr != nil || got != aliceCode {
		t.Fatalf("alice after restart = %q, %v; want %q", got, lookupErr, aliceCode)
	}
	if got, lookupErr := second.Lookup("bob"); lookupErr != nil || got != bobCode {
		t.Fatalf("bob after restart = %q, %v; want %q", got, lookupErr, bobCode)
	}

	// 恢复后重复分配仍是同一个已发布短码，不会重新扩展。
	if got, allocErr := second.Allocate("bob"); allocErr != nil || got != bobCode {
		t.Fatalf("re-allocate after restart = %q, %v; want %q", got, allocErr, bobCode)
	}

	resolved, err := second.Resolve("aaa")
	if err != nil || len(resolved) != 2 {
		t.Fatalf("resolve aaa after restart = %v, %v", resolved, err)
	}
	foundLegacy := false
	for _, alias := range second.AliasesOf("legacy-ax7") {
		if alias.Identity == "alice" && alias.Kind == AliasLegacy {
			foundLegacy = true
		}
	}
	if !foundLegacy {
		t.Fatalf("legacy alias lost after restart")
	}

	// carol 在重启后首次分配，必须继续遵守历史碰撞关系。
	carolCode, err := second.Allocate("carol")
	if err != nil {
		t.Fatalf("allocate carol after restart: %v", err)
	}
	if len(carolCode) != 5 {
		t.Fatalf("carol code after restart = %q, want length 5", carolCode)
	}
	if err := reopened.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if len(recovered) == 0 {
		t.Fatal("expected recovered WAL entries on reopen")
	}
}
