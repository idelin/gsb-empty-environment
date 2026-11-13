package netsec

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

var (
	// ErrInvalidUnit reports malformed or structurally invalid unit content.
	ErrInvalidUnit = errors.New("netsec: invalid code unit")
	// ErrDigestMismatch reports content whose recomputed digest does not match
	// the digest stored in the immutable version.
	ErrDigestMismatch = errors.New("netsec: digest mismatch")
	// ErrInvalidManifest reports malformed or incomplete manifests.
	ErrInvalidManifest = errors.New("netsec: invalid manifest")
)

// Range is a half-open interval [Start, End).
type Range struct {
	Start uint64
	End   uint64
}

// Contains reports whether pc is in [Start, End).
func (r Range) Contains(pc uint64) bool {
	return r.Start <= pc && pc < r.End
}

// FunctionBoundary describes one function's instruction range.
type FunctionBoundary struct {
	Name  string
	Start uint64
	End   uint64
}

// InstructionBoundary describes one native instruction.
type InstructionBoundary struct {
	PC   uint64
	Size uint32
}

// StackMapEntry describes the stack layout at PC.
type StackMapEntry struct {
	PC            uint64
	FrameSize     uint64
	PointerBitmap []byte
}

// ExceptionEntry maps a protected native range to a handler.
type ExceptionEntry struct {
	StartPC   uint64
	EndPC     uint64
	HandlerPC uint64
	Action    string
}

// CallTarget identifies the callee of a call site.
type CallTarget struct {
	CallSite   uint64
	UnitID     string
	Generation uint64
	Offset     uint64
}

// CodeUnitSpec is the mutable input used to construct an immutable
// UnitVersion. All slice fields are copied by NewUnitVersion.
type CodeUnitSpec struct {
	UnitID           string
	Generation       uint64
	ParentGeneration uint64
	Code             []byte

	Functions      []FunctionBoundary
	Instructions   []InstructionBoundary
	StackMap       []StackMapEntry
	ExceptionTable []ExceptionEntry
	CallTargets    []CallTarget

	// PatchRanges are the address ranges replaced when this unit is updated.
	// The zero value means the entire code blob.
	PatchRanges []Range
}

// CodeContent is passed to a code validator. The slices are private copies.
type CodeContent struct {
	UnitID           string
	Generation       uint64
	ParentGeneration uint64
	Code             []byte
}

// BoundaryContent is passed to a function/instruction boundary validator.
type BoundaryContent struct {
	Functions    []FunctionBoundary
	Instructions []InstructionBoundary
}

// StackMapContent is passed to a stack-map validator.
type StackMapContent struct {
	Entries []StackMapEntry
}

// ExceptionContent is passed to an exception-table validator.
type ExceptionContent struct {
	Entries []ExceptionEntry
}

// CallTargetContent is passed to a call-target validator.
type CallTargetContent struct {
	Entries []CallTarget
}

// Validators are invoked separately for every metadata component before a
// manifest root is generated. A nil callback accepts that component.
type Validators struct {
	Code           func(CodeContent) error
	Boundaries     func(BoundaryContent) error
	StackMap       func(StackMapContent) error
	ExceptionTable func(ExceptionContent) error
	CallTargets    func(CallTargetContent) error
}

// ValidationError identifies the component whose callback failed.
type ValidationError struct {
	Component string
	Err       error
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("netsec: %s validation failed: %v", e.Component, e.Err)
}

func (e *ValidationError) Unwrap() error { return e.Err }

// UnitDigests is the complete digest set of a UnitVersion.
type UnitDigests struct {
	Code           Digest
	Boundaries     Digest
	StackMap       Digest
	ExceptionTable Digest
	CallTargets    Digest
}

// UnitVersion is an immutable code unit version. All contents are deep-copied
// at construction; accessors return copies and never expose internal storage.
type UnitVersion struct {
	unitID           string
	generation       uint64
	parentGeneration uint64

	code         []byte
	functions    []FunctionBoundary
	instructions []InstructionBoundary
	stackMap     []StackMapEntry
	exceptions   []ExceptionEntry
	callTargets  []CallTarget
	patchRanges  []Range

	digests     UnitDigests
	contentRoot Digest
}

// CodeUnitVersion and CodeUnit are aliases for UnitVersion, matching the
// runtime terminology used by JIT hosts.
type CodeUnitVersion = UnitVersion

// CodeUnit is an alias for UnitVersion.
type CodeUnit = UnitVersion

// NewUnitVersion validates spec, copies it into immutable storage, computes
// every digest, recomputes them once more and returns the immutable version.
func NewUnitVersion(spec CodeUnitSpec, validators Validators) (*UnitVersion, error) {
	if strings.TrimSpace(spec.UnitID) == "" {
		return nil, fmt.Errorf("%w: empty unit ID", ErrInvalidUnit)
	}
	if len(spec.Code) == 0 {
		return nil, fmt.Errorf("%w: unit %q has no code", ErrInvalidUnit, spec.UnitID)
	}
	ranges := cloneRanges(spec.PatchRanges)
	if len(ranges) == 0 {
		ranges = []Range{{Start: 0, End: uint64(len(spec.Code))}}
	}
	for i, r := range ranges {
		if r.Start >= r.End {
			return nil, fmt.Errorf("%w: unit %q patch range %d is empty", ErrInvalidUnit, spec.UnitID, i)
		}
	}

	functions := cloneFunctions(spec.Functions)
	instructions := cloneInstructions(spec.Instructions)
	stackMap := cloneStackMap(spec.StackMap)
	exceptions := cloneExceptions(spec.ExceptionTable)
	callTargets := cloneCallTargets(spec.CallTargets)

	sort.SliceStable(functions, func(i, j int) bool {
		if functions[i].Start != functions[j].Start {
			return functions[i].Start < functions[j].Start
		}
		if functions[i].End != functions[j].End {
			return functions[i].End < functions[j].End
		}
		return functions[i].Name < functions[j].Name
	})
	sort.SliceStable(instructions, func(i, j int) bool {
		if instructions[i].PC != instructions[j].PC {
			return instructions[i].PC < instructions[j].PC
		}
		return instructions[i].Size < instructions[j].Size
	})
	sort.SliceStable(stackMap, func(i, j int) bool {
		if stackMap[i].PC != stackMap[j].PC {
			return stackMap[i].PC < stackMap[j].PC
		}
		return stackMap[i].FrameSize < stackMap[j].FrameSize
	})
	sort.SliceStable(exceptions, func(i, j int) bool {
		a, b := exceptions[i], exceptions[j]
		if a.StartPC != b.StartPC {
			return a.StartPC < b.StartPC
		}
		if a.EndPC != b.EndPC {
			return a.EndPC < b.EndPC
		}
		if a.HandlerPC != b.HandlerPC {
			return a.HandlerPC < b.HandlerPC
		}
		return a.Action < b.Action
	})
	sort.SliceStable(callTargets, func(i, j int) bool {
		a, b := callTargets[i], callTargets[j]
		if a.CallSite != b.CallSite {
			return a.CallSite < b.CallSite
		}
		if a.UnitID != b.UnitID {
			return a.UnitID < b.UnitID
		}
		if a.Generation != b.Generation {
			return a.Generation < b.Generation
		}
		return a.Offset < b.Offset
	})
	sort.SliceStable(ranges, func(i, j int) bool {
		if ranges[i].Start != ranges[j].Start {
			return ranges[i].Start < ranges[j].Start
		}
		return ranges[i].End < ranges[j].End
	})

	u := &UnitVersion{
		unitID:           spec.UnitID,
		generation:       spec.Generation,
		parentGeneration: spec.ParentGeneration,
		code:             append([]byte(nil), spec.Code...),
		functions:        functions,
		instructions:     instructions,
		stackMap:         stackMap,
		exceptions:       exceptions,
		callTargets:      callTargets,
		patchRanges:      ranges,
	}
	if err := u.runValidators(validators); err != nil {
		return nil, err
	}
	u.computeDigests()
	if err := u.checkDigests(); err != nil {
		return nil, err
	}
	return u, nil
}

// Verify runs every validator and recomputes all digests. It is used for
// manifest construction and crash recovery.
func (u *UnitVersion) Verify(validators Validators) error {
	if u == nil {
		return fmt.Errorf("%w: nil unit", ErrInvalidUnit)
	}
	if err := u.runValidators(validators); err != nil {
		return err
	}
	return u.checkDigests()
}

func (u *UnitVersion) runValidators(v Validators) error {
	if v.Code != nil {
		if err := v.Code(CodeContent{
			UnitID:           u.unitID,
			Generation:       u.generation,
			ParentGeneration: u.parentGeneration,
			Code:             append([]byte(nil), u.code...),
		}); err != nil {
			return &ValidationError{Component: "code", Err: err}
		}
	}
	if v.Boundaries != nil {
		if err := v.Boundaries(BoundaryContent{
			Functions:    cloneFunctions(u.functions),
			Instructions: cloneInstructions(u.instructions),
		}); err != nil {
			return &ValidationError{Component: "boundaries", Err: err}
		}
	}
	if v.StackMap != nil {
		if err := v.StackMap(StackMapContent{Entries: cloneStackMap(u.stackMap)}); err != nil {
			return &ValidationError{Component: "stackMap", Err: err}
		}
	}
	if v.ExceptionTable != nil {
		if err := v.ExceptionTable(ExceptionContent{Entries: cloneExceptions(u.exceptions)}); err != nil {
			return &ValidationError{Component: "exceptionTable", Err: err}
		}
	}
	if v.CallTargets != nil {
		if err := v.CallTargets(CallTargetContent{Entries: cloneCallTargets(u.callTargets)}); err != nil {
			return &ValidationError{Component: "callTargets", Err: err}
		}
	}
	return nil
}

func (u *UnitVersion) computeDigests() {
	u.digests, u.contentRoot = computeUnitDigests(
		u.unitID, u.generation, u.parentGeneration,
		u.code, u.functions, u.instructions, u.stackMap, u.exceptions, u.callTargets,
	)
}

func (u *UnitVersion) checkDigests() error {
	digests, root := computeUnitDigests(
		u.unitID, u.generation, u.parentGeneration,
		u.code, u.functions, u.instructions, u.stackMap, u.exceptions, u.callTargets,
	)
	if digests != u.digests {
		return fmt.Errorf("%w: unit %s generation %d", ErrDigestMismatch, u.unitID, u.generation)
	}
	if root != u.contentRoot {
		return fmt.Errorf("%w: unit %s content root", ErrDigestMismatch, u.unitID)
	}
	return nil
}

func computeUnitDigests(
	unitID string, generation, parentGeneration uint64,
	code []byte,
	functions []FunctionBoundary,
	instructions []InstructionBoundary,
	stackMap []StackMapEntry,
	exceptions []ExceptionEntry,
	callTargets []CallTarget,
) (UnitDigests, Digest) {
	var codeDigest = digestOf("netsec/code/v1", code)

	var boundary canonical
	boundary.u64(uint64(len(functions)))
	for _, f := range functions {
		boundary.str(f.Name)
		boundary.u64(f.Start)
		boundary.u64(f.End)
	}
	boundary.u64(uint64(len(instructions)))
	for _, ins := range instructions {
		boundary.u64(ins.PC)
		boundary.u32(ins.Size)
	}
	var boundaryDigest = digestOf("netsec/boundaries/v1", boundary.b)

	var stack canonical
	stack.u64(uint64(len(stackMap)))
	for _, e := range stackMap {
		stack.u64(e.PC)
		stack.u64(e.FrameSize)
		stack.bytes(e.PointerBitmap)
	}
	var stackDigest = digestOf("netsec/stackmap/v1", stack.b)

	var exception canonical
	exception.u64(uint64(len(exceptions)))
	for _, e := range exceptions {
		exception.u64(e.StartPC)
		exception.u64(e.EndPC)
		exception.u64(e.HandlerPC)
		exception.str(e.Action)
	}
	var exceptionDigest = digestOf("netsec/exceptions/v1", exception.b)

	var calls canonical
	calls.u64(uint64(len(callTargets)))
	for _, c := range callTargets {
		calls.u64(c.CallSite)
		calls.str(c.UnitID)
		calls.u64(c.Generation)
		calls.u64(c.Offset)
	}
	var callDigest = digestOf("netsec/calltargets/v1", calls.b)

	d := UnitDigests{
		Code:           codeDigest,
		Boundaries:     boundaryDigest,
		StackMap:       stackDigest,
		ExceptionTable: exceptionDigest,
		CallTargets:    callDigest,
	}
	root := digestOf("netsec/unit/v1",
		[]byte(unitID),
		u64bytes(generation),
		u64bytes(parentGeneration),
		codeDigest[:],
		boundaryDigest[:],
		stackDigest[:],
		exceptionDigest[:],
		callDigest[:],
	)
	return d, root
}

func u64bytes(v uint64) []byte {
	var c canonical
	c.u64(v)
	return c.b
}

// UnitID returns the stable identity of this code unit.
func (u *UnitVersion) UnitID() string { return u.unitID }

// Generation returns the generation number attached to this version. A
// generation number may be reused; ContentRoot and ManifestRoot remain the
// unambiguous identity.
func (u *UnitVersion) Generation() uint64 { return u.generation }

// ParentGeneration returns the generation from which this version was produced.
func (u *UnitVersion) ParentGeneration() uint64 { return u.parentGeneration }

// ContentRoot is the digest of all immutable content in this version.
func (u *UnitVersion) ContentRoot() Digest { return u.contentRoot }

// Digests returns the component digest set.
func (u *UnitVersion) Digests() UnitDigests { return u.digests }

// CodeDigest returns the digest of the native code blob.
func (u *UnitVersion) CodeDigest() Digest { return u.digests.Code }

// BoundaryDigest returns the digest of the function/instruction boundaries.
func (u *UnitVersion) BoundaryDigest() Digest { return u.digests.Boundaries }

// StackMapDigest returns the digest of the stack map.
func (u *UnitVersion) StackMapDigest() Digest { return u.digests.StackMap }

// ExceptionTableDigest returns the digest of the exception table.
func (u *UnitVersion) ExceptionTableDigest() Digest { return u.digests.ExceptionTable }

// CallTargetDigest returns the digest of the call-target table.
func (u *UnitVersion) CallTargetDigest() Digest { return u.digests.CallTargets }

// Code returns a copy of the native code blob.
func (u *UnitVersion) Code() []byte { return append([]byte(nil), u.code...) }

// Functions returns a copy of the function boundaries.
func (u *UnitVersion) Functions() []FunctionBoundary { return cloneFunctions(u.functions) }

// Instructions returns a copy of the instruction boundaries.
func (u *UnitVersion) Instructions() []InstructionBoundary {
	return cloneInstructions(u.instructions)
}

// StackMap returns a deep copy of the stack map.
func (u *UnitVersion) StackMap() []StackMapEntry { return cloneStackMap(u.stackMap) }

// ExceptionTable returns a copy of the exception table.
func (u *UnitVersion) ExceptionTable() []ExceptionEntry { return cloneExceptions(u.exceptions) }

// CallTargets returns a copy of the call target table.
func (u *UnitVersion) CallTargets() []CallTarget { return cloneCallTargets(u.callTargets) }

// PatchRanges returns a copy of the replaceable address ranges.
func (u *UnitVersion) PatchRanges() []Range { return cloneRanges(u.patchRanges) }

// InstructionAt returns the instruction containing pc.
func (u *UnitVersion) InstructionAt(pc uint64) (InstructionBoundary, bool) {
	for _, ins := range u.instructions {
		if ins.PC <= pc && pc-ins.PC < uint64(ins.Size) {
			return ins, true
		}
		if ins.PC > pc {
			break
		}
	}
	return InstructionBoundary{}, false
}

// HasInstructionAt reports whether pc is inside a known instruction. If the
// unit has no instruction boundaries, any pc inside the code blob is accepted.
func (u *UnitVersion) HasInstructionAt(pc uint64) bool {
	if len(u.instructions) == 0 {
		return pc < uint64(len(u.code))
	}
	_, ok := u.InstructionAt(pc)
	return ok
}

func (u *UnitVersion) stackMapAt(pc uint64) (StackMapEntry, bool) {
	i := sort.Search(len(u.stackMap), func(i int) bool { return u.stackMap[i].PC >= pc })
	if i < len(u.stackMap) && u.stackMap[i].PC == pc {
		return cloneStackMapEntry(u.stackMap[i]), true
	}
	return StackMapEntry{}, false
}

func (u *UnitVersion) exceptionAt(pc uint64) (ExceptionEntry, bool) {
	var best ExceptionEntry
	found := false
	for _, e := range u.exceptions {
		if e.StartPC > pc {
			break
		}
		if e.StartPC <= pc && pc < e.EndPC {
			if !found || e.EndPC-e.StartPC < best.EndPC-best.StartPC {
				best, found = e, true
			}
		}
	}
	return best, found
}

func (u *UnitVersion) callTargetAt(pc uint64) (CallTarget, bool) {
	i := sort.Search(len(u.callTargets), func(i int) bool { return u.callTargets[i].CallSite >= pc })
	if i < len(u.callTargets) && u.callTargets[i].CallSite == pc {
		return u.callTargets[i], true
	}
	return CallTarget{}, false
}

func cloneFunctions(in []FunctionBoundary) []FunctionBoundary {
	return append([]FunctionBoundary(nil), in...)
}

func cloneInstructions(in []InstructionBoundary) []InstructionBoundary {
	return append([]InstructionBoundary(nil), in...)
}

func cloneExceptions(in []ExceptionEntry) []ExceptionEntry {
	return append([]ExceptionEntry(nil), in...)
}

func cloneCallTargets(in []CallTarget) []CallTarget {
	return append([]CallTarget(nil), in...)
}

func cloneRanges(in []Range) []Range {
	return append([]Range(nil), in...)
}

func cloneStackMap(in []StackMapEntry) []StackMapEntry {
	out := make([]StackMapEntry, len(in))
	for i, e := range in {
		out[i] = cloneStackMapEntry(e)
	}
	return out
}

func cloneStackMapEntry(e StackMapEntry) StackMapEntry {
	return StackMapEntry{
		PC:            e.PC,
		FrameSize:     e.FrameSize,
		PointerBitmap: append([]byte(nil), e.PointerBitmap...),
	}
}
