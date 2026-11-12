package taskpkg

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	tenantAlice   = "alice"
	tenantBob     = "bob"
	tenantMallory = "mallory"
)

type keyring struct {
	priv map[string]ed25519.PrivateKey
	pub  map[string]ed25519.PublicKey
}

func newKeyring(t *testing.T, tenants ...string) *keyring {
	t.Helper()
	kr := &keyring{
		priv: make(map[string]ed25519.PrivateKey),
		pub:  make(map[string]ed25519.PublicKey),
	}
	for _, id := range tenants {
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatalf("generate key for %s: %v", id, err)
		}
		kr.priv[id] = priv
		kr.pub[id] = pub
	}
	return kr
}

func newTestHost(t *testing.T) (*Host, *keyring) {
	t.Helper()
	kr := newKeyring(t, tenantAlice, tenantBob, tenantMallory)
	h := NewHost()
	for _, id := range []string{tenantAlice, tenantBob, tenantMallory} {
		if err := h.RegisterTenant(id, kr.pub[id]); err != nil {
			t.Fatalf("register %s: %v", id, err)
		}
	}
	return h, kr
}

func (h *Host) objectSnapshot(t *testing.T, id string) (owner string, version, generation int64) {
	t.Helper()
	h.mu.RLock()
	defer h.mu.RUnlock()
	obj, ok := h.objects[id]
	if !ok {
		t.Fatalf("object %s missing", id)
	}
	return obj.owner, obj.version, obj.generation
}

func makeTicket(t *testing.T, h *Host, kr *keyring, objectID, from, to string) Ticket {
	t.Helper()
	_, version, generation := h.objectSnapshot(t, objectID)
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		t.Fatalf("nonce: %v", err)
	}
	tk := NewTicket(objectID, from, to, version, generation, nonce, h.now().Add(h.ttl))
	tk.SignSource(kr.priv[from])
	tk.SignTarget(kr.priv[to])
	return tk
}

// TestSmallObjectRoundTrip 覆盖小对象模型：1~32 字节明文的创建、可信打开与读取。
func TestSmallObjectRoundTrip(t *testing.T) {
	h, _ := newTestHost(t)
	alice := Principal{TenantID: tenantAlice}

	smallObjects := [][]byte{
		{0x01},
		{0xCA, 0xFE},
		[]byte("hi"),
		bytes.Repeat([]byte{7}, 16),
		bytes.Repeat([]byte{0xAB}, 32),
	}
	for i, pt := range smallObjects {
		id := fmt.Sprintf("obj-%d", i)
		if err := h.Create(alice, id, pt); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
		capRO, err := h.Open(alice, id, PermRead)
		if err != nil {
			t.Fatalf("open %s: %v", id, err)
		}
		if capRO.ObjectID() != id || capRO.TenantID() != tenantAlice ||
			capRO.Version() != 1 || capRO.Generation() != 1 || !capRO.CanRead() || capRO.CanWrite() {
			t.Fatalf("capability binding mismatch: %+v", capRO)
		}
		got, err := h.Read(capRO)
		if err != nil {
			t.Fatalf("read %s: %v", id, err)
		}
		if !bytes.Equal(got, pt) {
			t.Fatalf("plaintext mismatch on %s", id)
		}
	}

	if _, err := h.Open(alice, "obj-0", 0); !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("open with empty perms: want ErrAccessDenied, got %v", err)
	}

	// 返回的明文是副本，调用方修改不会污染宿主缓存。
	capRO, _ := h.Open(alice, "obj-0", PermRead)
	got, _ := h.Read(capRO)
	got[0] = 0xFF
	again, err := h.Read(capRO)
	if err != nil || !bytes.Equal(again, []byte{0x01}) {
		t.Fatalf("plaintext buffer aliases cache: %v %v", again, err)
	}
}

// TestDuplicateNarrowsOnly 验证复制句柄只能收窄权限、不能放大，绑定与代次原样继承。
func TestDuplicateNarrowsOnly(t *testing.T) {
	h, _ := newTestHost(t)
	alice := Principal{TenantID: tenantAlice}
	if err := h.Create(alice, "doc", []byte("secret")); err != nil {
		t.Fatal(err)
	}
	full, err := h.Open(alice, "doc", PermRead|PermWrite)
	if err != nil {
		t.Fatal(err)
	}

	ro := full.Duplicate(Narrow{DropWrite: true})
	if ro.CanWrite() || !ro.CanRead() {
		t.Fatal("narrow did not drop write")
	}
	if ro.ObjectID() != full.ObjectID() || ro.Generation() != full.Generation() ||
		ro.Version() != full.Version() || ro.TenantID() != full.TenantID() {
		t.Fatal("duplicate changed object binding or generation")
	}

	none := ro.Duplicate(Narrow{DropRead: true})
	if none.CanRead() || none.CanWrite() {
		t.Fatal("narrowed capability still holds permissions")
	}

	// 再次复制也不可能把权限“复制回来”。
	stillNone := none.Duplicate(Narrow{})
	if stillNone.CanRead() || stillNone.CanWrite() {
		t.Fatal("duplicate escalated permissions")
	}

	h.Locks().LockExclusive("doc", tenantAlice)
	defer h.Locks().Unlock("doc", tenantAlice)
	if _, err := h.Read(none); !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("read without perm while holding lock: want ErrAccessDenied, got %v", err)
	}
	if err := h.Replace(alice, ro, []byte("x")); !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("replace without write perm: want ErrAccessDenied, got %v", err)
	}
}

// TestForgeLockHolderNoUnauthorizedRead 是核心安全测试：
// 伪造锁持有者能拿到锁（锁不做身份授权），但锁不增加任何读取权限。
func TestForgeLockHolderNoUnauthorizedRead(t *testing.T) {
	h, _ := newTestHost(t)
	alice := Principal{TenantID: tenantAlice}
	mallory := Principal{TenantID: tenantMallory}
	const id = "victim-doc"
	plaintext := []byte("alice-only-secret")
	if err := h.Create(alice, id, plaintext); err != nil {
		t.Fatal(err)
	}

	// 攻击者先让受害者产生一条解包缓存，再尝试通过锁途径命中它。
	victimCap, err := h.Open(alice, id, PermRead)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Read(victimCap); err != nil {
		t.Fatal(err)
	}
	if h.cacheSize() != 1 {
		t.Fatalf("expected one cache entry, got %d", h.cacheSize())
	}

	// 伪造锁持有者：锁管理器不校验身份，攻击者确实能抢到共享锁和排他锁。
	if !h.Locks().TryLockShared(id, "forged-holder") {
		t.Fatal("shared lock should be acquirable regardless of identity")
	}
	if err := h.Locks().Unlock(id, "forged-holder"); err != nil {
		t.Fatal(err)
	}
	if !h.Locks().TryLockExclusive(id, tenantMallory) {
		t.Fatal("forged exclusive lock should be acquirable; locks carry no authorization")
	}
	t.Cleanup(func() {
		_ = h.Locks().Unlock(id, tenantMallory)
	})

	// 路径一：攻击者走正常可信打开，因跨租户被拒，锁状态不影响结论。
	if _, err := h.Open(mallory, id, PermRead|PermWrite); !errors.Is(err, ErrWrongTenant) {
		t.Fatalf("cross-tenant open: want ErrWrongTenant, got %v", err)
	}

	// 路径二：同包内模拟跨租户伪造能力句柄（包外无法构造非导出字段），
	// 即便持有排他锁，读路径仍在授权检查处失败，且不会触达解包缓存。
	forged := Capability{
		objectID:   id,
		version:    1,
		tenantID:   tenantMallory,
		generation: 1,
		perms:      PermRead,
	}
	if _, err := h.Read(forged); !errors.Is(err, ErrWrongTenant) {
		t.Fatalf("forged cap read under exclusive lock: want ErrWrongTenant, got %v", err)
	}
	forgedWrite := forged
	forgedWrite.perms = PermWrite
	if err := h.Replace(mallory, forgedWrite, []byte("pwn")); !errors.Is(err, ErrWrongTenant) {
		t.Fatalf("forged cap replace: want ErrWrongTenant, got %v", err)
	}

	// 零越权读取的最终证据：缓存仍只有受害者自己的条目，攻击者键从未出现。
	if h.hasCacheEntry(id, tenantMallory, 1) {
		t.Fatal("attacker obtained an unpack-cache entry")
	}
	if h.cacheSize() != 1 {
		t.Fatalf("cache changed after unauthorized attempts: %d entries", h.cacheSize())
	}
	got, err := h.Read(victimCap)
	if err != nil || !bytes.Equal(got, plaintext) {
		t.Fatalf("victim read altered: %v %v", got, err)
	}
}

// TestGenerationRecheckedOnEveryRead 验证替换推进代次后，旧句柄每次读取都被拒。
func TestGenerationRecheckedOnEveryRead(t *testing.T) {
	h, _ := newTestHost(t)
	alice := Principal{TenantID: tenantAlice}
	const id = "gen-doc"
	if err := h.Create(alice, id, []byte("v1")); err != nil {
		t.Fatal(err)
	}
	capV1, err := h.Open(alice, id, PermRead|PermWrite)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Read(capV1); err != nil {
		t.Fatal(err)
	}

	h.Locks().LockExclusive(id, tenantAlice)
	if err := h.Replace(alice, capV1, []byte("v2")); err != nil {
		t.Fatalf("replace: %v", err)
	}
	h.Locks().Unlock(id, tenantAlice)

	// 旧代次缓存必须被清空，旧句柄连续读取都失败，而不是只失败第一次。
	if h.cacheSize() != 0 {
		t.Fatalf("cache not purged after replace: %d", h.cacheSize())
	}
	for i := 0; i < 3; i++ {
		if _, err := h.Read(capV1); !errors.Is(err, ErrStaleCapability) {
			t.Fatalf("read %d with stale cap: want ErrStaleCapability, got %v", i, err)
		}
	}

	capV2, err := h.Open(alice, id, PermRead)
	if err != nil {
		t.Fatal(err)
	}
	got, err := h.Read(capV2)
	if err != nil || !bytes.Equal(got, []byte("v2")) {
		t.Fatalf("fresh cap read: %v %v", got, err)
	}
	_, version, generation := h.objectSnapshot(t, id)
	if version != 2 || generation != 2 {
		t.Fatalf("version/generation = %d/%d, want 2/2", version, generation)
	}
}

// TestLockUpgradeConflict 验证锁升级语义：只控制冲突，与权限无关。
func TestLockUpgradeConflict(t *testing.T) {
	lm := NewLockManager()
	const id, holderA, holderB, holderC = "o", "a", "b", "c"

	lm.LockShared(id, holderA)
	lm.LockShared(id, holderB)
	if lm.TryLockExclusive(id, holderC) {
		t.Fatal("exclusive lock granted while shared locks held")
	}
	if err := lm.Upgrade(id, holderC); !errors.Is(err, ErrNotLocked) {
		t.Fatalf("upgrade without shared lock: want ErrNotLocked, got %v", err)
	}

	// B 释放后 A 才能升级；升级等待期间 C 的排他尝试必须失败。
	done := make(chan error, 1)
	go func() { done <- lm.Upgrade(id, holderA) }()
	time.Sleep(20 * time.Millisecond)
	if lm.TryLockExclusive(id, holderC) {
		t.Fatal("exclusive lock granted during pending upgrade")
	}
	if err := lm.Unlock(id, holderB); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("upgrade: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("upgrade did not complete after peer release")
	}
	if !lm.HoldsExclusive(id, holderA) {
		t.Fatal("upgrade did not produce exclusive lock")
	}
	if lm.TryLockShared(id, holderB) {
		t.Fatal("shared lock granted while exclusive lock held")
	}
	if err := lm.Unlock(id, holderA); err != nil {
		t.Fatal(err)
	}
	if !lm.TryLockShared(id, holderB) {
		t.Fatal("shared lock blocked after exclusive release")
	}
	if err := lm.Unlock(id, holderB); err != nil {
		t.Fatal(err)
	}

	// 排他锁重入深度：两次加锁必须对称解锁两次，期间他人不得插入。
	lm.LockExclusive(id, holderA)
	lm.LockExclusive(id, holderA)
	if lm.TryLockExclusive(id, holderB) {
		t.Fatal("second holder entered a reentrant exclusive lock")
	}
	if err := lm.Unlock(id, holderA); err != nil {
		t.Fatal(err)
	}
	if !lm.HoldsExclusive(id, holderA) {
		t.Fatal("exclusive lock released before reentrancy depth reached zero")
	}
	if err := lm.Unlock(id, holderA); err != nil {
		t.Fatal(err)
	}
	if lm.HoldsExclusive(id, holderA) {
		t.Fatal("exclusive lock still held after balanced unlocks")
	}
	if err := lm.Unlock(id, holderA); !errors.Is(err, ErrNotLocked) {
		t.Fatalf("over-unlock: want ErrNotLocked, got %v", err)
	}
}

// TestConcurrentReplaceAndRead 在 -race 下验证并发替换/读取的代次不变量：
// 每次读取核对同一能力代次：旧句柄在替换后必须收到 ErrStaleCapability，
// 新句柄读到的明文必然对应某个真实存在的代次，绝不串数据。
func TestConcurrentReplaceAndRead(t *testing.T) {
	h, _ := newTestHost(t)
	alice := Principal{TenantID: tenantAlice}
	const id = "race-doc"
	const rounds = 200
	if err := h.Create(alice, id, []byte("round-0")); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	var staleCount, okCount int64
	startWrites := make(chan struct{})

	// 旧句柄必须在任何替换发生之前签发，确保它固定绑定初始代次。
	staleCap, err := h.Open(alice, id, PermRead)
	if err != nil {
		t.Fatalf("stale reader open: %v", err)
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		<-startWrites
		for r := 0; r < rounds; r++ {
			h.Locks().LockExclusive(id, tenantAlice)
			writeCap, err := h.Open(alice, id, PermRead|PermWrite)
			if err != nil {
				t.Errorf("writer open: %v", err)
				h.Locks().Unlock(id, tenantAlice)
				return
			}
			next := fmt.Sprintf("round-%d", r+1)
			if err := h.Replace(alice, writeCap, []byte(next)); err != nil {
				t.Errorf("replace round %d: %v", r, err)
				h.Locks().Unlock(id, tenantAlice)
				return
			}
			h.Locks().Unlock(id, tenantAlice)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		// 旧句柄固定在初始代次，替换发生后每次读取都必须被代次核对拒绝。
		for r := 0; r < rounds*2; r++ {
			if pt, err := h.Read(staleCap); err == nil {
				if !bytes.Equal(pt, []byte("round-0")) {
					t.Errorf("stale cap returned foreign-generation data: %q", pt)
					return
				}
			} else if !errors.Is(err, ErrStaleCapability) {
				t.Errorf("stale cap unexpected err: %v", err)
				return
			} else {
				atomic.AddInt64(&staleCount, 1)
			}

			// 新句柄：每次现开现读，明文必须是宿主里真实存在的某代次内容。
			freshCap, err := h.Open(alice, id, PermRead)
			if err != nil {
				t.Errorf("fresh reader open: %v", err)
				return
			}
			pt, err := h.Read(freshCap)
			switch {
			case errors.Is(err, ErrStaleCapability):
				// Open 之后、Read 之前恰好发生替换：代次核对正确拦截。
				atomic.AddInt64(&staleCount, 1)
			case err != nil:
				t.Errorf("reader unexpected err: %v", err)
				return
			default:
				var n int
				if _, err := fmt.Sscanf(string(pt), "round-%d", &n); err != nil || n < 0 || n > rounds {
					t.Errorf("read returned content from no real generation: %q", string(pt))
					return
				}
				atomic.AddInt64(&okCount, 1)
			}
		}
	}()

	close(startWrites)
	wg.Wait()
	if staleCount == 0 {
		t.Fatal("old capability never observed a stale generation; test did not exercise the race")
	}
	if okCount == 0 {
		t.Fatal("fresh capability never completed a read")
	}

	// 最终一致性：新句柄读到最后一轮数据，缓存仅含当前代次。
	finalCap, err := h.Open(alice, id, PermRead)
	if err != nil {
		t.Fatal(err)
	}
	pt, err := h.Read(finalCap)
	if err != nil || !bytes.Equal(pt, []byte(fmt.Sprintf("round-%d", rounds))) {
		t.Fatalf("final state wrong: %q err=%v", pt, err)
	}
	if h.cacheSize() != 1 {
		t.Fatalf("cache entries = %d, want exactly current generation", h.cacheSize())
	}
	_, version, generation := h.objectSnapshot(t, id)
	if version != int64(rounds+1) || generation != int64(rounds+1) {
		t.Fatalf("version/generation = %d/%d", version, generation)
	}
}

// TestSuccessfulTransferReBindsTenant 验证成功移交：双签票据、重封装、代次推进与缓存清理。
func TestSuccessfulTransferReBindsTenant(t *testing.T) {
	h, kr := newTestHost(t)
	alice := Principal{TenantID: tenantAlice}
	bob := Principal{TenantID: tenantBob}
	const id = "handoff-doc"
	secret := []byte("classified-small-object")
	if err := h.Create(alice, id, secret); err != nil {
		t.Fatal(err)
	}
	oldAliceCap, _ := h.Open(alice, id, PermRead)
	if _, err := h.Read(oldAliceCap); err != nil {
		t.Fatal(err)
	}

	tk := makeTicket(t, h, kr, id, tenantAlice, tenantBob)
	h.Locks().LockExclusive(id, tenantAlice)
	if err := h.Transfer(alice, tk); err != nil {
		h.Locks().Unlock(id, tenantAlice)
		t.Fatalf("transfer: %v", err)
	}
	h.Locks().Unlock(id, tenantAlice)

	owner, version, generation := h.objectSnapshot(t, id)
	if owner != tenantBob || version != 2 || generation != 2 {
		t.Fatalf("post-transfer state = %s v%d g%d", owner, version, generation)
	}
	// 源租户的解包缓存必须被清掉，旧句柄立即失效。
	if h.cacheSize() != 0 || h.hasCacheEntry(id, tenantAlice, 1) {
		t.Fatal("source plaintext cache survived transfer")
	}
	if _, err := h.Read(oldAliceCap); !errors.Is(err, ErrWrongTenant) {
		t.Fatalf("old owner read: want ErrWrongTenant, got %v", err)
	}
	if _, err := h.Open(alice, id, PermRead); !errors.Is(err, ErrWrongTenant) {
		t.Fatalf("old owner reopen: want ErrWrongTenant, got %v", err)
	}

	// 目标租户用自己的密钥解包成功，证明宿主完成了重新封装。
	bobCap, err := h.Open(bob, id, PermRead)
	if err != nil {
		t.Fatal(err)
	}
	pt, err := h.Read(bobCap)
	if err != nil || !bytes.Equal(pt, secret) {
		t.Fatalf("new owner read: %q err=%v", pt, err)
	}
	if !h.hasCacheEntry(id, tenantBob, 2) {
		t.Fatal("expected new owner cache entry under new generation")
	}
}

// TestFailedTransfersDoNotLeak 枚举所有失败移交路径，逐条验证：
// 对象归属/版本/代次不变、解包缓存不新增、目标租户永远拿不到明文。
func TestFailedTransfersDoNotLeak(t *testing.T) {
	type seed struct {
		h  *Host
		kr *keyring
		id string
		tk Ticket
	}

	setup := func(t *testing.T) seed {
		t.Helper()
		h, kr := newTestHost(t)
		alice := Principal{TenantID: tenantAlice}
		const id = "leak-doc"
		if err := h.Create(alice, id, []byte("do-not-leak")); err != nil {
			t.Fatal(err)
		}
		aliceCap, _ := h.Open(alice, id, PermRead)
		if _, err := h.Read(aliceCap); err != nil {
			t.Fatal(err)
		}
		return seed{h: h, kr: kr, id: id, tk: makeTicket(t, h, kr, id, tenantAlice, tenantBob)}
	}

	withLock := func(h *Host, id string, fn func()) {
		h.Locks().LockExclusive(id, tenantAlice)
		defer h.Locks().Unlock(id, tenantAlice)
		fn()
	}

	alice := Principal{TenantID: tenantAlice}
	mallory := Principal{TenantID: tenantMallory}

	t.Run("missing source signature", func(t *testing.T) {
		s := setup(t)
		s.tk.FromSignature = nil
		withLock(s.h, s.id, func() {
			if err := s.h.Transfer(alice, s.tk); !errors.Is(err, ErrSignature) {
				t.Fatalf("want ErrSignature, got %v", err)
			}
		})
		assertNoLeak(t, s, 1, 1, 1)
	})

	t.Run("missing target signature", func(t *testing.T) {
		s := setup(t)
		s.tk.ToSignature = nil
		withLock(s.h, s.id, func() {
			if err := s.h.Transfer(alice, s.tk); !errors.Is(err, ErrSignature) {
				t.Fatalf("want ErrSignature, got %v", err)
			}
		})
		assertNoLeak(t, s, 1, 1, 1)
	})

	t.Run("forged source signature", func(t *testing.T) {
		s := setup(t)
		// 攻击者用自己的私钥重签源签名，目标签名保持 Bob 的真实签名。
		s.tk.FromSignature = ed25519.Sign(s.kr.priv[tenantMallory], s.tk.signingBytes())
		withLock(s.h, s.id, func() {
			if err := s.h.Transfer(alice, s.tk); !errors.Is(err, ErrSignature) {
				t.Fatalf("want ErrSignature, got %v", err)
			}
		})
		assertNoLeak(t, s, 1, 1, 1)
	})

	t.Run("target binding replaced", func(t *testing.T) {
		s := setup(t)
		// 票据原本绑定 Bob，攻击者把目标改成自己并由自己签目标签名；
		// 源签名覆盖的规范字节流随之失配，宿主必须拒绝。
		s.tk.ToTenant = tenantMallory
		s.tk.ToSignature = ed25519.Sign(s.kr.priv[tenantMallory], s.tk.signingBytes())
		withLock(s.h, s.id, func() {
			if err := s.h.Transfer(alice, s.tk); !errors.Is(err, ErrSignature) {
				t.Fatalf("want ErrSignature, got %v", err)
			}
		})
		assertNoLeak(t, s, 1, 1, 1)
	})

	t.Run("stale version and generation", func(t *testing.T) {
		s := setup(t)
		aliceCap, _ := s.h.Open(alice, s.id, PermRead|PermWrite)
		s.h.Locks().LockExclusive(s.id, tenantAlice)
		if err := s.h.Replace(alice, aliceCap, []byte("rotated")); err != nil {
			t.Fatal(err)
		}
		s.h.Locks().Unlock(s.id, tenantAlice)
		// 票据固化的是替换前的版本/代次，此刻已过期。
		withLock(s.h, s.id, func() {
			if err := s.h.Transfer(alice, s.tk); !errors.Is(err, ErrStaleCapability) {
				t.Fatalf("want ErrStaleCapability, got %v", err)
			}
		})
		assertNoLeak(t, s, 2, 2, 0)
	})

	t.Run("replay", func(t *testing.T) {
		s := setup(t)
		withLock(s.h, s.id, func() {
			if err := s.h.Transfer(alice, s.tk); err != nil {
				t.Fatalf("first transfer: %v", err)
			}
		})
		// 同一票据二次提交：对象已属于 Bob，源租户校验即失败，且 nonce 已消费。
		s.h.Locks().LockExclusive(s.id, tenantAlice)
		err := s.h.Transfer(alice, s.tk)
		s.h.Locks().Unlock(s.id, tenantAlice)
		if !errors.Is(err, ErrNotLocked) && !errors.Is(err, ErrWrongTenant) && !errors.Is(err, ErrTicketReplay) {
			t.Fatalf("replay want lock/tenant/nonce rejection, got %v", err)
		}
		bobCap, err := s.h.Open(Principal{TenantID: tenantBob}, s.id, PermRead)
		if err != nil {
			t.Fatal(err)
		}
		if pt, err := s.h.Read(bobCap); err != nil || !bytes.Equal(pt, []byte("do-not-leak")) {
			t.Fatalf("object corrupted after replay attempt: %q %v", pt, err)
		}
	})

	t.Run("expired ticket", func(t *testing.T) {
		s := setup(t)
		s.tk.ExpiresAt = s.h.now().Add(-time.Second)
		withLock(s.h, s.id, func() {
			if err := s.h.Transfer(alice, s.tk); !errors.Is(err, ErrTicketExpired) {
				t.Fatalf("want ErrTicketExpired, got %v", err)
			}
		})
		assertNoLeak(t, s, 1, 1, 1)
	})

	t.Run("missing exclusive lock", func(t *testing.T) {
		s := setup(t)
		// 不获取任何锁：锁是写路径的前置条件，缺失直接拒绝。
		if err := s.h.Transfer(alice, s.tk); !errors.Is(err, ErrNotLocked) {
			t.Fatalf("want ErrNotLocked, got %v", err)
		}
		assertNoLeak(t, s, 1, 1, 1)
	})

	t.Run("wrong caller principal", func(t *testing.T) {
		s := setup(t)
		// Mallory 即便拿到票据内容，也不是票据上的源租户。
		s.h.Locks().LockExclusive(s.id, tenantAlice)
		err := s.h.Transfer(mallory, s.tk)
		s.h.Locks().Unlock(s.id, tenantAlice)
		if !errors.Is(err, ErrWrongTenant) {
			t.Fatalf("want ErrWrongTenant, got %v", err)
		}
		assertNoLeak(t, s, 1, 1, 1)
	})
}

// assertNoLeak 汇总失败移交后的全部零泄漏断言。
func assertNoLeak(t *testing.T, s struct {
	h  *Host
	kr *keyring
	id string
	tk Ticket
}, wantVersion, wantGeneration, wantCacheSize int64) {
	t.Helper()
	owner, version, generation := s.h.objectSnapshot(t, s.id)
	if owner != tenantAlice || version != wantVersion || generation != wantGeneration {
		t.Fatalf("state mutated on failed transfer: owner=%s v%d g%d", owner, version, generation)
	}
	// 解包缓存仍只有源租户、代次 1 的条目；目标/攻击者条目从未产生。
	if int64(s.h.cacheSize()) != wantCacheSize {
		t.Fatalf("cache size = %d after failed transfer, want %d", s.h.cacheSize(), wantCacheSize)
	}
	if s.h.hasCacheEntry(s.id, tenantBob, 1) || s.h.hasCacheEntry(s.id, tenantMallory, 1) {
		t.Fatal("failed transfer produced a foreign-tenant cache entry")
	}
	// 目标与攻击者均无法通过可信打开读到对象。
	for _, p := range []Principal{{TenantID: tenantBob}, {TenantID: tenantMallory}} {
		if _, err := s.h.Open(p, s.id, PermRead); !errors.Is(err, ErrWrongTenant) {
			t.Fatalf("foreign open: want ErrWrongTenant, got %v", err)
		}
	}
}

// TestConcurrentTransferHandoff 在移交与跨租户攻击读取并发下验证代次与缓存隔离。
func TestConcurrentTransferHandoff(t *testing.T) {
	h, kr := newTestHost(t)
	alice := Principal{TenantID: tenantAlice}
	mallory := Principal{TenantID: tenantMallory}
	const id = "pingpong-doc"
	if err := h.Create(alice, id, []byte("ping-0")); err != nil {
		t.Fatal(err)
	}

	const rounds = 30
	var wg sync.WaitGroup

	// 攻击者在整个移交过程中不断尝试跨租户打开与伪造句柄读取。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < rounds*8; i++ {
			if cap, err := h.Open(mallory, id, PermRead); err == nil {
				t.Errorf("attacker obtained capability: %+v", cap)
				return
			}
			forged := Capability{
				objectID:   id,
				version:    1,
				tenantID:   tenantMallory,
				generation: 1,
				perms:      PermRead,
			}
			if pt, err := h.Read(forged); err == nil {
				t.Errorf("attacker forged read succeeded: %q", pt)
				return
			}
		}
	}()

	// 所有者在 Alice/Bob 之间反复移交，每次都生成新的双签票据。
	wg.Add(1)
	go func() {
		defer wg.Done()
		from, to := tenantAlice, tenantBob
		fromPrincipal := alice
		for r := 0; r < rounds; r++ {
			tk := makeTicket(t, h, kr, id, from, to)
			h.Locks().LockExclusive(id, from)
			if err := h.Transfer(fromPrincipal, tk); err != nil {
				t.Errorf("transfer round %d: %v", r, err)
				h.Locks().Unlock(id, from)
				return
			}
			h.Locks().Unlock(id, from)
			from, to = to, from
			fromPrincipal = Principal{TenantID: from}
		}
	}()

	wg.Wait()

	owner, version, generation := h.objectSnapshot(t, id)
	if version != int64(rounds+1) || generation != int64(rounds+1) {
		t.Fatalf("version/generation = %d/%d, want %d", version, generation, rounds+1)
	}
	// 缓存里只允许出现当前所有者的当前代次条目。
	if h.cacheSize() > 1 {
		t.Fatalf("cache entries = %d, want at most current owner entry", h.cacheSize())
	}
	if h.hasCacheEntry(id, tenantMallory, 1) {
		t.Fatal("attacker cache entry appeared during concurrent transfer")
	}
	// 当前所有者可正常读取，且每代明文与对象实际状态自洽。
	ownerPrincipal := Principal{TenantID: owner}
	cap, err := h.Open(ownerPrincipal, id, PermRead)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Read(cap); err != nil {
		t.Fatalf("current owner cannot read after churn: %v", err)
	}
}
