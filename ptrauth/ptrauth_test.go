package ptrauth

import (
	"bytes"
	"errors"
	"testing"
)

const (
	ctxA ContextID = "context-A"
	ctxB ContextID = "context-B"
	ctxC ContextID = "context-C"
)

func newTestRegistry(t *testing.T) *Registry {
	t.Helper()
	// Deterministic 32-byte HMAC key: real HMAC-SHA256, fixed only so test
	// failures are reproducible.
	key := bytes.Repeat([]byte{0x42}, 32)
	auth, err := NewHMACAuthenticator(key)
	if err != nil {
		t.Fatalf("NewHMACAuthenticator: %v", err)
	}
	return NewRegistry(auth)
}

func newSecondRegistry(t *testing.T) *Registry {
	t.Helper()
	auth, err := NewHMACAuthenticator(bytes.Repeat([]byte{0x77}, 32))
	if err != nil {
		t.Fatalf("NewHMACAuthenticator: %v", err)
	}
	return NewRegistry(auth)
}

func TestAuthenticatorRejectsWeakKey(t *testing.T) {
	if _, err := NewHMACAuthenticator([]byte("short")); !errors.Is(err, ErrWeakKey) {
		t.Fatalf("expected ErrWeakKey, got %v", err)
	}
	key, err := GenerateHMACKey()
	if err != nil {
		t.Fatalf("GenerateHMACKey: %v", err)
	}
	auth, err := NewHMACAuthenticator(key)
	if err != nil {
		t.Fatalf("NewHMACAuthenticator with generated key: %v", err)
	}
	tag := auth.Tag(1, 1, KindReadOnly, ctxA, 8)
	if !auth.Verify(1, 1, KindReadOnly, ctxA, 8, tag) {
		t.Fatal("tag generated from a random key failed verification")
	}
}

func TestTagReadWriteRoundTrip(t *testing.T) {
	reg := newTestRegistry(t)
	backing := make([]byte, 64)
	obj, err := reg.RegisterData(backing)
	if err != nil {
		t.Fatalf("RegisterData: %v", err)
	}
	if obj.Generation() != 1 || !obj.Live() {
		t.Fatalf("unexpected initial object state: gen=%d live=%v", obj.Generation(), obj.Live())
	}

	ptr, err := reg.Tag(obj, KindReadWrite, ctxA)
	if err != nil {
		t.Fatalf("Tag: %v", err)
	}
	if ptr.Kind() != KindReadWrite || ptr.Size() != 64 || ptr.Context() != ctxA {
		t.Fatalf("tag metadata mismatch: %+v", ptr)
	}

	payload := []byte("hello-runtime")
	if err := reg.Write(ptr, ctxA, 4, payload); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := reg.Read(ptr, ctxA, 4, uint64(len(payload)))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("data mismatch: got %q want %q", got, payload)
	}
	if !bytes.Equal(backing[4:4+len(payload)], payload) {
		t.Fatal("write did not reach the backing allocation")
	}
}

func TestReadOnlyPointerDeniesWrite(t *testing.T) {
	reg := newTestRegistry(t)
	obj, err := reg.RegisterData(make([]byte, 32))
	if err != nil {
		t.Fatalf("RegisterData: %v", err)
	}
	ptr, err := reg.Tag(obj, KindReadOnly, ctxA)
	if err != nil {
		t.Fatalf("Tag: %v", err)
	}
	if _, err := reg.Read(ptr, ctxA, 0, 4); err != nil {
		t.Fatalf("Read through read-only pointer: %v", err)
	}
	if err := reg.Write(ptr, ctxA, 0, []byte{1, 2}); !errors.Is(err, ErrKindNotPermitted) {
		t.Fatalf("expected ErrKindNotPermitted, got %v", err)
	}
}

func TestCrossObjectForgery(t *testing.T) {
	reg := newTestRegistry(t)
	backingA := make([]byte, 64)
	objA, err := reg.RegisterData(backingA)
	if err != nil {
		t.Fatalf("RegisterData A: %v", err)
	}
	objB, err := reg.RegisterData(make([]byte, 64))
	if err != nil {
		t.Fatalf("RegisterData B: %v", err)
	}

	ptrA, err := reg.Tag(objA, KindReadWrite, ctxA)
	if err != nil {
		t.Fatalf("Tag A: %v", err)
	}
	ptrB, err := reg.Tag(objB, KindReadWrite, ctxA)
	if err != nil {
		t.Fatalf("Tag B: %v", err)
	}

	// Attack 1: splice B's MAC onto A's attributes. HMAC binds the address,
	// so the grafted tag must fail.
	forged := ptrA
	forged.tag = cloneTag(ptrB.tag)
	if err := reg.Check(forged, ctxA); !errors.Is(err, ErrTagForged) {
		t.Fatalf("cross-object tag graft: expected ErrTagForged, got %v", err)
	}

	// Attack 2: keep A's valid MAC but relabel the address to B.
	relabeled := ptrA
	relabeled.address = ptrB.address
	if err := reg.Check(relabeled, ctxA); !errors.Is(err, ErrTagForged) {
		t.Fatalf("address relabel: expected ErrTagForged, got %v", err)
	}

	// Attack 3: mutate the kind bits without recomputing the tag.
	promoted := ptrA
	promoted.kind = KindCode
	if err := reg.Check(promoted, ctxA); !errors.Is(err, ErrTagForged) {
		t.Fatalf("kind promotion: expected ErrTagForged, got %v", err)
	}

	// Attack 4: forge with a different runtime key.
	other := newSecondRegistry(t)
	otherObj, err := other.RegisterData(backingA)
	if err != nil {
		t.Fatalf("foreign RegisterData: %v", err)
	}
	foreign, err := other.Tag(otherObj, KindReadWrite, ctxA)
	if err != nil {
		t.Fatalf("foreign Tag: %v", err)
	}
	if err := reg.Check(foreign, ctxA); !errors.Is(err, ErrTagForged) {
		t.Fatalf("foreign key tag: expected ErrTagForged, got %v", err)
	}

	// Attack 5: tamper with a single tag byte.
	tampered := ptrA
	tampered.tag = cloneTag(ptrA.tag)
	tampered.tag[0] ^= 0x01
	if err := reg.Check(tampered, ctxA); !errors.Is(err, ErrTagForged) {
		t.Fatalf("tag tamper: expected ErrTagForged, got %v", err)
	}

	// Attack 6: zero-valued pointer has no tag at all.
	if err := reg.Check(TaggedPtr{}, ctxA); !errors.Is(err, ErrTagForged) {
		t.Fatalf("zero pointer: expected ErrTagForged, got %v", err)
	}
}

func TestCrossContextSwap(t *testing.T) {
	reg := newTestRegistry(t)
	obj, err := reg.RegisterData(make([]byte, 32))
	if err != nil {
		t.Fatalf("RegisterData: %v", err)
	}
	ptr, err := reg.Tag(obj, KindReadWrite, ctxA)
	if err != nil {
		t.Fatalf("Tag: %v", err)
	}

	// Presenting an A-context pointer in context B is denied outright.
	if _, err := reg.Read(ptr, ctxB, 0, 1); !errors.Is(err, ErrContextMismatch) {
		t.Fatalf("read in foreign context: expected ErrContextMismatch, got %v", err)
	}

	// Hand-editing the context field invalidates the HMAC rather than
	// downgrading into a successful access.
	swapped := ptr
	swapped.context = ctxB
	if err := reg.Check(swapped, ctxB); !errors.Is(err, ErrTagForged) {
		t.Fatalf("raw context swap: expected ErrTagForged, got %v", err)
	}

	// Recontext must verify the source context before re-minting.
	if _, err := reg.Recontext(ptr, ctxB, ctxC, KindReadOnly); !errors.Is(err, ErrContextMismatch) {
		t.Fatalf("Recontext from wrong source: expected ErrContextMismatch, got %v", err)
	}

	// The sanctioned transfer re-authenticates and may narrow the kind.
	borrowed, err := reg.Recontext(ptr, ctxA, ctxB, KindReadOnly)
	if err != nil {
		t.Fatalf("Recontext: %v", err)
	}
	if borrowed.Context() != ctxB || borrowed.Kind() != KindReadOnly {
		t.Fatalf("borrowed pointer metadata mismatch: %+v", borrowed)
	}
	if err := reg.Check(borrowed, ctxB); err != nil {
		t.Fatalf("borrowed pointer check in target context: %v", err)
	}
	if _, err := reg.Read(borrowed, ctxA, 0, 1); !errors.Is(err, ErrContextMismatch) {
		t.Fatalf("borrowed pointer used in source context: expected ErrContextMismatch, got %v", err)
	}
	if err := reg.Write(borrowed, ctxB, 0, []byte{1}); !errors.Is(err, ErrKindNotPermitted) {
		t.Fatalf("narrowed borrowed pointer write: expected ErrKindNotPermitted, got %v", err)
	}

	// The original pointer remains bound to its own context.
	if err := reg.Check(ptr, ctxA); err != nil {
		t.Fatalf("original pointer should remain valid in ctxA: %v", err)
	}
}
