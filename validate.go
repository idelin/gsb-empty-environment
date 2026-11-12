package netsec

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
)

// WordSize is the fixed width, in bytes, of every descriptor and table entry.
const WordSize = 16

// PageSize is the device page granularity used for freeze tracking.
const PageSize uint64 = 4096

// maxPages bounds page-span arithmetic so a corrupt count cannot trigger
// unbounded allocation during validation.
const maxPages = 1 << 20

// defaultMaxDepth bounds indirect-table recursion when the pattern set does
// not specify its own limit.
const defaultMaxDepth = 16

var (
	// ErrUnknownOpcode is returned when a word's opcode matches no pattern.
	ErrUnknownOpcode = errors.New("netsec: unknown opcode")
	// ErrInvalidPattern is returned when a PatternSet is malformed.
	ErrInvalidPattern = errors.New("netsec: invalid instruction pattern")
	// ErrInvalidWord is returned when a submitted word has the wrong width.
	ErrInvalidWord = errors.New("netsec: invalid descriptor word")
	// ErrReadDenied is returned when a read target falls outside ReadRegions.
	ErrReadDenied = errors.New("netsec: read target outside authorized regions")
	// ErrWriteDenied is returned when a write target falls outside WriteRegions.
	ErrWriteDenied = errors.New("netsec: write target outside authorized regions")
	// ErrOverflow is returned when address or count arithmetic overflows.
	ErrOverflow = errors.New("netsec: address/count arithmetic overflow")
	// ErrCycle is returned when an indirect table reaches itself.
	ErrCycle = errors.New("netsec: indirect table cycle")
	// ErrDepth is returned when indirect recursion exceeds MaxDepth.
	ErrDepth = errors.New("netsec: indirect recursion depth exceeded")
	// ErrPlanFinalized is returned when a committed or released plan is reused.
	ErrPlanFinalized = errors.New("netsec: plan already committed or released")
	// ErrCommitAborted wraps the cause of an all-or-nothing commit abort.
	ErrCommitAborted = errors.New("netsec: commit aborted, no partial execution")
)

// FieldKind classifies a field inside a descriptor word.
type FieldKind int

const (
	// FieldOpcode selects the pattern that interprets the word.
	FieldOpcode FieldKind = iota
	// FieldCount gives the number of entries in an indirect table.
	FieldCount
	// FieldAddrRead is an address the agent reads from.
	FieldAddrRead
	// FieldAddrWrite is an address the agent writes to.
	FieldAddrWrite
	// FieldData carries the value written through a FieldAddrWrite field.
	FieldData
)

// Field describes one fixed-width field inside a WordSize byte word.
// All integer fields are little-endian.
type Field struct {
	Kind   FieldKind
	Offset int
	Width  int
}

// Pattern declares how one opcode is laid out and interpreted.
//
// Indirect patterns must contain exactly one FieldCount and one
// FieldAddrRead field; the read address is the table base and every table
// entry is itself a WordSize byte word parsed recursively. A pattern may
// contain at most one FieldAddrWrite and at most one FieldData.
type Pattern struct {
	Opcode   uint64
	Indirect bool
	Fields   []Field
}

// PatternSet is the finite instruction vocabulary granted to a plugin.
type PatternSet struct {
	Patterns map[uint64]Pattern
	// MaxDepth bounds indirect-table recursion; zero selects defaultMaxDepth.
	MaxDepth int
}

// Region is a half-open byte range [Base, Base+Size) in device address space.
type Region struct {
	Base uint64
	Size uint64
}

// Contains reports whether the access [addr, addr+size) lies inside r.
func (r Region) Contains(addr, size uint64) bool {
	if size == 0 {
		return false
	}
	end, ok := checkedAdd(addr, size)
	if !ok {
		return false
	}
	regionEnd, ok := checkedAdd(r.Base, r.Size)
	if !ok {
		return false
	}
	return addr >= r.Base && end <= regionEnd
}

// Authz declares the regions reachable by reads and writes.
type Authz struct {
	ReadRegions  []Region
	WriteRegions []Region
}

func (a Authz) allowRead(addr, size uint64) bool {
	for _, r := range a.ReadRegions {
		if r.Contains(addr, size) {
			return true
		}
	}
	return false
}

func (a Authz) allowWrite(addr, size uint64) bool {
	for _, r := range a.WriteRegions {
		if r.Contains(addr, size) {
			return true
		}
	}
	return false
}

// DeviceMemory is the driver-facing primitive set. Freeze makes a page
// immutable to writers other than the commit channel: Write is the commit
// channel and must only target currently frozen pages. UnfreezeAll ends the
// transaction and releases every frozen page.
type DeviceMemory interface {
	Read(addr uint64, buf []byte) error
	Write(addr uint64, data []byte) error
	Freeze(pageAddr uint64) error
	UnfreezeAll() error
}

// ValidationError locates a rejected descriptor in the top-level batch and
// in the chain of indirect tables traversed to reach it.
type ValidationError struct {
	// Index is the top-level word index.
	Index int
	// Path lists the base addresses of the indirect tables traversed.
	Path []uint64
	Op   uint64
	Err  error
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("netsec: descriptor %d (op=%#x path=%#x): %v", e.Index, e.Op, e.Path, e.Err)
}

func (e *ValidationError) Unwrap() error { return e.Err }

type plannedWrite struct {
	addr     uint64
	data     []byte
	original []byte
}

// Plan is a fully validated, snapshotted batch. It holds no live references
// to device table memory: every table entry has been parsed and every write
// target has been shadowed, so later self-modification cannot change what a
// commit does.
type Plan struct {
	dev    DeviceMemory
	pages  []uint64
	writes []plannedWrite
	done   bool
}

// Pages returns the sorted, deduplicated device pages that Commit freezes.
func (p *Plan) Pages() []uint64 {
	out := make([]uint64, len(p.pages))
	copy(out, p.pages)
	return out
}

// WriteCount returns the number of validated writes the plan will perform.
func (p *Plan) WriteCount() int { return len(p.writes) }

// Validator resolves descriptor batches against a fixed pattern vocabulary
// and a fixed authorization policy.
type Validator struct {
	patterns map[uint64]Pattern
	maxDepth int
	authz    Authz
	dev      DeviceMemory
}

// NewValidator compiles a PatternSet, rejecting malformed patterns.
func NewValidator(set PatternSet, authz Authz, dev DeviceMemory) (*Validator, error) {
	if dev == nil {
		return nil, fmt.Errorf("%w: nil device memory", ErrInvalidPattern)
	}
	depth := set.MaxDepth
	if depth <= 0 {
		depth = defaultMaxDepth
	}
	compiled := make(map[uint64]Pattern, len(set.Patterns))
	for opcode, pat := range set.Patterns {
		if pat.Opcode != opcode {
			return nil, fmt.Errorf("%w: opcode key %#x != pattern opcode %#x", ErrInvalidPattern, opcode, pat.Opcode)
		}
		if err := validatePattern(pat); err != nil {
			return nil, err
		}
		compiled[opcode] = pat
	}
	return &Validator{patterns: compiled, maxDepth: depth, authz: authz, dev: dev}, nil
}

func validatePattern(pat Pattern) error {
	if len(pat.Fields) == 0 {
		return fmt.Errorf("%w: opcode %#x has no fields", ErrInvalidPattern, pat.Opcode)
	}
	opcodeFields, countFields, readFields, writeFields, dataFields := 0, 0, 0, 0, 0
	fields := make([]Field, len(pat.Fields))
	copy(fields, pat.Fields)
	sort.Slice(fields, func(i, j int) bool { return fields[i].Offset < fields[j].Offset })
	for i, f := range fields {
		if f.Width != 1 && f.Width != 2 && f.Width != 4 && f.Width != 8 {
			return fmt.Errorf("%w: opcode %#x field %d has width %d", ErrInvalidPattern, pat.Opcode, f.Kind, f.Width)
		}
		if f.Offset < 0 || f.Offset+f.Width > WordSize {
			return fmt.Errorf("%w: opcode %#x field %d outside word", ErrInvalidPattern, pat.Opcode, f.Kind)
		}
		if i > 0 {
			prev := fields[i-1]
			if prev.Offset+prev.Width > f.Offset {
				return fmt.Errorf("%w: opcode %#x overlapping fields", ErrInvalidPattern, pat.Opcode)
			}
		}
		switch f.Kind {
		case FieldOpcode:
			opcodeFields++
		case FieldCount:
			countFields++
		case FieldAddrRead:
			readFields++
		case FieldAddrWrite:
			writeFields++
		case FieldData:
			dataFields++
		default:
			return fmt.Errorf("%w: opcode %#x unknown field kind %d", ErrInvalidPattern, pat.Opcode, f.Kind)
		}
	}
	if opcodeFields != 1 {
		return fmt.Errorf("%w: opcode %#x needs exactly one opcode field", ErrInvalidPattern, pat.Opcode)
	}
	if writeFields > 1 || dataFields > 1 {
		return fmt.Errorf("%w: opcode %#x has multiple write/data fields", ErrInvalidPattern, pat.Opcode)
	}
	if dataFields == 1 && writeFields == 0 {
		return fmt.Errorf("%w: opcode %#x data field without write address", ErrInvalidPattern, pat.Opcode)
	}
	if pat.Indirect {
		if countFields != 1 || readFields != 1 {
			return fmt.Errorf("%w: indirect opcode %#x needs one count and one table address field", ErrInvalidPattern, pat.Opcode)
		}
	} else if countFields != 0 {
		return fmt.Errorf("%w: direct opcode %#x must not declare a count field", ErrInvalidPattern, pat.Opcode)
	}
	return nil
}

// Prepare validates a batch of top-level descriptor words, recursively
// resolving every reachable indirect table. It succeeds only when every
// reachable read and write is authorized; on success it returns a snapshot
// plan whose pages can be frozen and whose writes can be committed.
func (v *Validator) Prepare(words [][]byte) (*Plan, error) {
	plan := &Plan{dev: v.dev}
	pageSet := make(map[uint64]struct{})
	for i, word := range words {
		if len(word) != WordSize {
			return nil, &ValidationError{Index: i, Err: fmt.Errorf("%w: got %d bytes", ErrInvalidWord, len(word))}
		}
		if err := v.validateWord(word, nil, 1, plan, pageSet, map[uint64]bool{}); err != nil {
			return nil, &ValidationError{Index: i, Path: nestedPath(err), Op: nestedOp(err), Err: err}
		}
	}
	pages := make([]uint64, 0, len(pageSet))
	for pg := range pageSet {
		pages = append(pages, pg)
	}
	sort.Slice(pages, func(i, j int) bool { return pages[i] < pages[j] })
	plan.pages = pages
	return plan, nil
}

func (v *Validator) validateWord(word []byte, path []uint64, depth int, plan *Plan, pageSet map[uint64]struct{}, onPath map[uint64]bool) error {
	pat, err := v.matchPattern(word)
	if err != nil {
		return &ValidationError{Index: -1, Path: clonePath(path), Err: err}
	}
	var writeField *Field
	var dataValue uint64
	hasData := false
	for i := range pat.Fields {
		f := &pat.Fields[i]
		value := decodeUint(word, f.Offset, f.Width)
		switch f.Kind {
		case FieldOpcode, FieldCount:
			continue
		case FieldAddrRead:
			if !pat.Indirect {
				if !v.authz.allowRead(value, uint64(f.Width)) {
					return v.fail(path, pat, fmt.Errorf("%w: %#x width %d", ErrReadDenied, value, f.Width))
				}
				if err := addPages(pageSet, value, uint64(f.Width)); err != nil {
					return v.fail(path, pat, err)
				}
			}
		case FieldAddrWrite:
			writeField = f
		case FieldData:
			dataValue, hasData = value, true
		}
	}
	if writeField != nil {
		addr := decodeUint(word, writeField.Offset, writeField.Width)
		width := uint64(writeField.Width)
		if !v.authz.allowWrite(addr, width) {
			return v.fail(path, pat, fmt.Errorf("%w: %#x width %d", ErrWriteDenied, addr, width))
		}
		if err := addPages(pageSet, addr, width); err != nil {
			return v.fail(path, pat, err)
		}
		payload := make([]byte, width)
		if hasData {
			if !fitsInWidth(dataValue, int(width)) {
				return v.fail(path, pat, fmt.Errorf("%w: data value %#x", ErrOverflow, dataValue))
			}
			encodeUint(payload, dataValue)
		}
		original := make([]byte, width)
		if err := v.dev.Read(addr, original); err != nil {
			return v.fail(path, pat, fmt.Errorf("shadow read at %#x: %w", addr, err))
		}
		plan.writes = append(plan.writes, plannedWrite{addr: addr, data: payload, original: original})
	}
	if !pat.Indirect {
		return nil
	}
	if depth > v.maxDepth {
		return v.fail(path, pat, fmt.Errorf("%w: depth %d", ErrDepth, depth))
	}
	tableAddr, count, err := v.tableReference(pat, word)
	if err != nil {
		return v.fail(path, pat, err)
	}
	if onPath[tableAddr] {
		return v.fail(path, pat, fmt.Errorf("%w: table %#x reached from %#x", ErrCycle, tableAddr, path))
	}
	tableSize, ok := checkedMul(uint64(count), WordSize)
	if !ok {
		return v.fail(path, pat, fmt.Errorf("%w: table at %#x count %d", ErrOverflow, tableAddr, count))
	}
	if tableSize == 0 {
		return nil
	}
	if !v.authz.allowRead(tableAddr, tableSize) {
		return v.fail(path, pat, fmt.Errorf("%w: table at %#x", ErrReadDenied, tableAddr))
	}
	if err := addPages(pageSet, tableAddr, tableSize); err != nil {
		return v.fail(path, pat, err)
	}
	onPath[tableAddr] = true
	defer delete(onPath, tableAddr)
	entry := make([]byte, WordSize)
	childPath := appendPath(path, tableAddr)
	for index := uint64(0); index < count; index++ {
		entryAddr, ok := checkedAdd(tableAddr, index*WordSize)
		if !ok {
			return v.fail(childPath, pat, fmt.Errorf("%w: table entry %d at %#x", ErrOverflow, index, tableAddr))
		}
		if err := v.dev.Read(entryAddr, entry); err != nil {
			return v.fail(childPath, pat, fmt.Errorf("entry %d: %w", index, err))
		}
		if err := v.validateWord(entry, childPath, depth+1, plan, pageSet, onPath); err != nil {
			return err
		}
	}
	return nil
}

func (v *Validator) fail(path []uint64, pat Pattern, err error) error {
	return &ValidationError{Index: -1, Path: clonePath(path), Op: pat.Opcode, Err: err}
}

func (v *Validator) matchPattern(word []byte) (Pattern, error) {
	for _, pat := range v.patterns {
		f := pat.opcodeField()
		if decodeUint(word, f.Offset, f.Width) == pat.Opcode {
			return pat, nil
		}
	}
	for _, pat := range v.patterns {
		f := pat.opcodeField()
		return Pattern{}, fmt.Errorf("%w: %#x", ErrUnknownOpcode, decodeUint(word, f.Offset, f.Width))
	}
	return Pattern{}, ErrUnknownOpcode
}

func (p Pattern) opcodeField() Field {
	for _, f := range p.Fields {
		if f.Kind == FieldOpcode {
			return f
		}
	}
	return p.Fields[0]
}

func (v *Validator) tableReference(pat Pattern, word []byte) (uint64, uint64, error) {
	var tableAddr, count uint64
	for _, f := range pat.Fields {
		value := decodeUint(word, f.Offset, f.Width)
		switch f.Kind {
		case FieldAddrRead:
			tableAddr = value
		case FieldCount:
			count = value
		}
	}
	return tableAddr, count, nil
}

// Commit freezes every referenced page first and only then applies the
// validated writes. If any freeze or write fails, applied writes are rolled
// back from the Prepare-time shadow, all pages are released, and the wrapped
// error is returned: a failed commit has zero partial execution.
func (p *Plan) Commit() (err error) {
	if p.done {
		return ErrPlanFinalized
	}
	p.done = true
	defer func() {
		if unfreezeErr := p.dev.UnfreezeAll(); unfreezeErr != nil && err == nil {
			err = unfreezeErr
		}
	}()
	for _, pg := range p.pages {
		if freezeErr := p.dev.Freeze(pg); freezeErr != nil {
			return fmt.Errorf("%w: freeze page %#x: %v", ErrCommitAborted, pg, freezeErr)
		}
	}
	applied := 0
	for _, w := range p.writes {
		if writeErr := p.dev.Write(w.addr, w.data); writeErr != nil {
			for rollback := applied - 1; rollback >= 0; rollback-- {
				prev := p.writes[rollback]
				if rbErr := p.dev.Write(prev.addr, prev.original); rbErr != nil {
					return fmt.Errorf("%w: write %#x: %v (rollback also failed: %v)", ErrCommitAborted, w.addr, writeErr, rbErr)
				}
			}
			return fmt.Errorf("%w: write %#x: %v", ErrCommitAborted, w.addr, writeErr)
		}
		applied++
	}
	return nil
}

// Release abandons a prepared plan without touching device memory and marks
// it unusable for further commits.
func (p *Plan) Release() error {
	if p.done {
		return ErrPlanFinalized
	}
	p.done = true
	return nil
}

func addPages(pageSet map[uint64]struct{}, addr, size uint64) error {
	if size == 0 {
		return fmt.Errorf("%w: zero-width access at %#x", ErrOverflow, addr)
	}
	end, ok := checkedAdd(addr, size)
	if !ok {
		return fmt.Errorf("%w: access at %#x size %#x", ErrOverflow, addr, size)
	}
	first := addr / PageSize
	last := (end - 1) / PageSize
	count, ok := checkedAdd(last-first, 1)
	if !ok || count > maxPages {
		return fmt.Errorf("%w: span at %#x size %#x", ErrOverflow, addr, size)
	}
	for pg := first; pg <= last; pg++ {
		pageSet[pg*PageSize] = struct{}{}
		if pg == last {
			break
		}
	}
	return nil
}

func checkedAdd(a, b uint64) (uint64, bool) {
	if b > ^uint64(0)-a {
		return 0, false
	}
	return a + b, true
}

func checkedMul(a, b uint64) (uint64, bool) {
	if a == 0 || b == 0 {
		return 0, true
	}
	if a > ^uint64(0)/b {
		return 0, false
	}
	return a * b, true
}

func fitsInWidth(value uint64, width int) bool {
	if width >= 8 {
		return true
	}
	return value < (uint64(1) << (uint(width) * 8))
}

func decodeUint(word []byte, offset, width int) uint64 {
	switch width {
	case 1:
		return uint64(word[offset])
	case 2:
		return uint64(binary.LittleEndian.Uint16(word[offset:]))
	case 4:
		return uint64(binary.LittleEndian.Uint32(word[offset:]))
	default:
		return binary.LittleEndian.Uint64(word[offset:])
	}
}

func encodeUint(buf []byte, value uint64) {
	for i := range buf {
		buf[i] = byte(value >> (uint(i) * 8))
	}
}

func appendPath(path []uint64, tableAddr uint64) []uint64 {
	out := make([]uint64, 0, len(path)+1)
	out = append(out, path...)
	return append(out, tableAddr)
}

func clonePath(path []uint64) []uint64 {
	if len(path) == 0 {
		return nil
	}
	out := make([]uint64, len(path))
	copy(out, path)
	return out
}

func nestedPath(err error) []uint64 {
	var ve *ValidationError
	if errors.As(err, &ve) {
		return clonePath(ve.Path)
	}
	return nil
}

func nestedOp(err error) uint64 {
	var ve *ValidationError
	if errors.As(err, &ve) {
		return ve.Op
	}
	return 0
}
