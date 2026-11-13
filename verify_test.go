package netsec

import (
	"reflect"
	"strings"
	"testing"
)

// testHierarchy: Object is the root; Throwable and A are its children;
// B extends A.
func testHierarchy(t *testing.T) *Hierarchy {
	t.Helper()
	h, err := NewHierarchy(map[string]string{
		"Object":    "",
		"Throwable": "Object",
		"A":         "Object",
		"B":         "A",
	})
	if err != nil {
		t.Fatalf("NewHierarchy: %v", err)
	}
	return h
}

func expectReject(t *testing.T, err *VerifyError, pc int, path []int, substr string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected rejection at pc %d, got success", pc)
	}
	if err.PC != pc {
		t.Fatalf("rejected at pc %d, want pc %d (err: %v)", err.PC, pc, err)
	}
	if path != nil && !reflect.DeepEqual(err.Path, path) {
		t.Fatalf("rejection path %v, want %v (err: %v)", err.Path, path, err)
	}
	if !strings.Contains(err.Msg, substr) {
		t.Fatalf("rejection message %q, want substring %q", err.Msg, substr)
	}
}

// Two new instructions of the same class produce distinct identities:
// init only initializes aliases of its own receiver, so the first object
// is still uninitialized when used.
func TestTwoNewsSameClassDistinctIdentity(t *testing.T) {
	h := testHierarchy(t)
	p := &Program{
		NumLocals: 1,
		Code: []Instr{
			New("A"),  // 0: uninit id 0
			Astore(0), // 1: slot0 = id 0
			New("A"),  // 2: uninit id 2
			Init("A"), // 3: initializes id 2 only
			Aload(0),  // 4: still uninitialized id 0
			Use("A"),  // 5: must reject
			Return(),  // 6
		},
	}
	expectReject(t, Verify(h, p), 5, []int{0, 1, 2, 3, 4, 5}, "initialized")
}

// Two uninitialized references of the same class but different identities
// may not merge on the operand stack.
func TestUninitializedIdentitiesDoNotMerge(t *testing.T) {
	h := testHierarchy(t)
	p := &Program{
		Code: []Instr{
			Branch(1, 3), // 0
			New("A"),     // 1: uninit id 1
			Jmp(4),       // 2
			New("A"),     // 3: uninit id 3
			Init("A"),    // 4: stack merge of id 1 vs id 3 must reject
			Return(),     // 5
		},
	}
	err := Verify(h, p)
	expectReject(t, err, 4, nil, "incompatible stack types")
	if len(err.Path) == 0 || err.Path[len(err.Path)-1] != 4 {
		t.Fatalf("path %v should end at the merge point 4", err.Path)
	}
}

// When the constructor throws, the exception edge poisons the failed
// object's aliases in the locals; a handler that keeps using them is
// rejected.
func TestConstructorFailureHandlerCannotUsePoisonedSlot(t *testing.T) {
	h := testHierarchy(t)
	p := &Program{
		NumLocals: 1,
		Code: []Instr{
			New("A"),  // 0
			Dup(),     // 1
			Astore(0), // 2
			Init("A"), // 3: handler at 7
			Aload(0),  // 4: normal edge: initialized
			Use("A"),  // 5
			Return(),  // 6
			Aload(0),  // 7: handler: slot 0 was poisoned, must reject
			Use("A"),  // 8
			Return(),  // 9
		},
		Handlers: map[int][]int{3: {7}},
	}
	expectReject(t, Verify(h, p), 7, []int{0, 1, 2, 3, 7}, "unavailable")
}

// The normal init edge initializes every alias of the receiver's id,
// including copies sitting in local slots.
func TestNormalEdgeInitializesAliases(t *testing.T) {
	h := testHierarchy(t)
	p := &Program{
		NumLocals: 1,
		Code: []Instr{
			New("A"),  // 0
			Dup(),     // 1: two aliases of id 0
			Astore(0), // 2: slot0 aliases id 0
			Init("A"), // 3: normal edge initializes slot0 too
			Aload(0),  // 4: initialized A
			Use("A"),  // 5
			Return(),  // 6
		},
	}
	if err := Verify(h, p); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
}

// Two constructor sites share one exception handler. Each edge poisons
// only its own object's aliases; the merged handler state keeps the
// Throwable and nothing usable else, so a handler that only consumes the
// Throwable verifies.
func TestSafeSharedExceptionHandler(t *testing.T) {
	h := testHierarchy(t)
	p := &Program{
		NumLocals: 2,
		Code: []Instr{
			New("A"),         // 0
			Dup(),            // 1
			Astore(0),        // 2
			Init("A"),        // 3: handler at 11
			New("B"),         // 4
			Dup(),            // 5
			Astore(1),        // 6
			Init("B"),        // 7: handler at 11
			Aload(0),         // 8
			Use("A"),         // 9
			Return(),         // 10
			Use("Throwable"), // 11: shared handler consumes the Throwable
			Return(),         // 12
		},
		Handlers: map[int][]int{3: {11}, 7: {11}},
	}
	if err := Verify(h, p); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
}

// The shared handler is only safe because the poisoned slots merge to
// unavailable; touching one of them in the handler must reject.
func TestSharedHandlerCannotUseEitherSlot(t *testing.T) {
	h := testHierarchy(t)
	p := &Program{
		NumLocals: 2,
		Code: []Instr{
			New("A"),  // 0
			Dup(),     // 1
			Astore(0), // 2
			Init("A"), // 3: handler at 9
			New("B"),  // 4
			Dup(),     // 5
			Astore(1), // 6
			Init("B"), // 7: handler at 9
			Return(),  // 8
			Aload(0),  // 9: poisoned on one edge, merged to unavailable
			Use("A"),  // 10
			Return(),  // 11
		},
		Handlers: map[int][]int{3: {9}, 7: {9}},
	}
	expectReject(t, Verify(h, p), 9, nil, "unavailable")
}

// A back edge may never carry an uninitialized reference.
func TestBackEdgeCarryingUninitializedRejected(t *testing.T) {
	h := testHierarchy(t)
	p := &Program{
		Code: []Instr{
			New("A"), // 0
			Jmp(0),   // 1: back edge carries uninit id 0
		},
	}
	expectReject(t, Verify(h, p), 0, []int{0, 1, 0}, "back edge")
}

// return requires an empty operand stack.
func TestReturnRequiresEmptyStack(t *testing.T) {
	h := testHierarchy(t)
	p := &Program{
		Code: []Instr{
			New("A"),  // 0
			Dup(),     // 1
			Init("A"), // 2: one alias remains on the stack, now initialized
			Return(),  // 3: stack not empty
		},
	}
	expectReject(t, Verify(h, p), 3, []int{0, 1, 2, 3}, "non-empty")
}

// throw requires an initialized Throwable.
func TestThrowRequiresInitializedThrowable(t *testing.T) {
	h := testHierarchy(t)
	bad := &Program{
		Code: []Instr{
			New("A"),  // 0
			Dup(),     // 1
			Init("A"), // 2
			Throw(),   // 3: A is not a Throwable
		},
	}
	expectReject(t, Verify(h, bad), 3, []int{0, 1, 2, 3}, "Throwable")

	good := &Program{
		Code: []Instr{
			New("Throwable"),  // 0
			Dup(),             // 1
			Init("Throwable"), // 2
			Throw(),           // 3
		},
	}
	if err := Verify(h, good); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
}

// Initialized references of different classes merge to their nearest
// common ancestor on the operand stack.
func TestInitializedMergeUsesCommonAncestor(t *testing.T) {
	h := testHierarchy(t)
	p := &Program{
		Code: []Instr{
			Branch(1, 5), // 0
			New("A"),     // 1
			Dup(),        // 2
			Init("A"),    // 3
			Jmp(9),       // 4
			New("B"),     // 5
			Dup(),        // 6
			Init("B"),    // 7
			Jmp(9),       // 8
			Use("A"),     // 9: merged type is LCA(A, B) = A
			Return(),     // 10
		},
	}
	if err := Verify(h, p); err != nil {
		t.Fatalf("expected success, got %v", err)
	}

	// The merged type is only A, so using it as a B must reject.
	p2 := &Program{
		Code: []Instr{
			Branch(1, 5), // 0
			New("A"),     // 1
			Dup(),        // 2
			Init("A"),    // 3
			Jmp(9),       // 4
			New("B"),     // 5
			Dup(),        // 6
			Init("B"),    // 7
			Jmp(9),       // 8
			Use("B"),     // 9: A is not a B
			Return(),     // 10
		},
	}
	expectReject(t, Verify(h, p2), 9, nil, "subclass")
}

// A failed new produces no object: the handler sees only the Throwable.
func TestNewExceptionEdgeProducesNoObject(t *testing.T) {
	h := testHierarchy(t)
	p := &Program{
		Code: []Instr{
			New("A"),         // 0: handler at 2
			Return(),         // 1: would reject (uninit on stack) if reached normally...
			Use("Throwable"), // 2: handler: stack is exactly [Throwable]
			Return(),         // 3
		},
		Handlers: map[int][]int{0: {2}},
	}
	// pc 1 rejects because new's normal edge leaves the uninitialized
	// reference on the stack; the handler path itself is fine.
	expectReject(t, Verify(h, p), 1, []int{0, 1}, "non-empty")

	p2 := &Program{
		Code: []Instr{
			New("A"),         // 0: handler at 3
			Init("A"),        // 1
			Return(),         // 2
			Use("Throwable"), // 3
			Return(),         // 4
		},
		Handlers: map[int][]int{0: {3}},
	}
	if err := Verify(h, p2); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
}

// Hierarchy validation: cycles and dangling parents are rejected.
func TestHierarchyValidation(t *testing.T) {
	if _, err := NewHierarchy(map[string]string{"A": "B", "B": "A"}); err == nil {
		t.Fatal("expected cycle rejection")
	}
	if _, err := NewHierarchy(map[string]string{"A": "Missing"}); err == nil {
		t.Fatal("expected dangling parent rejection")
	}
}
