package ptrauth

import (
	"errors"
	"testing"
)

func TestFreeRetiresGenerationAndReuseAdvancesIt(t *testing.T) {
	reg := newTestRegistry(t)
	backing := make([]byte, 64)
	obj, err := reg.RegisterData(backing)
	if err != nil {
		t.Fatalf("RegisterData: %v", err)
	}
	ptr, err := reg.Tag(obj, KindReadWrite, ctxA)
	if err != nil {
		t.Fatalf("Tag: %v", err)
	}
	child, err := reg.Subslice(ptr, ctxA, 8, 16)
	if err != nil {
		t.Fatalf("Subslice: %v", err)
	}

	if err := reg.Free(obj); err != nil {
		t.Fatalf("Free: %v", err)
	}
	if obj.Live() {
		t.Fatal("object must report retired after Free")
	}

	// Both the root and every derived generation-1 pointer are dead at the
	// dereference gate.
	if err := reg.Check(ptr, ctxA); !errors.Is(err, ErrGenerationRetired) {
		t.Fatalf("retired root: expected ErrGenerationRetired, got %v", err)
	}
	if err := reg.Check(child, ctxA); !errors.Is(err, ErrGenerationRetired) {
		t.Fatalf("retired child: expected ErrGenerationRetired, got %v", err)
	}
	if _, err := reg.Read(ptr, ctxA, 0, 1); !errors.Is(err, ErrGenerationRetired) {
		t.Fatalf("read retired: expected ErrGenerationRetired, got %v", err)
	}

	// Double free is reported, not silently accepted.
	if err := reg.Free(obj); !errors.Is(err, ErrGenerationRetired) {
		t.Fatalf("double free: expected ErrGenerationRetired, got %v", err)
	}

	// Address reuse: the same backing slice is registered again. The old
	// tag now names a stale generation even though the address matches.
	obj2, err := reg.RegisterData(backing)
	if err != nil {
		t.Fatalf("re-register: %v", err)
	}
	if obj2.Generation() != 2 {
		t.Fatalf("expected generation 2 after reuse, got %d", obj2.Generation())
	}
	if err := reg.Check(ptr, ctxA); !errors.Is(err, ErrGenerationStale) {
		t.Fatalf("stale generation after reuse: expected ErrGenerationStale, got %v", err)
	}

	// A freshly minted generation-2 tag works.
	fresh, err := reg.Tag(obj2, KindReadWrite, ctxA)
	if err != nil {
		t.Fatalf("Tag gen2: %v", err)
	}
	if fresh.Generation() != 2 {
		t.Fatalf("fresh pointer generation = %d, want 2", fresh.Generation())
	}
	if err := reg.Write(fresh, ctxA, 0, []byte("gen2")); err != nil {
		t.Fatalf("Write gen2: %v", err)
	}

	// Freeing generation 2 retires the address again; the cycle is repeatable.
	if err := reg.Free(obj2); err != nil {
		t.Fatalf("Free gen2: %v", err)
	}
	if err := reg.Check(fresh, ctxA); !errors.Is(err, ErrGenerationRetired) {
		t.Fatalf("gen2 retirement: expected ErrGenerationRetired, got %v", err)
	}
}

func TestRetiredGenerationBlocksCrossContextBorrow(t *testing.T) {
	reg := newTestRegistry(t)
	obj, err := reg.RegisterData(make([]byte, 32))
	if err != nil {
		t.Fatalf("RegisterData: %v", err)
	}
	ptr, err := reg.Tag(obj, KindReadWrite, ctxA)
	if err != nil {
		t.Fatalf("Tag: %v", err)
	}
	if err := reg.Free(obj); err != nil {
		t.Fatalf("Free: %v", err)
	}

	// No sanctioned path can resurrect a retired pointer into another
	// context: recontextualization consults the same generation gate.
	if _, err := reg.Recontext(ptr, ctxA, ctxB, KindReadOnly); !errors.Is(err, ErrGenerationRetired) {
		t.Fatalf("recontext of retired pointer: expected ErrGenerationRetired, got %v", err)
	}

	// Reusing the exact address does not validate the old tag in any
	// context, including its original one.
	reused := make([]byte, 32)
	obj2, err := reg.RegisterData(reused)
	if err != nil {
		t.Fatalf("RegisterData: %v", err)
	}
	ptr2, err := reg.Tag(obj2, KindReadWrite, ctxA)
	if err != nil {
		t.Fatalf("Tag: %v", err)
	}
	if err := reg.Free(obj2); err != nil {
		t.Fatalf("Free: %v", err)
	}
	if _, err := reg.RegisterData(reused); err != nil {
		t.Fatalf("second registration: %v", err)
	}
	if _, err := reg.Recontext(ptr2, ctxA, ctxB, KindReadOnly); !errors.Is(err, ErrGenerationStale) {
		t.Fatalf("recontext of stale pointer: expected ErrGenerationStale, got %v", err)
	}
}

func TestRegisterInvariants(t *testing.T) {
	reg := newTestRegistry(t)

	if _, err := reg.RegisterData(nil); !errors.Is(err, ErrEmptyObject) {
		t.Fatalf("empty data register: expected ErrEmptyObject, got %v", err)
	}
	if _, err := reg.RegisterCode(0, 8, func(uint64) uint64 { return 0 }); !errors.Is(err, ErrUnknownAddress) {
		t.Fatalf("zero code address: expected ErrUnknownAddress, got %v", err)
	}
	if _, err := reg.RegisterCode(0x2000, 0, func(uint64) uint64 { return 0 }); !errors.Is(err, ErrEmptyObject) {
		t.Fatalf("zero code size: expected ErrEmptyObject, got %v", err)
	}
	if _, err := reg.RegisterCode(0x2000, 8, nil); !errors.Is(err, ErrNotExecutable) {
		t.Fatalf("nil code entry: expected ErrNotExecutable, got %v", err)
	}

	backing := make([]byte, 16)
	obj, err := reg.RegisterData(backing)
	if err != nil {
		t.Fatalf("RegisterData: %v", err)
	}
	if _, err := reg.RegisterData(backing); !errors.Is(err, ErrAddressLive) {
		t.Fatalf("double live registration: expected ErrAddressLive, got %v", err)
	}

	// A sub-slice backed by the same array overlaps the live object; since
	// its base address differs it is an independent registration.
	sub := backing[4:]
	subObj, err := reg.RegisterData(sub)
	if err != nil {
		t.Fatalf("overlapping sub-slice registration: %v", err)
	}
	if subObj.Address() == obj.Address() {
		t.Fatal("sub-slice must register at its own base address")
	}
}
