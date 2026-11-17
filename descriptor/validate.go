package descriptor

import (
	"fmt"
	"math"
)

// Limits bounds the work an untrusted descriptor graph can cause.
type Limits struct {
	MaxDepth      int
	MaxEntries    uint64
	MaxPages      uint64
	MaxTableBytes uint64
}

// DefaultLimits are applied to every zero field in Validator.Limits.
var DefaultLimits = Limits{
	MaxDepth:      32,
	MaxEntries:    1 << 16,
	MaxPages:      1 << 16,
	MaxTableBytes: 64 << 20,
}

func (l Limits) normalize() Limits {
	if l.MaxDepth <= 0 {
		l.MaxDepth = DefaultLimits.MaxDepth
	}
	if l.MaxEntries == 0 {
		l.MaxEntries = DefaultLimits.MaxEntries
	}
	if l.MaxPages == 0 {
		l.MaxPages = DefaultLimits.MaxPages
	}
	if l.MaxTableBytes == 0 {
		l.MaxTableBytes = DefaultLimits.MaxTableBytes
	}
	return l
}

// Validator validates descriptors against a page store and authorization
// policy. CheckReads additionally requires read permission for direct reads
// and for the memory occupied by indirect tables. Writes are always checked.
type Validator struct {
	Store      PageStore
	Authorizer Authorizer
	Translator Translator
	CheckReads bool
	Limits     Limits
}

// New returns a Validator with default limits.
func New(store PageStore, authorizer Authorizer) *Validator {
	return &Validator{Store: store, Authorizer: authorizer}
}

// Validate validates req using store and authorizer with default limits.
func Validate(store PageStore, authorizer Authorizer, req Request) (*Plan, error) {
	return New(store, authorizer).Validate(req)
}

// ValidateDescriptor is an alias for Validate.
func ValidateDescriptor(store PageStore, authorizer Authorizer, req Request) (*Plan, error) {
	return Validate(store, authorizer, req)
}

// Operation is one validated read or write.
type Operation struct {
	Access  Access
	Address Address
	Size    uint64
	// Entry is the address of the instruction that produced this operation.
	Entry Address
	// Field is the schema name of the address field.
	Field string
}

// Plan is an immutable, fully validated operation list.
type Plan struct {
	ops    []Operation
	refs   []PageID
	frozen bool
}

// Operations returns a copy of all validated operations.
func (p *Plan) Operations() []Operation {
	if p == nil {
		return nil
	}
	return append([]Operation(nil), p.ops...)
}

// Writes returns a copy of the validated write operations.
func (p *Plan) Writes() []Operation {
	if p == nil {
		return nil
	}
	var out []Operation
	for _, op := range p.ops {
		if op.Access == AccessWrite {
			out = append(out, op)
		}
	}
	return out
}

// Reads returns a copy of the validated read operations.
func (p *Plan) Reads() []Operation {
	if p == nil {
		return nil
	}
	var out []Operation
	for _, op := range p.ops {
		if op.Access == AccessRead {
			out = append(out, op)
		}
	}
	return out
}

// References returns every page read or written by the plan, sorted by ID.
func (p *Plan) References() []PageID {
	if p == nil {
		return nil
	}
	return append([]PageID(nil), p.refs...)
}

// Freeze freezes every referenced page. Calling Freeze more than once is a
// no-op after the first successful call.
func (p *Plan) Freeze(freezer PageFreezer) error {
	if p == nil {
		return fmt.Errorf("%w: nil plan", ErrNil)
	}
	if p.frozen {
		return nil
	}
	if len(p.refs) == 0 {
		p.frozen = true
		return nil
	}
	if freezer == nil {
		return fmt.Errorf("%w: plan references %d pages but no freezer was provided",
			ErrNil, len(p.refs))
	}
	for _, id := range p.refs {
		if err := freezer.FreezePage(id); err != nil {
			return &Error{
				Kind:   ErrFreeze,
				Detail: fmt.Sprintf("page %d", id),
				Cause:  err,
			}
		}
	}
	p.frozen = true
	return nil
}

// Submitter accepts the complete operation list. Implementations must treat
// the call as one transaction: either all operations execute or none do.
type Submitter interface {
	Submit(ops []Operation) error
}

// Submit freezes all referenced pages first, then submits the complete
// operation list in a single call. If validation failed there is no Plan, so
// Submit cannot be reached for a partially valid descriptor.
func (p *Plan) Submit(freezer PageFreezer, submitter Submitter) error {
	if p == nil {
		return fmt.Errorf("%w: nil plan", ErrNil)
	}
	if submitter == nil {
		return fmt.Errorf("%w: nil submitter", ErrNil)
	}
	if err := p.Freeze(freezer); err != nil {
		return err
	}
	ops := p.Operations()
	if err := submitter.Submit(ops); err != nil {
		return fmt.Errorf("descriptor: submit: %w", err)
	}
	return nil
}

type nodeKey struct {
	addr  Address
	table *Table
}

type memoEntry struct {
	ops     []Operation
	entries uint64
}

type traversal struct {
	validator *Validator
	limits    Limits
	snap      *snapshot
	ops       []Operation
	entries   uint64
	active    map[nodeKey]bool
	memo      map[nodeKey]memoEntry
}

// Validate recursively validates req and returns a complete plan. No device
// operation is performed during validation.
func (v *Validator) Validate(req Request) (*Plan, error) {
	if v == nil {
		return nil, fmt.Errorf("%w: nil validator", ErrNil)
	}
	if v.Store == nil {
		return nil, fmt.Errorf("%w: nil page store", ErrNil)
	}
	if v.Authorizer == nil {
		return nil, fmt.Errorf("%w: nil authorizer", ErrNil)
	}
	if err := validateFormat(req.Format); err != nil {
		return nil, err
	}
	pageSize := v.Store.PageSize()
	if pageSize == 0 {
		return nil, fmt.Errorf("%w: page size is zero", ErrPage)
	}
	limits := v.Limits.normalize()
	if uint64(req.Length) > limits.MaxTableBytes {
		return nil, fmt.Errorf("%w: top-level descriptor is %d bytes, limit is %d",
			ErrLimit, req.Length, limits.MaxTableBytes)
	}
	formatted := req.Format
	if req.Length%formatted.EntrySize != 0 {
		return nil, &Error{
			Kind:    ErrTail,
			Address: req.Address,
			Detail: fmt.Sprintf("%d bytes is not a multiple of %d-byte entries",
				req.Length, formatted.EntrySize),
		}
	}

	translator := v.Translator
	if translator == nil {
		translator = contiguousTranslator{pageSize: pageSize}
	}
	snap := newSnapshot(v.Store, translator, pageSize, limits)
	t := &traversal{
		validator: v,
		limits:    limits,
		snap:      snap,
		active:    make(map[nodeKey]bool),
		memo:      make(map[nodeKey]memoEntry),
	}

	if err := t.parseRoot(req); err != nil {
		return nil, err
	}
	return &Plan{
		ops:  append([]Operation(nil), t.ops...),
		refs: snap.refList(),
	}, nil
}

func (t *traversal) parseRoot(req Request) error {
	entrySize := req.Format.EntrySize
	maxInt := uint64(int(^uint(0) >> 1))
	if entrySize > maxInt {
		return fmt.Errorf("%w: entry size %d is too large", ErrSchema, entrySize)
	}
	entryCount := req.Length / entrySize
	if entryCount > t.limits.MaxEntries-t.entries {
		return &Error{
			Kind:    ErrLimit,
			Address: req.Address,
			Detail:  fmt.Sprintf("%d entries exceeds limit %d", entryCount, t.limits.MaxEntries),
		}
	}
	data, err := t.snap.readAt(req.Address, req.Length)
	if err != nil {
		return &Error{Kind: ErrPage, Address: req.Address, Cause: err}
	}
	for off := uint64(0); off < req.Length; off += entrySize {
		entryAddr := req.Address + Address(off)
		if err := t.parseEntry(data[off:off+entrySize], entryAddr, req.Format, 0); err != nil {
			return err
		}
	}
	return nil
}

func (t *traversal) parseTable(addr Address, table *Table, depth int) error {
	if depth > t.limits.MaxDepth {
		return &Error{
			Kind:    ErrLimit,
			Address: addr,
			Detail:  fmt.Sprintf("indirect depth exceeds %d", t.limits.MaxDepth),
		}
	}
	key := nodeKey{addr: addr, table: table}
	if t.active[key] {
		return &Error{Kind: ErrCycle, Address: addr}
	}
	if cached, ok := t.memo[key]; ok {
		if err := t.addEntries(cached.entries); err != nil {
			return err
		}
		t.ops = append(t.ops, cached.ops...)
		return nil
	}

	t.active[key] = true
	defer delete(t.active, key)
	opsStart := len(t.ops)
	entriesStart := t.entries

	header := table.HeaderSize
	if header > t.limits.MaxTableBytes {
		return &Error{
			Kind:    ErrLimit,
			Address: addr,
			Detail:  fmt.Sprintf("table header is %d bytes", header),
		}
	}

	var count uint64
	switch table.Count.Mode {
	case CountFixed:
		count = table.Count.Value
	case CountField, CountBytesField:
		field := table.Count.Field
		fieldEnd := field.ByteOffset + field.ByteSize
		if fieldEnd > header {
			return fmt.Errorf("%w: count field lies outside table header", ErrSchema)
		}
		if t.validator.CheckReads {
			if err := t.authorizeRead(addr, fieldEnd, addr); err != nil {
				return err
			}
		}
		headerData, err := t.snap.readAt(addr, fieldEnd)
		if err != nil {
			return &Error{Kind: ErrPage, Address: addr, Cause: err}
		}
		raw, err := field.read(headerData)
		if err != nil {
			return &Error{Kind: ErrBadDescriptor, Address: addr, Cause: err}
		}
		if table.Count.Mode == CountBytesField {
			if raw%table.Format.EntrySize != 0 {
				return &Error{
					Kind:    ErrTail,
					Address: addr + Address(header),
					Detail: fmt.Sprintf("entry area of %d bytes is not a multiple of %d",
						raw, table.Format.EntrySize),
				}
			}
			count = raw / table.Format.EntrySize
		} else {
			count = raw
		}
	}

	if table.MaxEntries != 0 && count > table.MaxEntries {
		return &Error{
			Kind:    ErrLimit,
			Address: addr,
			Detail:  fmt.Sprintf("%d entries exceeds table limit %d", count, table.MaxEntries),
		}
	}
	if count > t.limits.MaxEntries-t.entries {
		return &Error{
			Kind:    ErrLimit,
			Address: addr,
			Detail:  fmt.Sprintf("%d entries exceeds remaining limit %d", count, t.limits.MaxEntries-t.entries),
		}
	}
	entryBytes := table.Format.EntrySize
	if count > 0 && entryBytes > (math.MaxUint64-header)/count {
		return &Error{Kind: ErrOverflow, Address: addr}
	}
	entriesSize := count * entryBytes
	total := header + entriesSize
	if total > t.limits.MaxTableBytes {
		return &Error{
			Kind:    ErrLimit,
			Address: addr,
			Detail:  fmt.Sprintf("table is %d bytes, limit is %d", total, t.limits.MaxTableBytes),
		}
	}
	if t.validator.CheckReads {
		if err := t.authorizeRead(addr, total, addr); err != nil {
			return err
		}
	}

	data, err := t.snap.readAt(addr, total)
	if err != nil {
		return &Error{Kind: ErrPage, Address: addr, Cause: err}
	}
	for i := uint64(0); i < count; i++ {
		entryAddr := addr + Address(header+i*entryBytes)
		if err := t.parseEntry(data[header+i*entryBytes:header+(i+1)*entryBytes],
			entryAddr, table.Format, depth); err != nil {
			return err
		}
	}

	t.memo[key] = memoEntry{
		ops:     append([]Operation(nil), t.ops[opsStart:]...),
		entries: t.entries - entriesStart,
	}
	return nil
}

func (t *traversal) parseEntry(entry []byte, entryAddr Address, format *Format, depth int) error {
	if err := t.addEntries(1); err != nil {
		return err
	}
	opcode, err := extractBits(entry, format.Opcode.BitOffset, format.Opcode.BitWidth)
	if err != nil {
		return &Error{Kind: ErrBadDescriptor, Address: entryAddr, Cause: err}
	}
	for _, pattern := range format.Patterns {
		mask := patternMask(pattern, format.Opcode.BitWidth)
		if opcode&mask != pattern.Value {
			continue
		}
		for i := range pattern.Fields {
			field := &pattern.Fields[i]
			if field.Kind == FieldIgnored {
				continue
			}
			raw, err := extractBits(entry, field.BitOffset, field.BitWidth)
			if err != nil {
				return &Error{
					Kind:    ErrBadDescriptor,
					Address: entryAddr,
					Entry:   entryAddr,
					Field:   field.Name,
					Cause:   err,
				}
			}
			target, err := field.Semantics.resolve(raw)
			if err != nil {
				return &Error{
					Kind:    ErrBadDescriptor,
					Address: entryAddr,
					Entry:   entryAddr,
					Field:   field.Name,
					Detail:  fmt.Sprintf("raw address %#x", raw),
					Cause:   err,
				}
			}
			switch field.Kind {
			case FieldRead:
				size := field.accessSize()
				if size == 0 {
					return fmt.Errorf("%w: field %q has zero access size", ErrSchema, field.Name)
				}
				if t.validator.CheckReads {
					if err := t.authorizeRead(target, size, entryAddr); err != nil {
						return err
					}
				}
				if err := t.snap.addReference(target, size); err != nil {
					return &Error{Kind: ErrPage, Address: target, Entry: entryAddr, Field: field.Name, Cause: err}
				}
				t.ops = append(t.ops, Operation{
					Access:  AccessRead,
					Address: target,
					Size:    size,
					Entry:   entryAddr,
					Field:   field.Name,
				})
			case FieldWrite:
				size := field.accessSize()
				if size == 0 {
					return fmt.Errorf("%w: field %q has zero access size", ErrSchema, field.Name)
				}
				if err := t.validator.Authorizer.Authorize(target, size, AccessWrite); err != nil {
					return &Error{
						Kind:    ErrUnauthorized,
						Address: target,
						Entry:   entryAddr,
						Field:   field.Name,
						Cause:   err,
					}
				}
				if err := t.snap.addReference(target, size); err != nil {
					return &Error{Kind: ErrPage, Address: target, Entry: entryAddr, Field: field.Name, Cause: err}
				}
				t.ops = append(t.ops, Operation{
					Access:  AccessWrite,
					Address: target,
					Size:    size,
					Entry:   entryAddr,
					Field:   field.Name,
				})
			case FieldIndirect:
				if err := t.parseTable(target, field.Table, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return &Error{
		Kind:    ErrUnknownOpcode,
		Address: entryAddr,
		Entry:   entryAddr,
		Detail:  fmt.Sprintf("opcode %#x", opcode),
	}
}

func (t *traversal) authorizeRead(addr Address, size uint64, entry Address) error {
	if err := t.validator.Authorizer.Authorize(addr, size, AccessRead); err != nil {
		return &Error{
			Kind:    ErrUnauthorized,
			Address: addr,
			Entry:   entry,
			Cause:   err,
		}
	}
	return nil
}

func (t *traversal) addEntries(count uint64) error {
	if count > t.limits.MaxEntries-t.entries {
		return &Error{
			Kind:   ErrLimit,
			Detail: fmt.Sprintf("more than %d descriptor entries", t.limits.MaxEntries),
		}
	}
	t.entries += count
	return nil
}
