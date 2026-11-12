package netsec

import (
	"bytes"
	"errors"
	"testing"
)

func whitelist(names ...string) map[string]struct{} {
	wl := make(map[string]struct{}, len(names))
	for _, name := range names {
		wl[name] = struct{}{}
	}
	return wl
}

func inspect(data []byte, policy Policy) error {
	return Inspect(bytes.NewReader(data), policy)
}

// hString encodes a short Hessian2 final string (length < 32).
func hString(s string) []byte {
	b := []byte{byte(len(s))}
	return append(b, s...)
}

// hClass encodes a C class definition with the given field names.
func hClass(name string, fields ...string) []byte {
	var b []byte
	b = append(b, 0x43)
	b = append(b, hString(name)...)
	b = append(b, byte(0x90+len(fields))) // direct int field count
	for _, field := range fields {
		b = append(b, hString(field)...)
	}
	return b
}

func TestWhitelistRejectsForeignClass(t *testing.T) {
	stream := hClass("com.attacker.Evil", "payload")
	err := inspect(stream, DefaultPolicy(whitelist("com.safe.Account")))
	if !errors.Is(err, ErrClassNotAllowed) {
		t.Fatalf("expected ErrClassNotAllowed, got %v", err)
	}
}

func TestNilWhitelistFailsClosed(t *testing.T) {
	stream := hClass("com.safe.Account", "id")
	if err := inspect(stream, DefaultPolicy(nil)); !errors.Is(err, ErrClassNotAllowed) {
		t.Fatalf("expected ErrClassNotAllowed with nil whitelist, got %v", err)
	}
}

func TestReferenceMustPointToExistingObject(t *testing.T) {
	// Q 0x000005 references object index 5, but nothing has appeared yet.
	stream := []byte{0x51, 0x00, 0x00, 0x05}
	err := inspect(stream, DefaultPolicy(whitelist()))
	if !errors.Is(err, ErrBadReference) {
		t.Fatalf("expected ErrBadReference, got %v", err)
	}
}

func TestDeclaredListOverBudgetRejected(t *testing.T) {
	policy := DefaultPolicy(whitelist())
	policy.MaxContainer = 4

	// 'X' typed fixed list, type "int", length 5 (direct int 0x95).
	stream := []byte{0x58}
	stream = append(stream, hString("int")...)
	stream = append(stream, 0x95)

	err := inspect(stream, policy)
	if !errors.Is(err, ErrLengthExceeded) {
		t.Fatalf("expected ErrLengthExceeded, got %v", err)
	}
}

func TestCompactObjectRequiresWhitelistedClass(t *testing.T) {
	policy := DefaultPolicy(whitelist("com.safe.Account"))

	// Class definition with one field, then a compact object 0x60 whose field
	// is a direct integer.
	stream := hClass("com.safe.Account", "id")
	stream = append(stream, 0x60, 0x90)

	if err := inspect(stream, policy); err != nil {
		t.Fatalf("expected legal compact object to pass, got %v", err)
	}
}

func TestLegalNestedObjectsAndBackReferenceCycle(t *testing.T) {
	policy := DefaultPolicy(whitelist("com.safe.Node"))

	// class Node { value; next }
	var stream []byte
	stream = append(stream, hClass("com.safe.Node", "value", "next")...)

	// O 0x00 named object: instance 0 becomes referenceable first.
	stream = append(stream, 0x4f, 0x90)
	stream = append(stream, 0x91) // value = 1
	// next = Q 0x000000, a legal back reference that closes a cycle.
	stream = append(stream, 0x51, 0x00, 0x00, 0x00)

	if err := inspect(stream, policy); err != nil {
		t.Fatalf("expected legal nested/cyclic object to pass, got %v", err)
	}
}

func TestUnknownTypeByteRejected(t *testing.T) {
	// 0x70-0x79 is not part of the Hessian2 value grammar.
	stream := []byte{0x70}
	err := inspect(stream, DefaultPolicy(whitelist()))
	if !errors.Is(err, ErrUnknownType) {
		t.Fatalf("expected ErrUnknownType, got %v", err)
	}
}

func TestReservedByteRejected(t *testing.T) {
	stream := []byte{0x38}
	err := inspect(stream, DefaultPolicy(whitelist()))
	if !errors.Is(err, ErrUnknownType) {
		t.Fatalf("expected ErrUnknownType for reserved byte, got %v", err)
	}
}

func TestDepthBudgetEnforced(t *testing.T) {
	policy := DefaultPolicy(whitelist())
	policy.MaxDepth = 3

	// Three length-1 compact lists wrapping an empty compact list, reaching
	// depth 4 which exceeds the budget of 3.
	stream := []byte{0x41, 0x41, 0x41, 0x40}
	err := inspect(stream, policy)
	if !errors.Is(err, ErrDepthExceeded) {
		t.Fatalf("expected ErrDepthExceeded, got %v", err)
	}
}

func TestObjectBudgetEnforced(t *testing.T) {
	policy := DefaultPolicy(whitelist("com.safe.Pair"))
	policy.MaxObjects = 2

	stream := hClass("com.safe.Pair", "a", "b")
	// Three zero-field compact objects would exceed the two-instance budget.
	stream = append(stream, 0x60, 0x60, 0x60)

	err := inspect(stream, policy)
	if !errors.Is(err, ErrObjectLimit) {
		t.Fatalf("expected ErrObjectLimit, got %v", err)
	}
}

func TestTruncatedStreamRejected(t *testing.T) {
	// 'I' promises four bytes but the stream ends immediately.
	stream := []byte{0x49, 0x00, 0x00}
	err := inspect(stream, DefaultPolicy(whitelist()))
	if !errors.Is(err, ErrMalformedStream) {
		t.Fatalf("expected ErrMalformedStream, got %v", err)
	}
}

func TestVariableListReferenceAndBudget(t *testing.T) {
	policy := DefaultPolicy(whitelist())
	policy.MaxContainer = 2

	// 'U' untyped variable list with three null values then 'z'.
	stream := []byte{0x55, 0x4e, 0x4e, 0x4e, 0x7a}
	err := inspect(stream, policy)
	if !errors.Is(err, ErrLengthExceeded) {
		t.Fatalf("expected ErrLengthExceeded for over-long variable list, got %v", err)
	}
}

func TestMapEntryBudgetAndTypedMap(t *testing.T) {
	policy := DefaultPolicy(whitelist())
	policy.MaxContainer = 1

	// 'H' typed map with two string/null entries then 'z'.
	stream := []byte{0x48}
	stream = append(stream, hString("java.util.HashMap")...)
	stream = append(stream, hString("k1")...)
	stream = append(stream, 0x4e)
	stream = append(stream, hString("k2")...)
	stream = append(stream, 0x4e, 0x7a)

	err := inspect(stream, policy)
	if !errors.Is(err, ErrLengthExceeded) {
		t.Fatalf("expected ErrLengthExceeded for over-long map, got %v", err)
	}
}

func TestLegalPrimitivesAndContainers(t *testing.T) {
	policy := DefaultPolicy(whitelist())

	var stream []byte
	stream = append(stream, 0x4e)             // null
	stream = append(stream, 0x54)             // true
	stream = append(stream, 0x46)             // false
	stream = append(stream, 0x92)             // direct int 2
	stream = append(stream, 0x49, 0, 0, 1, 0) // I 256
	stream = append(stream, 0x4c, 0, 0, 0, 0, 0, 0, 0, 7)
	stream = append(stream, 0x5b) // double 0.0
	stream = append(stream, 0x5f, 0, 0, 0, 0, 0, 0, 0, 0)
	stream = append(stream, 0x4a, 0, 0, 0, 0, 0, 0, 0, 0) // date (ms precision)
	stream = append(stream, 0x4b, 0, 0, 0, 0)             // date (minute precision)
	stream = append(stream, hString("hello")...)
	stream = append(stream, 0x23, 1, 2, 3) // compact binary length 3
	stream = append(stream, 0x40)          // empty compact list

	if err := inspect(stream, policy); err != nil {
		t.Fatalf("expected legal primitives to pass, got %v", err)
	}
}

func TestListReferenceIndex(t *testing.T) {
	policy := DefaultPolicy(whitelist())

	// Compact list with one element (object slot 0), then a Q reference to it.
	stream := []byte{0x41, 0x4e, 0x51, 0x00, 0x00, 0x00}
	if err := inspect(stream, policy); err != nil {
		t.Fatalf("expected reference to earlier list to pass, got %v", err)
	}
}

func TestNamedObjectAndWhitelistBoundary(t *testing.T) {
	policy := DefaultPolicy(whitelist("com.safe.A", "com.safe.B"))

	var stream []byte
	stream = append(stream, hClass("com.safe.A")...)
	stream = append(stream, hClass("com.safe.B", "ref")...)
	stream = append(stream, 0x60) // object 0: class A
	stream = append(stream, 0x4f, 0x91)
	stream = append(stream, 0x51, 0x00, 0x00, 0x00) // field ref -> object 0

	if err := inspect(stream, policy); err != nil {
		t.Fatalf("expected named object with valid reference to pass, got %v", err)
	}
}

func TestSelfReferenceCycleIsLegal(t *testing.T) {
	policy := DefaultPolicy(whitelist("com.safe.Self"))

	var stream []byte
	stream = append(stream, hClass("com.safe.Self", "self")...)
	stream = append(stream, 0x60)
	stream = append(stream, 0x51, 0x00, 0x00, 0x00) // points at itself

	if err := inspect(stream, policy); err != nil {
		t.Fatalf("expected self reference cycle to pass, got %v", err)
	}
}

func TestFixedUntypedListW(t *testing.T) {
	policy := DefaultPolicy(whitelist())

	// 'W' fixed untyped list, length 2 (direct int), two null elements.
	stream := []byte{0x57, 0x92, 0x4e, 0x4e}
	if err := inspect(stream, policy); err != nil {
		t.Fatalf("expected W list to pass, got %v", err)
	}
}

func TestVariableTypedListV(t *testing.T) {
	policy := DefaultPolicy(whitelist())

	// 'V' typed variable list, type string, one int, then 'z'.
	stream := []byte{0x56}
	stream = append(stream, hString("java.util.List")...)
	stream = append(stream, 0x90, 0x7a)

	if err := inspect(stream, policy); err != nil {
		t.Fatalf("expected V list to pass, got %v", err)
	}
}

func TestUntypedMapM(t *testing.T) {
	policy := DefaultPolicy(whitelist())

	// 'M' untyped map: one string/null entry then 'z'.
	stream := []byte{0x4d}
	stream = append(stream, hString("k")...)
	stream = append(stream, 0x4e, 0x7a)

	if err := inspect(stream, policy); err != nil {
		t.Fatalf("expected M map to pass, got %v", err)
	}
}

func TestClassIndexOutOfRangeRejected(t *testing.T) {
	policy := DefaultPolicy(whitelist())
	// Compact object 0x60 with no preceding class definition.
	stream := []byte{0x60}
	err := inspect(stream, policy)
	if !errors.Is(err, ErrClassIndex) {
		t.Fatalf("expected ErrClassIndex, got %v", err)
	}
}

func TestTrailingGarbageRejected(t *testing.T) {
	policy := DefaultPolicy(whitelist())
	// Valid null followed by an unrecognized byte.
	stream := []byte{0x4e, 0x71}
	err := inspect(stream, policy)
	if !errors.Is(err, ErrUnknownType) {
		t.Fatalf("expected ErrUnknownType for trailing byte, got %v", err)
	}
}

func TestCompactIntegerAndLongEncodings(t *testing.T) {
	policy := DefaultPolicy(whitelist())

	var stream []byte
	stream = append(stream, 0x80)             // direct int -16
	stream = append(stream, 0xbf)             // direct int 47
	stream = append(stream, 0xc0, 0x00)       // two-octet int
	stream = append(stream, 0xd0, 0x00, 0x00) // three-octet int
	stream = append(stream, 0xd8, 0x00)       // two-octet long
	stream = append(stream, 0xf0, 0x00, 0x00) // three-octet long
	stream = append(stream, 0xf8)             // direct long -8
	stream = append(stream, 0xff)             // direct long 15
	// A following value must still align correctly after the payloads.
	stream = append(stream, 0x4e) // null

	if err := inspect(stream, policy); err != nil {
		t.Fatalf("expected compact numeric forms to parse, got %v", err)
	}
}

func TestChunkedStringReassembledAndBounded(t *testing.T) {
	policy := DefaultPolicy(whitelist())

	// 'R' non-final chunk of length 1, then a compact final string.
	stream := []byte{0x52, 0x00, 0x01, 'a', 0x01, 'b', 0x4e}
	if err := inspect(stream, policy); err != nil {
		t.Fatalf("expected chunked string to parse, got %v", err)
	}
}

func TestMapEntryIsReferenceable(t *testing.T) {
	policy := DefaultPolicy(whitelist())

	// 'M' map whose value is a compact list, then a Q reference to that list.
	stream := []byte{0x4d}
	stream = append(stream, hString("k")...)
	stream = append(stream, 0x40) // empty compact list -> object 0
	stream = append(stream, 0x7a)
	stream = append(stream, 0x51, 0x00, 0x00, 0x00)

	if err := inspect(stream, policy); err != nil {
		t.Fatalf("expected reference to map value list to pass, got %v", err)
	}
}
