package netsec_test

import (
	"errors"
	"math"
	"testing"
	"unsafe"

	netsec "example.org/netsec/q0196"
)

func uint32Ptr(t *testing.T, p unsafe.Pointer) *uint32 {
	t.Helper()
	return (*uint32)(p)
}

func addressToPointer(u uintptr) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&u))
}

func mustViolation(t *testing.T, err error, want netsec.ViolationKind) *netsec.Violation {
	t.Helper()
	if err == nil {
		t.Fatalf("expected %s violation, got nil", want)
	}
	var v *netsec.Violation
	if !errors.As(err, &v) {
		t.Fatalf("expected *netsec.Violation, got %T: %v", err, err)
	}
	if v.Kind != want {
		t.Fatalf("got violation kind %q, want %q: %v", v.Kind, want, err)
	}
	return v
}

func TestLegalAddAndSlice(t *testing.T) {
	var backing [64]byte
	c := netsec.NewChecker()
	if _, err := c.Register(unsafe.Pointer(&backing[0]), uintptr(len(backing)), netsec.Layout{Size: 1, Align: 1}); err != nil {
		t.Fatalf("register: %v", err)
	}

	p, err := c.Add(unsafe.Pointer(&backing[0]), 8, netsec.LayoutOf[uint32]())
	if err != nil {
		t.Fatalf("legal Add rejected: %v", err)
	}
	*uint32Ptr(t, p) = 0x01020304
	if backing[8] != 0x04 || backing[11] != 0x01 {
		t.Fatalf("checked pointer did not address backing[8]: %v", backing[8:12])
	}

	base, err := c.Slice(unsafe.Pointer(&backing[0]), 16, netsec.LayoutOf[uint32]())
	if err != nil {
		t.Fatalf("legal Slice rejected: %v", err)
	}
	if base != unsafe.Pointer(&backing[0]) {
		t.Fatalf("Slice returned %p, want %p", base, &backing[0])
	}

	at, err := c.SliceAt(unsafe.Pointer(&backing[0]), 8, 8, netsec.LayoutOf[uint16]())
	if err != nil {
		t.Fatalf("legal SliceAt rejected: %v", err)
	}
	*(*uint16)(at) = 0xabcd
	if backing[8] != 0xcd || backing[9] != 0xab {
		t.Fatalf("SliceAt did not address backing[8]: %v", backing[8:10])
	}
}

func TestLegalEndOffsetAndEmptySlice(t *testing.T) {
	var backing [64]byte
	c := netsec.NewChecker()
	alloc, err := c.Register(unsafe.Pointer(&backing[0]), uintptr(len(backing)), netsec.LayoutOf[byte]())
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	end := addressToPointer(uintptr(unsafe.Pointer(&backing[0])) + alloc.Size)

	if _, err := c.Slice(end, 0, netsec.LayoutOf[uint64]()); err != nil {
		t.Fatalf("empty slice at aligned one-past-end rejected: %v", err)
	}
	if _, err := c.Add(unsafe.Pointer(&backing[0]), alloc.Size, netsec.Layout{Align: 4}); err != nil {
		t.Fatalf("empty Add at one-past-end rejected: %v", err)
	}
	if _, err := c.Slice(addressToPointer(uintptr(end)+1), 0, netsec.LayoutOf[byte]()); err == nil {
		t.Fatal("empty slice past the allocation was accepted")
	}
}

func TestLengthFieldOverflow(t *testing.T) {
	var backing [64]byte
	c := netsec.NewChecker()
	if _, err := c.Register(unsafe.Pointer(&backing[0]), uintptr(len(backing)), netsec.LayoutOf[byte]()); err != nil {
		t.Fatalf("register: %v", err)
	}

	_, err := c.Slice(unsafe.Pointer(&backing[0]), math.MaxInt, netsec.LayoutOf[uint64]())
	mustViolation(t, err, netsec.KindLengthOverflow)

	_, err = c.Add(unsafe.Pointer(&backing[0]), ^uintptr(0), netsec.LayoutOf[byte]())
	mustViolation(t, err, netsec.KindLengthOverflow)

	_, err = c.Slice(unsafe.Pointer(&backing[0]), -1, netsec.LayoutOf[byte]())
	mustViolation(t, err, netsec.KindNegativeLength)

	_, err = c.SliceAt(unsafe.Pointer(&backing[0]), ^uintptr(0), 4, netsec.LayoutOf[byte]())
	mustViolation(t, err, netsec.KindLengthOverflow)

	_, err = c.SliceUint64(unsafe.Pointer(&backing[0]), math.MaxUint64, netsec.LayoutOf[uint64]())
	mustViolation(t, err, netsec.KindLengthOverflow)

	if got := len(c.Violations()); got != 5 {
		t.Fatalf("recorded %d violations, want 5", got)
	}
}

func TestMisalignedTarget(t *testing.T) {
	var backing [64]byte
	c := netsec.NewChecker()
	if _, err := c.Register(unsafe.Pointer(&backing[0]), uintptr(len(backing)), netsec.LayoutOf[byte]()); err != nil {
		t.Fatalf("register: %v", err)
	}

	base := uintptr(unsafe.Pointer(&backing[0]))
	var badOffset uintptr
	for offset := uintptr(1); offset < 8; offset++ {
		if (base+offset)%uintptr(unsafe.Alignof(uint32(0))) != 0 {
			badOffset = offset
			break
		}
	}
	if badOffset == 0 {
		t.Fatal("could not find a misaligned offset")
	}

	_, err := c.Add(unsafe.Pointer(&backing[0]), badOffset, netsec.LayoutOf[uint32]())
	mustViolation(t, err, netsec.KindMisaligned)

	_, err = c.SliceAt(unsafe.Pointer(&backing[0]), badOffset, 2, netsec.LayoutOf[uint32]())
	mustViolation(t, err, netsec.KindMisaligned)
}

func TestCrossingAdjacentObjects(t *testing.T) {
	var backing [64]byte
	c := netsec.NewChecker()
	first, err := c.Register(unsafe.Pointer(&backing[0]), 32, netsec.LayoutOf[byte]())
	if err != nil {
		t.Fatalf("register first: %v", err)
	}
	if _, err := c.Register(unsafe.Pointer(&backing[32]), 32, netsec.LayoutOf[byte]()); err != nil {
		t.Fatalf("register second: %v", err)
	}
	if first.End() != uintptr(unsafe.Pointer(&backing[32])) {
		t.Fatalf("test objects are not adjacent: first ends at 0x%x, second starts at %p",
			first.End(), &backing[32])
	}

	// The target starts inside object one and ends inside object two.
	_, err = c.Add(unsafe.Pointer(&backing[0]), 31, netsec.LayoutOf[uint32]())
	mustViolation(t, err, netsec.KindCrossAllocation)

	// Even a target that fits entirely inside object two is rejected when the
	// arithmetic started in object one.
	_, err = c.Add(unsafe.Pointer(&backing[0]), 32, netsec.LayoutOf[uint32]())
	mustViolation(t, err, netsec.KindCrossAllocation)

	// The same range is legal when the source really is object two.
	if _, err := c.Add(unsafe.Pointer(&backing[32]), 0, netsec.LayoutOf[uint32]()); err != nil {
		t.Fatalf("legal access to second object rejected: %v", err)
	}

	_, err = c.SliceAt(unsafe.Pointer(&backing[0]), 28, 3, netsec.LayoutOf[uint32]())
	mustViolation(t, err, netsec.KindCrossAllocation)
}

func TestOutOfBoundsAndUnregistered(t *testing.T) {
	var backing [16]byte
	var other int
	c := netsec.NewChecker()
	if _, err := c.Register(unsafe.Pointer(&backing[0]), uintptr(len(backing)), netsec.LayoutOf[byte]()); err != nil {
		t.Fatalf("register: %v", err)
	}

	_, err := c.Slice(unsafe.Pointer(&backing[0]), 5, netsec.LayoutOf[uint32]())
	mustViolation(t, err, netsec.KindCrossAllocation)

	_, err = c.Add(unsafe.Pointer(&backing[0]), 20, netsec.LayoutOf[byte]())
	mustViolation(t, err, netsec.KindOutOfBounds)

	err = c.CheckPointer(unsafe.Pointer(&other), netsec.LayoutOf[int]())
	mustViolation(t, err, netsec.KindUnregistered)

	err = c.CheckPointer(nil, netsec.LayoutOf[byte]())
	mustViolation(t, err, netsec.KindUnregistered)
}

func TestUintptrSafePointTracking(t *testing.T) {
	var backing [32]byte
	c := netsec.NewChecker()
	if _, err := c.Register(unsafe.Pointer(&backing[0]), uintptr(len(backing)), netsec.LayoutOf[byte]()); err != nil {
		t.Fatalf("register: %v", err)
	}
	p, err := c.Add(unsafe.Pointer(&backing[0]), 8, netsec.LayoutOf[uint32]())
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	u := c.PointerToUintptr(p)
	got, err := c.UintptrToPointer(u, netsec.LayoutOf[uint32]())
	if err != nil {
		t.Fatalf("same-epoch conversion rejected: %v", err)
	}
	if got != p {
		t.Fatalf("converted pointer %p, want %p", got, p)
	}

	epoch := c.SafePoint()
	if epoch == 0 {
		t.Fatal("SafePoint did not advance the epoch")
	}
	_, err = c.UintptrToPointer(u, netsec.LayoutOf[uint32]())
	mustViolation(t, err, netsec.KindStaleUintptr)
	if !errors.Is(err, netsec.ErrStaleUintptr) {
		t.Fatalf("errors.Is(ErrStaleUintptr) failed for %v", err)
	}

	// Re-recording the same address after the safe point establishes a fresh,
	// valid provenance for the new epoch.
	u = c.StoreUintptr(p)
	if _, err := c.LoadPointer(u, netsec.LayoutOf[uint32]()); err != nil {
		t.Fatalf("fresh post-safe-point conversion rejected: %v", err)
	}
}

func TestUintptrMustBeTracked(t *testing.T) {
	var backing [16]byte
	c := netsec.NewChecker()
	if _, err := c.Register(unsafe.Pointer(&backing[0]), uintptr(len(backing)), netsec.LayoutOf[byte]()); err != nil {
		t.Fatalf("register: %v", err)
	}

	raw := uintptr(unsafe.Pointer(&backing[0]))
	_, err := c.UintptrToPointer(raw, netsec.LayoutOf[byte]())
	mustViolation(t, err, netsec.KindUntrackedUintptr)
	if !errors.Is(err, netsec.ErrUntrackedUintptr) {
		t.Fatalf("errors.Is(ErrUntrackedUintptr) failed for %v", err)
	}
}

func TestUintptrArithmeticPreservesProvenance(t *testing.T) {
	var backing [32]byte
	c := netsec.NewChecker()
	if _, err := c.Register(unsafe.Pointer(&backing[0]), uintptr(len(backing)), netsec.LayoutOf[byte]()); err != nil {
		t.Fatalf("register: %v", err)
	}

	p := unsafe.Pointer(&backing[0])
	u := c.PointerToUintptr(p)

	derived, err := c.AddUintptr(u, 8)
	if err != nil {
		t.Fatalf("AddUintptr: %v", err)
	}
	if _, err := c.UintptrToPointer(derived, netsec.LayoutOf[uint32]()); err != nil {
		t.Fatalf("same-epoch derived conversion rejected: %v", err)
	}

	// Raw integer arithmetic is matched back to the captured allocation while
	// no safe point has occurred.
	raw := u + 8
	if _, err := c.UintptrToPointer(raw, netsec.LayoutOf[uint32]()); err != nil {
		t.Fatalf("same-epoch raw derived conversion rejected: %v", err)
	}

	c.SafePoint()
	_, err = c.UintptrToPointer(derived, netsec.LayoutOf[uint32]())
	mustViolation(t, err, netsec.KindStaleUintptr)

	_, err = c.UintptrToPointer(raw, netsec.LayoutOf[uint32]())
	mustViolation(t, err, netsec.KindStaleUintptr)
}

func TestTrackedUintptrStillChecksRange(t *testing.T) {
	var backing [16]byte
	c := netsec.NewChecker()
	if _, err := c.Register(unsafe.Pointer(&backing[0]), uintptr(len(backing)), netsec.LayoutOf[byte]()); err != nil {
		t.Fatalf("register: %v", err)
	}

	endAddress := uintptr(unsafe.Pointer(&backing[0])) + uintptr(len(backing))
	end := addressToPointer(endAddress)
	u := c.PointerToUintptr(end)
	_, err := c.UintptrToPointer(u, netsec.LayoutOf[byte]())
	mustViolation(t, err, netsec.KindOutOfBounds)
}

func TestRegisterLayoutAndOverflowValidation(t *testing.T) {
	var backing [8]byte
	c := netsec.NewChecker()

	_, err := c.RegisterRaw(unsafe.Pointer(&backing[0]), 8, 4, 3)
	mustViolation(t, err, netsec.KindInvalidLayout)

	_, err = c.RegisterRaw(unsafe.Pointer(&backing[0]), 8, 3, 4)
	mustViolation(t, err, netsec.KindInvalidLayout)

	_, err = c.Register(unsafe.Pointer(&backing[0]), ^uintptr(0), netsec.LayoutOf[byte]())
	mustViolation(t, err, netsec.KindLengthOverflow)

	a, err := c.Register(unsafe.Pointer(&backing[0]), 8, netsec.LayoutOf[uint16]())
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if !c.Unregister(a) {
		t.Fatal("Unregister returned false")
	}
	if _, err := c.Slice(unsafe.Pointer(&backing[0]), 2, netsec.LayoutOf[uint16]()); err == nil {
		t.Fatal("slice succeeded after the allocation was unregistered")
	}
}

func TestSliceUsesRegisteredElementLayout(t *testing.T) {
	var backing [32]byte
	c := netsec.NewChecker()
	if _, err := c.Register(unsafe.Pointer(&backing[0]), uintptr(len(backing)), netsec.LayoutOf[uint32]()); err != nil {
		t.Fatalf("register: %v", err)
	}

	if _, err := c.Slice(unsafe.Pointer(&backing[0]), 8); err != nil {
		t.Fatalf("Slice with registered layout rejected: %v", err)
	}
	if _, err := c.Slice(unsafe.Pointer(&backing[0]), 9); err == nil {
		t.Fatal("Slice accepted a target past the allocation")
	}
}

func TestRegisterSliceAndObjectHelpers(t *testing.T) {
	values := make([]uint32, 8)
	c := netsec.NewChecker()
	if _, err := netsec.RegisterSlice(c, values); err != nil {
		t.Fatalf("RegisterSlice: %v", err)
	}
	if _, err := c.Add(unsafe.Pointer(&values[0]), 28, netsec.LayoutOf[uint32]()); err != nil {
		t.Fatalf("Add into registered slice: %v", err)
	}

	var object uint64
	if _, err := netsec.RegisterObject(c, &object); err != nil {
		t.Fatalf("RegisterObject: %v", err)
	}
	if err := c.CheckPointer(unsafe.Pointer(&object), netsec.LayoutOf[uint64]()); err != nil {
		t.Fatalf("CheckPointer for registered object: %v", err)
	}
}

func TestReinterpretTypedSlice(t *testing.T) {
	var backing [32]byte
	c := netsec.NewChecker()
	if _, err := c.Register(unsafe.Pointer(&backing[0]), uintptr(len(backing)), netsec.LayoutOf[byte]()); err != nil {
		t.Fatalf("register: %v", err)
	}

	words, err := netsec.ReinterpretSlice[uint64](c, unsafe.Pointer(&backing[0]), 4)
	if err != nil {
		t.Fatalf("ReinterpretSlice: %v", err)
	}
	if len(words) != 4 {
		t.Fatalf("len(slice) = %d, want 4", len(words))
	}
	words[3] = math.MaxUint64
	if backing[24] != 0xff || backing[31] != 0xff {
		t.Fatalf("reinterpreted slice did not address the allocation: %v", backing[24:32])
	}

	_, err = netsec.ReinterpretSlice[uint64](c, unsafe.Pointer(&backing[0]), 5)
	mustViolation(t, err, netsec.KindCrossAllocation)
}

func TestDefaultCheckerWrappers(t *testing.T) {
	netsec.Default.Reset()
	t.Cleanup(netsec.Default.Reset)

	var backing [16]byte
	if _, err := netsec.RegisterAllocation(unsafe.Pointer(&backing[0]), 16, netsec.LayoutOf[byte]()); err != nil {
		t.Fatalf("RegisterAllocation: %v", err)
	}
	p, err := netsec.AddChecked(unsafe.Pointer(&backing[0]), 4, netsec.LayoutOf[uint32]())
	if err != nil {
		t.Fatalf("AddChecked: %v", err)
	}
	if err := netsec.CheckAdd(unsafe.Pointer(&backing[0]), 4, netsec.LayoutOf[uint32]()); err != nil {
		t.Fatalf("CheckAdd: %v", err)
	}
	u := netsec.StoreUintptr(p)
	if _, err := netsec.LoadPointer(u, netsec.LayoutOf[uint32]()); err != nil {
		t.Fatalf("LoadPointer: %v", err)
	}
	netsec.SafePoint()
	if _, err := netsec.FromUintptr(u, netsec.LayoutOf[uint32]()); err == nil {
		t.Fatal("stale package-level conversion was accepted")
	}
}
