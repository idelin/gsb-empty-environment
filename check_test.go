package netsec

import (
	"errors"
	"testing"
)

func scalar(k ValueKind) FieldType { return FieldType{Kind: k} }

func ref(t int) FieldType { return FieldType{Kind: Ref, Ref: t} }

func field(t FieldType, mut bool) Field { return Field{Type: t, Mutable: mut} }

// checkExposure asserts that CheckModule reports an ExposureError for the
// given module group index.
func checkExposure(t *testing.T, host RecGroup, module []RecGroup, want int) {
	t.Helper()
	err := CheckModule(host, module)
	var exp *ExposureError
	if !errors.As(err, &exp) {
		t.Fatalf("CheckModule() = %v, want ExposureError for group %d", err, want)
	}
	if exp.GroupIndex != want {
		t.Fatalf("ExposureError.GroupIndex = %d, want %d", exp.GroupIndex, want)
	}
}

// checkClean asserts that CheckModule accepts the module.
func checkClean(t *testing.T, host RecGroup, module []RecGroup) {
	t.Helper()
	if err := CheckModule(host, module); err != nil {
		t.Fatalf("CheckModule() = %v, want nil", err)
	}
}

// hostChain is the host's private group: a 3-member reference cycle
// M0 -> M1 -> M2 -> M0 at base index 10.
func hostChain() RecGroup {
	return RecGroup{Base: 10, Members: []StructType{
		{Super: NoSuper, Fields: []Field{field(scalar(I32), true), field(ref(11), false)}},
		{Super: NoSuper, Fields: []Field{field(scalar(F64), false), field(ref(12), true)}},
		{Super: NoSuper, Fields: []Field{field(scalar(I64), false), field(ref(10), false)}},
	}}
}

func TestIsomorphicRenamingRejected(t *testing.T) {
	host := hostChain()
	// Same group rebuilt at base 0 with members permuted to
	// [M2, M0, M1] and every intra-group reference remapped.
	evil := RecGroup{Base: 0, Members: []StructType{
		{Super: NoSuper, Fields: []Field{field(scalar(I64), false), field(ref(1), false)}},
		{Super: NoSuper, Fields: []Field{field(scalar(I32), true), field(ref(2), false)}},
		{Super: NoSuper, Fields: []Field{field(scalar(F64), false), field(ref(0), true)}},
	}}
	decoy := RecGroup{Base: 3, Members: []StructType{
		{Super: NoSuper, Fields: []Field{field(scalar(I32), false)}},
	}}
	checkExposure(t, host, []RecGroup{decoy, evil}, 1)
}

func TestMutabilityDifferenceAccepted(t *testing.T) {
	host := hostChain()
	// Identical to the host group except M1's second field is immutable.
	mod := RecGroup{Base: 0, Members: []StructType{
		{Super: NoSuper, Fields: []Field{field(scalar(I32), true), field(ref(1), false)}},
		{Super: NoSuper, Fields: []Field{field(scalar(F64), false), field(ref(2), false)}},
		{Super: NoSuper, Fields: []Field{field(scalar(I64), false), field(ref(0), false)}},
	}}
	checkClean(t, host, []RecGroup{mod})
}

func TestReferenceCycleRejected(t *testing.T) {
	// Host: two structs referencing each other, plus a self-reference.
	host := RecGroup{Base: 4, Members: []StructType{
		{Super: NoSuper, Fields: []Field{field(ref(5), false), field(ref(4), false)}},
		{Super: NoSuper, Fields: []Field{field(scalar(I32), false), field(ref(4), false)}},
	}}
	// Module: same cycle rebuilt at base 0.
	evil := RecGroup{Base: 0, Members: []StructType{
		{Super: NoSuper, Fields: []Field{field(ref(1), false), field(ref(0), false)}},
		{Super: NoSuper, Fields: []Field{field(scalar(I32), false), field(ref(0), false)}},
	}}
	checkExposure(t, host, []RecGroup{evil}, 0)
}

func TestSupertypeMatched(t *testing.T) {
	// Host: member 0 extends earlier type 3; member 1 is final.
	host := RecGroup{Base: 5, Members: []StructType{
		{Super: 3, Fields: []Field{field(scalar(I32), false), field(ref(6), false)}},
		{Final: true, Super: NoSuper, Fields: []Field{field(scalar(F64), true)}},
	}}
	same := RecGroup{Base: 5, Members: []StructType{
		{Super: 3, Fields: []Field{field(scalar(I32), false), field(ref(6), false)}},
		{Final: true, Super: NoSuper, Fields: []Field{field(scalar(F64), true)}},
	}}
	checkExposure(t, host, []RecGroup{same}, 0)

	// Same shape but a different supertype index: not equivalent.
	otherSuper := RecGroup{Base: 5, Members: []StructType{
		{Super: 4, Fields: []Field{field(scalar(I32), false), field(ref(6), false)}},
		{Final: true, Super: NoSuper, Fields: []Field{field(scalar(F64), true)}},
	}}
	// Same shape but the supertype is dropped: not equivalent.
	noSuper := RecGroup{Base: 5, Members: []StructType{
		{Super: NoSuper, Fields: []Field{field(scalar(I32), false), field(ref(6), false)}},
		{Final: true, Super: NoSuper, Fields: []Field{field(scalar(F64), true)}},
	}}
	checkClean(t, host, []RecGroup{otherSuper, noSuper})
}

func TestUnrelatedModuleAccepted(t *testing.T) {
	host := hostChain()
	groups := []RecGroup{
		// Different member count.
		{Base: 0, Members: []StructType{
			{Super: NoSuper, Fields: []Field{field(scalar(I32), true), field(ref(1), false)}},
			{Super: NoSuper, Fields: []Field{field(scalar(F64), false), field(ref(0), true)}},
		}},
		// Same member count, different field kinds.
		{Base: 2, Members: []StructType{
			{Super: NoSuper, Fields: []Field{field(scalar(I64), true), field(ref(3), false)}},
			{Super: NoSuper, Fields: []Field{field(scalar(I64), false), field(ref(4), true)}},
			{Super: NoSuper, Fields: []Field{field(scalar(I64), false), field(ref(2), false)}},
		}},
		// No references at all.
		{Base: 5, Members: []StructType{
			{Super: NoSuper, Fields: []Field{field(scalar(F64), false)}},
		}},
	}
	checkClean(t, host, groups)
}

// TestNonEquivalentVariants pins the spec rule: a difference in field
// order, mutability, final flag, supertype or reference target alone
// defeats equivalence.
func TestNonEquivalentVariants(t *testing.T) {
	host := RecGroup{Base: 5, Members: []StructType{
		{Super: NoSuper, Fields: []Field{field(scalar(I32), true), field(ref(6), false)}},
		{Super: NoSuper, Fields: []Field{field(scalar(F64), false), field(ref(5), false)}},
	}}
	// Sanity: the unmodified copy rebuilt at base 0 is equivalent.
	checkExposure(t, host, []RecGroup{{Base: 0, Members: []StructType{
		{Super: NoSuper, Fields: []Field{field(scalar(I32), true), field(ref(1), false)}},
		{Super: NoSuper, Fields: []Field{field(scalar(F64), false), field(ref(0), false)}},
	}}}, 0)

	variants := map[string]RecGroup{
		"field order": {Base: 0, Members: []StructType{
			{Super: NoSuper, Fields: []Field{field(ref(1), false), field(scalar(I32), true)}},
			{Super: NoSuper, Fields: []Field{field(scalar(F64), false), field(ref(0), false)}},
		}},
		"mutability": {Base: 0, Members: []StructType{
			{Super: NoSuper, Fields: []Field{field(scalar(I32), false), field(ref(1), false)}},
			{Super: NoSuper, Fields: []Field{field(scalar(F64), false), field(ref(0), false)}},
		}},
		"final flag": {Base: 0, Members: []StructType{
			{Final: true, Super: NoSuper, Fields: []Field{field(scalar(I32), true), field(ref(1), false)}},
			{Super: NoSuper, Fields: []Field{field(scalar(F64), false), field(ref(0), false)}},
		}},
		// Member 1 gains a supertype (earlier type 0) the host lacks.
		"supertype": {Base: 1, Members: []StructType{
			{Super: NoSuper, Fields: []Field{field(scalar(I32), true), field(ref(2), false)}},
			{Super: 0, Fields: []Field{field(scalar(F64), false), field(ref(1), false)}},
		}},
		// Member 1 references itself instead of closing the cycle.
		"ref target": {Base: 0, Members: []StructType{
			{Super: NoSuper, Fields: []Field{field(scalar(I32), true), field(ref(1), false)}},
			{Super: NoSuper, Fields: []Field{field(scalar(F64), false), field(ref(1), false)}},
		}},
		// Member 1's back-edge is replaced by a reference to an earlier
		// type sitting at the same absolute index as the host's member 0.
		"external ref": {Base: 6, Members: []StructType{
			{Super: NoSuper, Fields: []Field{field(scalar(I32), true), field(ref(7), false)}},
			{Super: NoSuper, Fields: []Field{field(scalar(F64), false), field(ref(5), false)}},
		}},
	}
	for name, g := range variants {
		t.Run(name, func(t *testing.T) {
			if err := g.Validate(); err != nil {
				t.Fatalf("variant is malformed: %v", err)
			}
			checkClean(t, host, []RecGroup{g})
		})
	}
}

func TestMalformedGroupsRejected(t *testing.T) {
	host := hostChain()
	// Reference target past the end of the module's type space.
	bad := RecGroup{Base: 0, Members: []StructType{
		{Super: NoSuper, Fields: []Field{field(ref(7), false)}},
	}}
	err := CheckModule(host, []RecGroup{bad})
	if err == nil {
		t.Fatal("CheckModule() = nil, want validation error")
	}
	var exp *ExposureError
	if errors.As(err, &exp) {
		t.Fatalf("CheckModule() = %v, want validation error, not ExposureError", err)
	}
}
