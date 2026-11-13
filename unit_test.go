package netsec

import (
	"errors"
	"strings"
	"testing"
)

func TestUnitVersionValidatesEachComponentAndFreezesContent(t *testing.T) {
	spec := makeUnitSpec("u", 7, 6, "A")
	originalFirstCode := spec.Code[0]
	var calls []string
	validators := Validators{
		Code: func(c CodeContent) error {
			calls = append(calls, "code")
			c.Code[0] = 'X'
			return nil
		},
		Boundaries: func(c BoundaryContent) error {
			calls = append(calls, "boundaries")
			c.Functions[0].Name = "mutated"
			c.Instructions[0].PC = 999
			return nil
		},
		StackMap: func(c StackMapContent) error {
			calls = append(calls, "stackMap")
			c.Entries[0].PointerBitmap[0] = 0xff
			return nil
		},
		ExceptionTable: func(c ExceptionContent) error {
			calls = append(calls, "exceptionTable")
			c.Entries[0].HandlerPC = 999
			return nil
		},
		CallTargets: func(c CallTargetContent) error {
			calls = append(calls, "callTargets")
			c.Entries[0].UnitID = "mutated"
			return nil
		},
	}
	u, err := NewUnitVersion(spec, validators)
	if err != nil {
		t.Fatalf("NewUnitVersion: %v", err)
	}
	wantCalls := []string{"code", "boundaries", "stackMap", "exceptionTable", "callTargets"}
	if strings.Join(calls, ",") != strings.Join(wantCalls, ",") {
		t.Fatalf("validator order = %v, want %v", calls, wantCalls)
	}
	if got := u.Code()[0]; got != originalFirstCode {
		t.Fatalf("validator mutated stored code: got %q", got)
	}
	if got := u.Functions()[0].Name; got != "f" {
		t.Fatalf("validator mutated function boundary: %q", got)
	}
	if got := u.StackMap()[0].PointerBitmap[0]; got != 'A' {
		t.Fatalf("validator mutated stack map: %#x", got)
	}
	if got := u.ExceptionTable()[0].HandlerPC; got != 0x30 {
		t.Fatalf("validator mutated exception table: %#x", got)
	}
	if got := u.CallTargets()[0].UnitID; got != "callee" {
		t.Fatalf("validator mutated call targets: %q", got)
	}

	// Accessors must hand out private copies too.
	code := u.Code()
	code[0] = 'Y'
	if got := u.Code()[0]; got != originalFirstCode {
		t.Fatalf("accessor exposed internal code: got %q", got)
	}
	stack := u.StackMap()
	stack[0].PointerBitmap[0] = 0
	if got := u.StackMap()[0].PointerBitmap[0]; got != 'A' {
		t.Fatalf("accessor exposed internal stack map: %#x", got)
	}
}

func TestUnitVersionValidatorFailureNamesComponent(t *testing.T) {
	for _, component := range []string{"code", "boundaries", "stackMap", "exceptionTable", "callTargets"} {
		t.Run(component, func(t *testing.T) {
			var calls []string
			_, err := NewUnitVersion(makeUnitSpec("u", 1, 0, "A"), recordingValidators(&calls, component))
			if err == nil {
				t.Fatalf("expected %s validation failure", component)
			}
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("error %v is not ValidationError", err)
			}
			if ve.Component != component {
				t.Fatalf("failed component = %q, want %q", ve.Component, component)
			}
			if len(calls) == 0 || calls[len(calls)-1] != component {
				t.Fatalf("calls = %v, want failure at %s", calls, component)
			}
		})
	}
}

func TestDigestRecomputationDetectsTampering(t *testing.T) {
	u := makeUnit(t, "u", 1, 0, "A")
	u.digests.Code[0] ^= 0xff
	if err := u.Verify(Validators{}); !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("tampered digest error = %v, want ErrDigestMismatch", err)
	}
	u.digests.Code[0] ^= 0xff
	if err := u.Verify(Validators{}); err != nil {
		t.Fatalf("restored unit does not verify: %v", err)
	}

	u.code[0] ^= 0xff
	if err := u.Verify(Validators{}); !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("tampered code error = %v, want ErrDigestMismatch", err)
	}
}

func TestManifestRootIsOrderIndependentAndCoversLineage(t *testing.T) {
	a := makeUnit(t, "a", 1, 0, "A")
	b := makeUnit(t, "b", 1, 0, "B")
	m1 := mustManifest(t, []*UnitVersion{a, b}, nil)
	m2 := mustManifest(t, []*UnitVersion{b, a}, nil)
	if m1.Root() != m2.Root() {
		t.Fatalf("manifest root depends on input order: %s != %s", m1.Root(), m2.Root())
	}
	child := mustManifest(t, []*UnitVersion{a, b}, m1)
	if child.Root() == m1.Root() {
		t.Fatal("child manifest root does not cover lineage")
	}
	if child.ParentRoot() != m1.Root() {
		t.Fatalf("parent root = %s, want %s", child.ParentRoot(), m1.Root())
	}
}

func TestManifestRejectsDuplicateUnitsAndTamperedUnit(t *testing.T) {
	a := makeUnit(t, "a", 1, 0, "A")
	if _, err := BuildManifest([]*UnitVersion{a, a}, nil, Validators{}); !errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("duplicate unit error = %v", err)
	}
	m := mustManifest(t, []*UnitVersion{a}, nil)
	a.code[0] ^= 1
	if err := m.Verify(Validators{}); !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("tampered manifest error = %v, want ErrDigestMismatch", err)
	}
}
