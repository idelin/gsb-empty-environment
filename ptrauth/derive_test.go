package ptrauth

import (
	"errors"
	"sync/atomic"
	"testing"
)

func TestDeriveReauthenticatesAndNarrows(t *testing.T) {
	reg := newTestRegistry(t)
	backing := make([]byte, 128)
	obj, err := reg.RegisterData(backing)
	if err != nil {
		t.Fatalf("RegisterData: %v", err)
	}
	root, err := reg.Tag(obj, KindReadWrite, ctxA)
	if err != nil {
		t.Fatalf("Tag: %v", err)
	}

	// A sub-slice gets a fresh tag over the new address and span.
	child, err := reg.Subslice(root, ctxA, 16, 32)
	if err != nil {
		t.Fatalf("Subslice: %v", err)
	}
	if child.Address() != root.Address()+16 || child.Size() != 32 {
		t.Fatalf("child geometry mismatch: addr delta=%d size=%d", child.Address()-root.Address(), child.Size())
	}
	if child.Context() != ctxA || child.Kind() != KindReadWrite {
		t.Fatalf("child must preserve context and kind: %+v", child)
	}
	if err := reg.Write(child, ctxA, 0, []byte("child-write")); err != nil {
		t.Fatalf("Write through child: %v", err)
	}

	// Narrowing to read-only is allowed and enforced.
	ro, err := reg.Derive(root, ctxA, 0, 8, KindReadOnly)
	if err != nil {
		t.Fatalf("Derive read-only: %v", err)
	}
	if err := reg.Write(ro, ctxA, 0, []byte{1}); !errors.Is(err, ErrKindNotPermitted) {
		t.Fatalf("write to narrowed pointer: expected ErrKindNotPermitted, got %v", err)
	}

	// Widening a read-only pointer back to writable is rejected.
	if _, err := reg.Derive(ro, ctxA, 0, 8, KindReadWrite); !errors.Is(err, ErrKindNotPermitted) {
		t.Fatalf("kind widening: expected ErrKindNotPermitted, got %v", err)
	}

	// Data pointers cannot be promoted to executable via derivation.
	if _, err := reg.Derive(root, ctxA, 0, 8, KindCode); !errors.Is(err, ErrKindNotPermitted) {
		t.Fatalf("data-to-code promotion: expected ErrKindNotPermitted, got %v", err)
	}

	// Spans cannot escape the authenticated region.
	if _, err := reg.Subslice(root, ctxA, 100, 40); !errors.Is(err, ErrOutOfBounds) {
		t.Fatalf("out-of-bounds subslice: expected ErrOutOfBounds, got %v", err)
	}
	if _, err := reg.Derive(root, ctxA, 0, 129, KindReadOnly); !errors.Is(err, ErrOutOfBounds) {
		t.Fatalf("oversized derive: expected ErrOutOfBounds, got %v", err)
	}

	// Forging the child's span without a fresh tag fails the HMAC.
	extended := child
	extended.size = 64
	if err := reg.Check(extended, ctxA); !errors.Is(err, ErrTagForged) {
		t.Fatalf("span extension: expected ErrTagForged, got %v", err)
	}

	// Derivation in the wrong context is rejected.
	if _, err := reg.Subslice(root, ctxB, 0, 8); !errors.Is(err, ErrContextMismatch) {
		t.Fatalf("derive in foreign context: expected ErrContextMismatch, got %v", err)
	}
}

func TestWXInvariant(t *testing.T) {
	reg := newTestRegistry(t)
	obj, err := reg.RegisterData(make([]byte, 16))
	if err != nil {
		t.Fatalf("RegisterData: %v", err)
	}
	if _, err := reg.Tag(obj, KindRead|KindWrite|KindExecute, ctxA); !errors.Is(err, ErrWXConflict) {
		t.Fatalf("W^X: expected ErrWXConflict, got %v", err)
	}
	if _, err := reg.Tag(obj, 0, ctxA); !errors.Is(err, ErrZeroKind) {
		t.Fatalf("zero kind: expected ErrZeroKind, got %v", err)
	}
}

func TestExecutableObjectAndCodeGate(t *testing.T) {
	reg := newTestRegistry(t)
	const codeAddr uint64 = 0x1000
	var called atomic.Bool
	obj, err := reg.RegisterCode(codeAddr, 16, func(arg uint64) uint64 {
		called.Store(true)
		return arg + 1
	})
	if err != nil {
		t.Fatalf("RegisterCode: %v", err)
	}
	code, err := reg.Tag(obj, KindCode, ctxA)
	if err != nil {
		t.Fatalf("Tag code: %v", err)
	}
	out, err := reg.Call(code, ctxA, 41)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if out != 42 || !called.Load() {
		t.Fatalf("unexpected call result=%d called=%v", out, called.Load())
	}

	// Executable pointers cannot be used as data.
	if _, err := reg.Read(code, ctxA, 0, 1); !errors.Is(err, ErrNotData) {
		t.Fatalf("read code as data: expected ErrNotData, got %v", err)
	}

	// Data pointers cannot be called.
	dataObj, err := reg.RegisterData(make([]byte, 16))
	if err != nil {
		t.Fatalf("RegisterData: %v", err)
	}
	dataPtr, err := reg.Tag(dataObj, KindReadWrite, ctxA)
	if err != nil {
		t.Fatalf("Tag data: %v", err)
	}
	if _, err := reg.Call(dataPtr, ctxA, 1); !errors.Is(err, ErrKindNotPermitted) {
		t.Fatalf("call data pointer: expected ErrKindNotPermitted, got %v", err)
	}

	// A code pointer presented in another context is blocked.
	if _, err := reg.Call(code, ctxB, 1); !errors.Is(err, ErrContextMismatch) {
		t.Fatalf("cross-context call: expected ErrContextMismatch, got %v", err)
	}
}
