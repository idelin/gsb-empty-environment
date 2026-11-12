package netsec

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
)

var (
	ctxA = Context{Domain: "runtime.net", CallSite: 101}
	ctxB = Context{Domain: "runtime.net", CallSite: 202}
	ctxC = Context{Domain: "driver.io", CallSite: 101}
)

const addr1 uintptr = 0x1000
const addr2 uintptr = 0x2000

func newTestRegistry(t *testing.T) *Registry {
	t.Helper()
	tagger, err := NewHMACTagger(sha256.New, []byte("unit-test-pointer-auth-key-0123456789"))
	if err != nil {
		t.Fatalf("NewHMACTagger: %v", err)
	}
	registry, err := NewRegistry(tagger)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	return registry
}

// withTag 返回把认证标签替换成 other 标签的指针副本，用于模拟标签/指针交换攻击。
func withTag(p AuthenticatedPointer, tag []byte) AuthenticatedPointer {
	p.tag = append([]byte(nil), tag...)
	return p
}

// TestHMACRFC4231Vector 用 RFC 4231 用例 1 确认底层 MAC 是真实 HMAC-SHA256。
func TestHMACRFC4231Vector(t *testing.T) {
	key := make([]byte, 20)
	for i := range key {
		key[i] = 0x0b
	}
	tagger, err := NewHMACTagger(sha256.New, key)
	if err != nil {
		t.Fatalf("NewHMACTagger: %v", err)
	}

	got := hex.EncodeToString(tagger.Tag([]byte("Hi There")))
	const want = "b0344c61d8db38535ca8afceaf0bf12b881dc200c9833da726e9376c2e32cff7"
	if got != want {
		t.Fatalf("HMAC-SHA256 mismatch:\n got %s\nwant %s", got, want)
	}
	if len(tagger.Tag([]byte("x"))) != sha256.Size {
		t.Fatal("tag length must equal hash output size")
	}
	if !tagger.Equal(tagger.Tag([]byte("x")), tagger.Tag([]byte("x"))) {
		t.Fatal("equal tags must compare equal")
	}
	if tagger.Equal(tagger.Tag([]byte("x")), tagger.Tag([]byte("y"))) {
		t.Fatal("different tags must compare unequal")
	}
}

func TestNewRegistryRejectsNilMAC(t *testing.T) {
	if _, err := NewRegistry(nil); !errors.Is(err, ErrNilMAC) {
		t.Fatalf("want ErrNilMAC, got %v", err)
	}
	if _, err := NewHMACTagger(sha256.New, nil); err == nil {
		t.Fatal("empty HMAC key must be rejected")
	}
}

func TestMintAndUseGates(t *testing.T) {
	registry := newTestRegistry(t)

	fn, err := registry.Mint(addr1, KindFunction, ctxA)
	if err != nil {
		t.Fatalf("Mint function: %v", err)
	}
	data, err := registry.Mint(addr2, KindData, ctxA)
	if err != nil {
		t.Fatalf("Mint data: %v", err)
	}
	if fn.Generation() == data.Generation() {
		t.Fatal("each Mint must allocate a distinct generation")
	}

	if out, err := registry.UseForCall(fn, ctxA); err != nil || out != addr1 {
		t.Fatalf("function pointer should pass call gate, got %#x, %v", out, err)
	}
	if out, err := registry.UseForDeref(data, ctxA); err != nil || out != addr2 {
		t.Fatalf("data pointer should pass deref gate, got %#x, %v", out, err)
	}

	if _, err := registry.UseForDeref(fn, ctxA); !errors.Is(err, ErrInvalidKind) {
		t.Fatalf("function pointer must fail deref gate, got %v", err)
	}
	if _, err := registry.UseForCall(data, ctxA); !errors.Is(err, ErrInvalidKind) {
		t.Fatalf("data pointer must fail call gate, got %v", err)
	}
}

// TestContextReuseDataAsFunction 复现题述威胁：认证上下文被复用时，
// 同上下文的数据指针不能通过函数指针的调用门禁。
func TestContextReuseDataAsFunction(t *testing.T) {
	registry := newTestRegistry(t)

	fn, err := registry.Mint(addr1, KindFunction, ctxA)
	if err != nil {
		t.Fatal(err)
	}
	data, err := registry.Mint(addr2, KindData, ctxA)
	if err != nil {
		t.Fatal(err)
	}

	if err := registry.Verify(data, ctxA); err != nil {
		t.Fatalf("data pointer should verify in its own context: %v", err)
	}
	if _, err := registry.UseForCall(data, ctxA); !errors.Is(err, ErrInvalidKind) {
		t.Fatalf("data pointer must never be callable, got %v", err)
	}

	forged := withTag(data, fn.Tag())
	if err := registry.Verify(forged, ctxA); !errors.Is(err, ErrTagMismatch) {
		t.Fatalf("swapped function tag on data pointer must mismatch, got %v", err)
	}
}

func TestTamperedFieldsFailTag(t *testing.T) {
	registry := newTestRegistry(t)
	p, err := registry.Mint(addr1, KindData, ctxA)
	if err != nil {
		t.Fatal(err)
	}

	cases := map[string]AuthenticatedPointer{
		"address":    {address: addr2, generation: p.generation, kind: p.kind, context: p.context, tag: p.tag},
		"generation": {address: p.address, generation: p.generation + 1, kind: p.kind, context: p.context, tag: p.tag},
		"kind":       {address: p.address, generation: p.generation, kind: KindFunction, context: p.context, tag: p.tag},
		"context":    {address: p.address, generation: p.generation, kind: p.kind, context: ctxB, tag: p.tag},
		"tag":        withTag(p, []byte("not-a-valid-authentication-tag!!")),
	}
	for name, forged := range cases {
		if err := registry.Verify(forged, ctxA); !errors.Is(err, ErrTagMismatch) {
			t.Fatalf("%s tamper must yield ErrTagMismatch, got %v", name, err)
		}
	}
}

func TestCrossObjectTagSwap(t *testing.T) {
	registry := newTestRegistry(t)

	objA, err := registry.Mint(addr1, KindData, ctxA)
	if err != nil {
		t.Fatal(err)
	}
	objB, err := registry.Mint(addr2, KindData, ctxA)
	if err != nil {
		t.Fatal(err)
	}

	// 同种类、同上下文，仅对象不同：互换标签后两边都必须认证失败。
	swappedA := withTag(objA, objB.Tag())
	swappedB := withTag(objB, objA.Tag())
	if err := registry.Verify(swappedA, ctxA); !errors.Is(err, ErrTagMismatch) {
		t.Fatalf("cross-object swap A<-B must mismatch, got %v", err)
	}
	if err := registry.Verify(swappedB, ctxA); !errors.Is(err, ErrTagMismatch) {
		t.Fatalf("cross-object swap B<-A must mismatch, got %v", err)
	}
	if err := registry.Verify(objA, ctxA); err != nil {
		t.Fatalf("original pointer must remain valid: %v", err)
	}
}

func TestCrossContextExchange(t *testing.T) {
	registry := newTestRegistry(t)

	p, err := registry.Mint(addr1, KindFunction, ctxA)
	if err != nil {
		t.Fatal(err)
	}

	// 在其它调用点/安全域使用：标签仍证明字段完整，但上下文门禁拒绝。
	for _, foreign := range []Context{ctxB, ctxC} {
		if err := registry.Verify(p, foreign); !errors.Is(err, ErrContextMismatch) {
			t.Fatalf("pointer bound to %v must fail in %v, got %v", p.UseContext(), foreign, err)
		}
		if _, err := registry.UseForCall(p, foreign); !errors.Is(err, ErrContextMismatch) {
			t.Fatalf("call gate in foreign context must fail, got %v", err)
		}
	}

	// 把上下文字段改成 ctxB 却保留旧标签，属于篡改，标签先失配。
	forged := p
	forged.context = ctxB
	if err := registry.Verify(forged, ctxB); !errors.Is(err, ErrTagMismatch) {
		t.Fatalf("rebinding context without re-auth must mismatch, got %v", err)
	}
}

func TestDeriveReauthenticationAndNarrowing(t *testing.T) {
	registry := newTestRegistry(t)

	parent, err := registry.Mint(addr1, KindFunction, ctxA)
	if err != nil {
		t.Fatal(err)
	}

	// 函数指针收窄为数据指针（内部指针），必须重新签发标签。
	interior, err := registry.Derive(parent, addr1+0x40, KindData, ctxA)
	if err != nil {
		t.Fatalf("narrowing derive: %v", err)
	}
	if hmac.Equal(interior.tag, parent.tag) {
		t.Fatal("derived pointer must carry a freshly computed tag, not the parent tag")
	}
	if interior.Generation() != parent.Generation() {
		t.Fatal("derived pointer shares the parent object generation")
	}
	if out, err := registry.UseForDeref(interior, ctxA); err != nil || out != addr1+0x40 {
		t.Fatalf("interior data pointer should pass deref gate, got %#x, %v", out, err)
	}
	if _, err := registry.UseForCall(interior, ctxA); !errors.Is(err, ErrInvalidKind) {
		t.Fatalf("narrowed pointer must not be callable, got %v", err)
	}

	// 同种类保留是允许的。
	sameKind, err := registry.Derive(parent, addr1+0x80, KindFunction, ctxA)
	if err != nil {
		t.Fatalf("same-kind derive: %v", err)
	}
	if _, err := registry.UseForCall(sameKind, ctxA); err != nil {
		t.Fatalf("same-kind derived function pointer should be callable: %v", err)
	}

	// 数据指针提权为函数指针必须拒绝。
	dataParent, err := registry.Mint(addr2, KindData, ctxA)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Derive(dataParent, addr2+8, KindFunction, ctxA); !errors.Is(err, ErrWidenedKind) {
		t.Fatalf("kind widening must be rejected, got %v", err)
	}

	// 在错误上下文里派生：父指针先过不了上下文门禁。
	if _, err := registry.Derive(parent, addr1+0xC0, KindData, ctxB); !errors.Is(err, ErrContextMismatch) {
		t.Fatalf("derive in foreign context must fail parent gate, got %v", err)
	}

	// 复用父标签冒充派生指针：地址不同，标签失配。
	forged := withTag(interior, parent.Tag())
	if err := registry.Verify(forged, ctxA); !errors.Is(err, ErrTagMismatch) {
		t.Fatalf("reusing parent tag on derived pointer must mismatch, got %v", err)
	}
}

func TestAddressReuseInvalidatesOldGeneration(t *testing.T) {
	registry := newTestRegistry(t)

	old, err := registry.Mint(addr1, KindData, ctxA)
	if err != nil {
		t.Fatal(err)
	}
	oldGen := old.Generation()

	// 同一地址分配新对象：旧代映射被覆盖。
	current, err := registry.Mint(addr1, KindFunction, ctxA)
	if err != nil {
		t.Fatal(err)
	}
	if current.Generation() <= oldGen {
		t.Fatal("reused address must receive a fresh, larger generation")
	}

	if err := registry.Verify(old, ctxA); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("old-generation pointer must be stale, got %v", err)
	}
	if _, err := registry.UseForDeref(old, ctxA); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("stale pointer must fail deref gate, got %v", err)
	}
	if _, err := registry.UseForCall(current, ctxA); err != nil {
		t.Fatalf("current-generation pointer must pass call gate: %v", err)
	}

	forged := withTag(old, current.Tag())
	if err := registry.Verify(forged, ctxA); !errors.Is(err, ErrTagMismatch) {
		t.Fatalf("swapped tag across generations must mismatch, got %v", err)
	}
}

func TestRetireEnforcesUseGateAfterVerification(t *testing.T) {
	registry := newTestRegistry(t)

	root, err := registry.Mint(addr1, KindFunction, ctxA)
	if err != nil {
		t.Fatal(err)
	}
	interior, err := registry.Derive(root, addr1+0x10, KindData, ctxA)
	if err != nil {
		t.Fatal(err)
	}

	// 退役前两者都能通过各自门禁。
	if err := registry.Verify(root, ctxA); err != nil {
		t.Fatalf("root pre-retire: %v", err)
	}
	if err := registry.Verify(interior, ctxA); err != nil {
		t.Fatalf("interior pre-retire: %v", err)
	}

	if err := registry.Retire(addr1); err != nil {
		t.Fatalf("Retire: %v", err)
	}

	// 退役后同代次的全部指针（含派生指针）都被门禁拒绝。
	if err := registry.Verify(root, ctxA); !errors.Is(err, ErrRetired) {
		t.Fatalf("retired root pointer must fail verify, got %v", err)
	}
	if _, err := registry.UseForCall(root, ctxA); !errors.Is(err, ErrRetired) {
		t.Fatalf("retired root pointer must fail call gate, got %v", err)
	}
	if err := registry.Verify(interior, ctxA); !errors.Is(err, ErrRetired) {
		t.Fatalf("retired derived pointer must fail verify, got %v", err)
	}
	if _, err := registry.UseForDeref(interior, ctxA); !errors.Is(err, ErrRetired) {
		t.Fatalf("retired derived pointer must fail deref gate, got %v", err)
	}

	// 退役未知地址应报错。
	if err := registry.Retire(addr2); !errors.Is(err, ErrUnknownObject) {
		t.Fatalf("retire unknown address must fail, got %v", err)
	}
}

func TestSeparateMACKeysIsolateRegistries(t *testing.T) {
	taggerA, _ := NewHMACTagger(sha256.New, []byte("key-A"))
	taggerB, _ := NewHMACTagger(sha256.New, []byte("key-B"))
	registryA, _ := NewRegistry(taggerA)
	registryB, _ := NewRegistry(taggerB)

	p, err := registryA.Mint(addr1, KindData, ctxA)
	if err != nil {
		t.Fatal(err)
	}
	// 在 registryB 中登记同地址同字段，但密钥不同，A 的标签不可被 B 接受。
	if _, err := registryB.Mint(addr1, KindData, ctxA); err != nil {
		t.Fatal(err)
	}
	if err := registryB.Verify(p, ctxA); !errors.Is(err, ErrTagMismatch) {
		t.Fatalf("pointer tagged under another MAC key must mismatch, got %v", err)
	}
}
