package netsec

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func secret(n int, fill byte) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = fill
	}
	return b
}

func allZero(b []byte) bool {
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRegisterAndAcquire(t *testing.T) {
	g := NewGraph()
	rootData := secret(16, 0x11)
	if err := g.RegisterSource("root", 1, rootData); err != nil {
		t.Fatalf("RegisterSource: %v", err)
	}
	if err := g.RegisterSource("root", 1, rootData); !errors.Is(err, ErrObjectExists) {
		t.Fatalf("dup register = %v, want ErrObjectExists", err)
	}

	h, err := g.Acquire("root")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if !bytes.Equal(h.Bytes(), rootData) || h.ID() != "root" || h.Gen() != 1 || h.Len() != 16 {
		t.Fatalf("handle metadata mismatch: %+v", h)
	}
	h.Release()
	if !h.Released() {
		t.Fatal("Release should mark handle released")
	}
	h.Release()

	if _, err := g.Acquire("missing"); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("Acquire missing = %v, want ErrObjectNotFound", err)
	}

	copyData := secret(16, 0x22)
	if err := g.RegisterCopy("read-cache", "root", copyData); err != nil {
		t.Fatalf("RegisterCopy: %v", err)
	}
	if err := g.RegisterCopy("bad-copy", "missing", nil); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("copy of missing source = %v", err)
	}

	if err := g.RegisterDerived("d1", 1, secret(8, 0x33), []ObjectID{"root", "read-cache"}); err != nil {
		t.Fatalf("RegisterDerived: %v", err)
	}
	if err := g.RegisterDerived("d2", 1, nil, nil); !errors.Is(err, ErrInvalidSources) {
		t.Fatalf("derived without sources = %v, want ErrInvalidSources", err)
	}
	if err := g.RegisterDerived("d3", 1, nil, []ObjectID{"root", "nope"}); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("derived with missing source = %v", err)
	}
	if err := g.RegisterDerived("d4", 1, nil, []ObjectID{"root", "root"}); err != nil {
		t.Fatalf("dedup sources: %v", err)
	}

	if g.Len() != 4 {
		t.Fatalf("Len = %d, want 4", g.Len())
	}
}

func TestRevokeClosureWipesAllCopies(t *testing.T) {
	g := NewGraph()
	rootData := secret(8, 0xAB)
	readData := secret(8, 0xAB)
	indexData := secret(8, 0xAB)
	derivedData := secret(4, 0xAB)

	must(t, g.RegisterSource("root", 1, rootData))
	must(t, g.RegisterCopy("read", "root", readData))
	must(t, g.RegisterCopy("index", "root", indexData))
	must(t, g.RegisterDerived("frag", 1, derivedData, []ObjectID{"read", "index"}))

	otherData := secret(4, 0xCD)
	must(t, g.RegisterSource("other", 1, otherData))

	if err := g.Revoke("root"); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	for id, want := range map[ObjectID]bool{
		"root": true, "read": true, "index": true, "frag": true, "other": false,
	} {
		if got := g.IsRevoked(id); got != want {
			t.Errorf("IsRevoked(%q) = %v, want %v", id, got, want)
		}
		if got := g.IsWiped(id); got != want {
			t.Errorf("IsWiped(%q) = %v, want %v", id, got, want)
		}
	}

	for name, data := range map[string][]byte{
		"root": rootData, "read": readData, "index": indexData, "frag": derivedData,
	} {
		if !allZero(data) {
			t.Errorf("%s data not zeroized: %v", name, data)
		}
	}
	if allZero(otherData) {
		t.Error("unrelated object was wiped")
	}

	if _, err := g.Acquire("root"); !errors.Is(err, ErrRevoked) {
		t.Fatalf("Acquire after revoke = %v, want ErrRevoked", err)
	}
	if len(g.Wiped()) != 4 {
		t.Fatalf("Wiped snapshot = %v", g.Wiped())
	}
}

func TestSharedDerivedInvalidatedByAnySource(t *testing.T) {
	g := NewGraph()
	must(t, g.RegisterSource("a", 1, secret(4, 0x01)))
	must(t, g.RegisterSource("b", 2, secret(4, 0x02)))
	shared := secret(4, 0x03)
	must(t, g.RegisterDerived("shared", 1, shared, []ObjectID{"a", "b"}))

	must(t, g.Revoke("b"))
	if !g.IsRevoked("shared") || !g.IsWiped("shared") || !allZero(shared) {
		t.Fatal("shared derivative must fail when any source is revoked")
	}
	if g.IsRevoked("a") {
		t.Fatal("revoking one source must not touch the other source")
	}

	if err := g.RegisterCopy("late-copy", "shared", nil); !errors.Is(err, ErrRevoked) {
		t.Fatalf("register on revoked object = %v, want ErrRevoked", err)
	}
}

func TestTransitiveClosureDiamond(t *testing.T) {
	g := NewGraph()
	blobs := map[ObjectID][]byte{}
	must(t, g.RegisterSource("root", 1, secret(2, 0x7)))
	for _, id := range []ObjectID{"left", "right"} {
		blobs[id] = secret(2, 0x7)
		must(t, g.RegisterCopy(id, "root", blobs[id]))
	}
	blobs["merge"] = secret(2, 0x7)
	must(t, g.RegisterDerived("merge", 1, blobs["merge"], []ObjectID{"left", "right"}))
	blobs["leaf"] = secret(2, 0x7)
	must(t, g.RegisterDerived("leaf", 1, blobs["leaf"], []ObjectID{"merge"}))

	must(t, g.Revoke("root"))
	for id := range blobs {
		if !g.IsWiped(id) || !allZero(blobs[id]) {
			t.Errorf("object %q survived root revocation: wiped=%v", id, g.IsWiped(id))
		}
	}
	if !g.IsWiped("root") {
		t.Error("root itself must be wiped")
	}
}

func TestKeyGenerationIsolation(t *testing.T) {
	g := NewGraph()
	gen1 := secret(4, 0x1)
	gen1Copy := secret(4, 0x1)
	gen2 := secret(4, 0x2)
	must(t, g.RegisterSource("src-g1", 1, gen1))
	must(t, g.RegisterCopy("copy-g1", "src-g1", gen1Copy))
	must(t, g.RegisterSource("src-g2", 2, gen2))

	if err := g.RevokeGeneration(1); err != nil {
		t.Fatalf("RevokeGeneration: %v", err)
	}
	if err := g.RevokeGeneration(99); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("revoke unknown gen = %v, want ErrObjectNotFound", err)
	}

	if !allZero(gen1) || !allZero(gen1Copy) {
		t.Error("generation 1 data not zeroized")
	}
	if allZero(gen2) {
		t.Error("generation 2 object was wiped by generation 1 revocation")
	}
	if g.IsRevoked("src-g2") {
		t.Error("generation 2 object was marked revoked")
	}

	if err := g.RegisterSource("src-g3", 3, secret(2, 0x3)); err != nil {
		t.Fatalf("register after generation revoke: %v", err)
	}
}

func TestRevokeWaitsForHolders(t *testing.T) {
	var wipeCalls atomic.Int32
	g := NewGraph(WithWiper(func(id ObjectID, gen KeyGen, data []byte) {
		wipeCalls.Add(1)
		DefaultWiper(id, gen, data)
	}))

	data := secret(8, 0x9)
	must(t, g.RegisterSource("root", 1, data))
	h1, err := g.Acquire("root")
	if err != nil {
		t.Fatal(err)
	}
	h2, err := g.Acquire("root")
	if err != nil {
		t.Fatal(err)
	}

	revoked := make(chan struct{})
	go func() {
		if err := g.Revoke("root"); err != nil {
			t.Errorf("Revoke: %v", err)
		}
		close(revoked)
	}()

	// 先等撤销协程拿到锁并完成“立即不可用”标记。
	marked := time.Now().Add(2 * time.Second)
	for time.Now().Before(marked) && !g.IsRevoked("root") {
		time.Sleep(time.Millisecond)
	}
	if !g.IsRevoked("root") {
		t.Fatal("object should be marked unavailable immediately")
	}

	// 标记已生效，但句柄未释放前不得擦除。
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if g.IsWiped("root") || wipeCalls.Load() != 0 {
			t.Fatal("wiped before holders exited")
		}
		if allZero(data) || allZero(h1.Bytes()) {
			t.Fatal("holder observed zeroized bytes while still using the object")
		}
		time.Sleep(time.Millisecond)
	}

	if _, err := g.Acquire("root"); !errors.Is(err, ErrRevoked) {
		t.Fatalf("Acquire during pending revoke = %v", err)
	}

	h1.Release()
	select {
	case <-revoked:
		t.Fatal("revoke returned while a holder still exists")
	case <-time.After(20 * time.Millisecond):
	}
	if allZero(h2.Bytes()) {
		t.Fatal("remaining holder observed premature zeroization")
	}

	h2.Release()
	select {
	case <-revoked:
	case <-time.After(2 * time.Second):
		t.Fatal("Revoke did not complete after holders exited")
	}
	if !allZero(data) || wipeCalls.Load() != 1 {
		t.Fatalf("data=%v wipeCalls=%d", data, wipeCalls.Load())
	}
}

func TestRegistrationRejectedAfterRevoke(t *testing.T) {
	g := NewGraph()
	must(t, g.RegisterSource("root", 1, secret(2, 0x1)))
	must(t, g.Revoke("root"))

	if err := g.RegisterCopy("c", "root", nil); !errors.Is(err, ErrRevoked) {
		t.Fatalf("copy after revoke = %v", err)
	}
	if err := g.RegisterDerived("d", 1, nil, []ObjectID{"root"}); !errors.Is(err, ErrRevoked) {
		t.Fatalf("derived after revoke = %v", err)
	}
	if err := g.Revoke("ghost"); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("revoke unknown = %v", err)
	}
	if err := g.Revoke("root"); err != nil {
		t.Fatalf("idempotent revoke: %v", err)
	}
}

func TestConcurrentDerivationAndRevocation(t *testing.T) {
	const (
		rootCount  = 8
		iterations = 300
		fill       = byte(0xAB)
	)

	var (
		wipeMu  sync.Mutex
		wipeSet = make(map[ObjectID]int)
	)
	// 擦除时首字节必须仍是哨兵值：若已为 0，说明发生了重复擦除或提前清零。
	g := NewGraph(WithWiper(func(id ObjectID, gen KeyGen, data []byte) {
		wipeMu.Lock()
		wipeSet[id]++
		wipeMu.Unlock()
		if len(data) > 0 && data[0] != fill {
			t.Errorf("object %s zeroized before safe point or wiped twice", id)
		}
		DefaultWiper(id, gen, data)
	}))

	var blobsMu sync.Mutex
	blobs := make(map[ObjectID][]byte)
	keep := func(id ObjectID, data []byte) {
		blobsMu.Lock()
		blobs[id] = data
		blobsMu.Unlock()
	}

	for i := 0; i < rootCount; i++ {
		id := ObjectID(fmt.Sprintf("root-%d", i))
		data := secret(16, fill)
		must(t, g.RegisterSource(id, KeyGen(i%3)+1, data))
		keep(id, data)
	}

	var copySeq, derivedSeq atomic.Int64

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// 副本派生协程：只有登记成功的对象才进入核对集合。
	wg.Add(1)
	go func() {
		defer wg.Done()
		r := rand.New(rand.NewSource(1001))
		for i := 0; i < iterations; i++ {
			source := ObjectID(fmt.Sprintf("root-%d", r.Intn(rootCount)))
			data := secret(8, fill)
			id := ObjectID(fmt.Sprintf("copy-%d", copySeq.Add(1)))
			if err := g.RegisterCopy(id, source, data); err == nil {
				keep(id, data)
			}
		}
	}()

	// 共享派生协程：随机绑定 1~3 个来源，覆盖多来源失效路径。
	wg.Add(1)
	go func() {
		defer wg.Done()
		r := rand.New(rand.NewSource(2002))
		for i := 0; i < iterations; i++ {
			srcCount := 1 + r.Intn(3)
			sources := make([]ObjectID, 0, srcCount)
			for j := 0; j < srcCount; j++ {
				sources = append(sources, ObjectID(fmt.Sprintf("root-%d", r.Intn(rootCount))))
			}
			data := secret(6, fill)
			id := ObjectID(fmt.Sprintf("derived-%d", derivedSeq.Add(1)))
			if err := g.RegisterDerived(id, KeyGen(1+r.Intn(3)), data, sources); err == nil {
				keep(id, data)
			}
		}
	}()

	// 读者协程：句柄存活期间观察到的字节必须完整，绝不允许读到半清零数据。
	for w := 0; w < 6; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			r := rand.New(rand.NewSource(seed))
			for {
				select {
				case <-stop:
					return
				default:
				}
				id := ObjectID(fmt.Sprintf("root-%d", r.Intn(rootCount)))
				h, err := g.Acquire(id)
				if err != nil {
					if !errors.Is(err, ErrRevoked) {
						t.Errorf("unexpected Acquire error: %v", err)
					}
					continue
				}
				b := h.Bytes()
				if len(b) > 0 && b[0] != fill {
					t.Errorf("reader saw corrupted bytes on %s", id)
				}
				time.Sleep(time.Microsecond)
				h.Release()
			}
		}(3003 + int64(w))
	}

	// 撤销协程：轮流按对象、按密钥代撤销，制造与派生的交错竞态。
	wg.Add(1)
	go func() {
		defer wg.Done()
		r := rand.New(rand.NewSource(4004))
		for i := 0; i < rootCount*2; i++ {
			if r.Intn(2) == 0 {
				id := ObjectID(fmt.Sprintf("root-%d", r.Intn(rootCount)))
				_ = g.Revoke(id)
			} else {
				_ = g.RevokeGeneration(KeyGen(1 + r.Intn(3)))
			}
			time.Sleep(time.Microsecond * 500)
		}
	}()

	// 等派生协程结束后再停读者，保证读者与撤销充分并发。
	copyDone := make(chan struct{})
	go func() {
		wg.Wait()
		close(copyDone)
	}()
	go func() {
		// 派生/撤销协程是 wg 的前 4 个成员；读者也在同一 wg，
		// 因此用 stop 控制读者退出：等待固定窗口后关闭 stop。
		time.Sleep(50 * time.Millisecond)
		close(stop)
	}()
	select {
	case <-copyDone:
	case <-time.After(10 * time.Second):
		t.Fatal("concurrent workers did not finish in time")
	}

	// 终态收敛：把所有根对象撤销，图内每个对象都必须被擦除且仅擦除一次。
	for i := 0; i < rootCount; i++ {
		_ = g.Revoke(ObjectID(fmt.Sprintf("root-%d", i)))
	}

	blobsMu.Lock()
	total := len(blobs)
	for id, data := range blobs {
		if !allZero(data) {
			t.Errorf("leak: object %s still contains plaintext", id)
		}
	}
	blobsMu.Unlock()

	if len(g.Wiped()) != total {
		t.Fatalf("wiped count = %d, registered count = %d (漏清)", len(g.Wiped()), total)
	}
	wipeMu.Lock()
	for id, n := range wipeSet {
		if n != 1 {
			t.Errorf("object %s wiped %d times, want exactly 1", id, n)
		}
	}
	if len(wipeSet) != total {
		t.Fatalf("wipe events = %d, objects = %d", len(wipeSet), total)
	}
	wipeMu.Unlock()
}

func TestConcurrentRevokeOverlappingClosures(t *testing.T) {
	g := NewGraph()
	must(t, g.RegisterSource("a", 1, secret(4, 0x5)))
	must(t, g.RegisterSource("b", 1, secret(4, 0x5)))
	shared := secret(4, 0x5)
	must(t, g.RegisterDerived("shared", 1, shared, []ObjectID{"a", "b"}))

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				_ = g.Revoke("a")
			} else {
				_ = g.Revoke("b")
			}
		}(i)
	}
	wg.Wait()

	if !g.IsWiped("a") || !g.IsWiped("b") || !g.IsWiped("shared") {
		t.Fatal("overlapping revocations missed an object")
	}
	if !allZero(shared) {
		t.Fatal("shared plaintext survived")
	}
}
