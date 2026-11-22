// Package shadow implements an AddressSanitizer-style shadow memory used by
// instrumentation callbacks to detect heap memory errors at runtime.
//
// Every eight application bytes are described by one shadow byte:
//
//   - 0: all eight bytes are accessible;
//   - 1..7: only the first k bytes are accessible (partial tail cell);
//   - negative values mark poisoned memory: left red zone, right red zone or
//     a freed (quarantined) block.
//
// A Detector owns a contiguous arena. Every allocation is surrounded by red
// zones, freed blocks enter a FIFO quarantine and are only recycled once the
// total quarantined byte count exceeds the configured limit.
//
// Instrumented code calls Load/Store (or Check) for accesses of 1, 2, 4, 8 or
// 16 bytes; the returned *AccessError identifies the error kind even when the
// access starts at an arbitrary offset or spans multiple shadow cells.
package shadow

import (
	"container/list"
	"errors"
	"fmt"
	"sync"
	"unsafe"
)

// Scale is the number of application bytes covered by one shadow byte.
const Scale = 8

// Shadow byte values. Zero and 1..7 encode accessible prefixes; every other
// valid value is negative and poisons the whole eight-byte cell.
const (
	// ShadowAccessible means all eight bytes are addressable.
	ShadowAccessible int8 = 0
	// ShadowLeftRedzone poisons the red zone before a block.
	ShadowLeftRedzone int8 = -1
	// ShadowRightRedzone poisons the red zone after a block (and dead tails).
	ShadowRightRedzone int8 = -2
	// ShadowFreed poisons blocks that have been freed.
	ShadowFreed int8 = -3
)

// Access describes whether an instrumented access reads or writes memory.
type Access uint8

const (
	Read Access = iota + 1
	Write
)

func (a Access) String() string {
	switch a {
	case Read:
		return "read"
	case Write:
		return "write"
	default:
		return "unknown"
	}
}

// ErrorKind classifies the memory error reported by a check.
type ErrorKind int

const (
	KindOK ErrorKind = iota
	// KindHeapUnderflow: access touches the left red zone before a block.
	KindHeapUnderflow
	// KindHeapOverflow: access runs past the valid tail of a live block.
	KindHeapOverflow
	// KindUseAfterFree: access touches a block that has been freed.
	KindUseAfterFree
	// KindInvalidAccess: address range is outside the managed arena.
	KindInvalidAccess
	// KindInvalidSize: access size is not one of 1, 2, 4, 8 or 16 bytes.
	KindInvalidSize
)

func (k ErrorKind) String() string {
	switch k {
	case KindOK:
		return "ok"
	case KindHeapUnderflow:
		return "heap-buffer-underflow"
	case KindHeapOverflow:
		return "heap-buffer-overflow"
	case KindUseAfterFree:
		return "use-after-free"
	case KindInvalidAccess:
		return "invalid-access"
	case KindInvalidSize:
		return "invalid-size"
	default:
		return "unknown"
	}
}

// AccessError describes a failed instrumented access.
type AccessError struct {
	Kind     ErrorKind
	Access   Access
	Addr     uintptr // address of the access
	Size     int     // access size in bytes
	CellAddr uintptr // start address of the first bad shadow cell
	Shadow   int8    // shadow value of that cell
}

func (e *AccessError) Error() string {
	return fmt.Sprintf("shadow: %s: %s of %d byte(s) at %#x (shadow cell %#x = %d)",
		e.Kind, e.Access, e.Size, e.Addr, e.CellAddr, e.Shadow)
}

// Sentinel errors returned by Free.
var (
	ErrUnknownPointer = errors.New("shadow: pointer was not allocated by this detector")
	ErrDoubleFree     = errors.New("shadow: block is not live (double or quarantined free)")
)

type blockState uint8

const (
	stateLive blockState = iota + 1
	stateQuarantined
	stateEvicted
)

type block struct {
	payload unsafe.Pointer // first usable byte
	cap     int            // 8-aligned payload region owned by the block
	size    int            // live bytes currently exposed
	state   blockState
	seq     uint64
}

// Detector owns the arena, its shadow memory and the quarantine queue.
// It is safe for concurrent use.
type Detector struct {
	mu sync.RWMutex

	base      uintptr // 8-aligned first arena byte
	basePtr   unsafe.Pointer
	usable    int    // usable arena length, multiple of Scale
	highWater int    // mapped bytes relative to base
	mem       []byte // backing storage keeping the arena alive
	sh        []int8 // one shadow byte per Scale arena bytes

	blocks   map[unsafe.Pointer]*block // keyed by payload pointer
	freeList []*block                  // evicted blocks available for reuse

	q      *list.List // FIFO quarantine of *block
	qBytes int        // sum of quarantined block capacities
	qLimit int

	leftRZ  int
	rightRZ int
	nextSeq uint64
}

type options struct {
	capacity int
	qLimit   int
	leftRZ   int
	rightRZ  int
}

// Option configures a Detector at construction.
type Option func(*options)

// WithCapacity sets the arena capacity in bytes.
func WithCapacity(n int) Option {
	return func(o *options) { o.capacity = n }
}

// WithQuarantineLimit sets the maximum total quarantined payload bytes.
// Blocks are only evicted once this limit is exceeded.
func WithQuarantineLimit(n int) Option {
	return func(o *options) { o.qLimit = n }
}

// WithRedzones sets the left and right red zone sizes in bytes. Both must be
// positive multiples of Scale.
func WithRedzones(left, right int) Option {
	return func(o *options) {
		o.leftRZ = left
		o.rightRZ = right
	}
}

const (
	defaultCapacity = 4 << 20
	defaultQLimit   = 1 << 20
	defaultRedzone  = 16
)

// New creates a Detector with the given options.
func New(opts ...Option) *Detector {
	o := options{
		capacity: defaultCapacity,
		qLimit:   defaultQLimit,
		leftRZ:   defaultRedzone,
		rightRZ:  defaultRedzone,
	}
	for _, opt := range opts {
		opt(&o)
	}
	if o.capacity < Scale {
		panic("shadow: arena capacity must be at least 8 bytes")
	}
	if o.qLimit < 0 {
		panic("shadow: quarantine limit must not be negative")
	}
	if o.leftRZ <= 0 || o.rightRZ <= 0 ||
		o.leftRZ%Scale != 0 || o.rightRZ%Scale != 0 {
		panic("shadow: red zone sizes must be positive multiples of 8")
	}

	raw := make([]byte, o.capacity+Scale)
	rawBase := uintptr(unsafe.Pointer(unsafe.SliceData(raw)))
	base := (rawBase + Scale - 1) &^ (Scale - 1)
	usable := (len(raw) - int(base-rawBase)) &^ (Scale - 1)

	return &Detector{
		base:    base,
		basePtr: unsafe.Add(unsafe.Pointer(unsafe.SliceData(raw)), int(base-rawBase)),
		usable:  usable,
		mem:     raw,
		sh:      make([]int8, usable/Scale),
		blocks:  make(map[unsafe.Pointer]*block),
		q:       list.New(),
		qLimit:  o.qLimit,
		leftRZ:  o.leftRZ,
		rightRZ: o.rightRZ,
	}
}

// at returns a pointer to the arena byte at offset off from base.
func (d *Detector) at(off int) unsafe.Pointer {
	return unsafe.Add(d.basePtr, off)
}

// markRange writes v to every shadow cell covering the n bytes at addr.
// n must be a multiple of Scale.
func (d *Detector) markRange(addr unsafe.Pointer, n int, v int8) {
	idx := int((uintptr(addr) - d.base) >> 3)
	for i := 0; i < n/Scale; i++ {
		d.sh[idx+i] = v
	}
}

// markPayload marks a live payload: full cells zero and the optional tail
// cell carrying the number of valid prefix bytes.
func (d *Detector) markPayload(addr unsafe.Pointer, size int) {
	idx := int((uintptr(addr) - d.base) >> 3)
	full := size >> 3
	for i := 0; i < full; i++ {
		d.sh[idx+i] = ShadowAccessible
	}
	if rem := size & (Scale - 1); rem != 0 {
		d.sh[idx+full] = int8(rem)
	}
}

// Alloc reserves size bytes in the arena and returns the first usable byte.
// The payload is surrounded by poisoned red zones. It panics when size is not
// positive or when the arena is exhausted.
func (d *Detector) Alloc(size int) unsafe.Pointer {
	if size <= 0 {
		panic("shadow: allocation size must be positive")
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	if b := d.pickFree(size); b != nil {
		d.reuse(b, size)
		return b.payload
	}

	padded := (size + Scale - 1) &^ (Scale - 1)
	stride := d.leftRZ + padded + d.rightRZ
	if d.highWater+stride > d.usable {
		panic("shadow: arena exhausted")
	}

	blockStart := d.at(d.highWater)
	payload := d.at(d.highWater + d.leftRZ)

	d.markRange(blockStart, d.leftRZ, ShadowLeftRedzone)
	d.markRange(payload, padded, ShadowRightRedzone)
	d.markPayload(payload, size)
	d.markRange(unsafe.Add(payload, padded), d.rightRZ, ShadowRightRedzone)

	d.highWater += stride
	b := &block{
		payload: payload,
		cap:     padded,
		size:    size,
		state:   stateLive,
		seq:     d.nextSeq,
	}
	d.nextSeq++
	d.blocks[b.payload] = b
	return b.payload
}

// AllocBytes is Alloc followed by a slice header over the returned region.
func (d *Detector) AllocBytes(size int) []byte {
	p := d.Alloc(size)
	return unsafe.Slice((*byte)(p), size)
}

// pickFree returns the smallest evicted block that fits size (best fit).
func (d *Detector) pickFree(size int) *block {
	choice := -1
	for i, b := range d.freeList {
		if b.cap < size {
			continue
		}
		if choice == -1 || b.cap < d.freeList[choice].cap {
			choice = i
		}
	}
	if choice == -1 {
		return nil
	}
	b := d.freeList[choice]
	d.freeList = append(d.freeList[:choice], d.freeList[choice+1:]...)
	return b
}

// reuse reactivates an evicted block for a new (possibly smaller) allocation.
func (d *Detector) reuse(b *block, size int) {
	b.state = stateLive
	b.size = size
	// Poison the whole region first so dead tail bytes stay inaccessible.
	d.markRange(b.payload, b.cap, ShadowRightRedzone)
	d.markPayload(b.payload, size)
}

// Free moves a live block into the quarantine. Its payload is poisoned as
// freed; the oldest quarantined blocks are evicted only once the total byte
// count exceeds the limit. Evicted memory stays poisoned until reallocated.
func (d *Detector) Free(p unsafe.Pointer) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	b, ok := d.blocks[p]
	if !ok {
		return ErrUnknownPointer
	}
	if b.state != stateLive {
		return fmt.Errorf("%w (seq %d)", ErrDoubleFree, b.seq)
	}

	b.state = stateQuarantined
	d.markRange(b.payload, b.cap, ShadowFreed)
	d.q.PushBack(b)
	d.qBytes += b.cap

	for d.qBytes > d.qLimit {
		front := d.q.Front()
		old := front.Value.(*block)
		d.q.Remove(front)
		d.qBytes -= old.cap
		old.state = stateEvicted
		d.freeList = append(d.freeList, old)
	}
	return nil
}

// Check validates an access of size bytes starting at addr. Size must be one
// of 1, 2, 4, 8 or 16; the access may start at any byte offset.
func (d *Detector) Check(addr unsafe.Pointer, size int, acc Access) error {
	if size != 1 && size != 2 && size != 4 && size != 8 && size != 16 {
		return &AccessError{
			Kind:   KindInvalidSize,
			Access: acc,
			Addr:   uintptr(addr),
			Size:   size,
		}
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.check(uintptr(addr), size, acc)
}

// Load is Check for a read access.
func (d *Detector) Load(addr unsafe.Pointer, size int) error {
	return d.Check(addr, size, Read)
}

// Store is Check for a write access.
func (d *Detector) Store(addr unsafe.Pointer, size int) error {
	return d.Check(addr, size, Write)
}

// CheckSlice validates an access covering the full slice length.
func (d *Detector) CheckSlice(b []byte, acc Access) error {
	if len(b) == 0 {
		return &AccessError{Kind: KindInvalidSize, Access: acc, Size: 0}
	}
	return d.Check(unsafe.Pointer(unsafe.SliceData(b)), len(b), acc)
}

func (d *Detector) check(addr uintptr, size int, acc Access) error {
	end := addr + uintptr(size)
	if addr < d.base || end < addr || end > d.base+uintptr(d.highWater) {
		return &AccessError{
			Kind:   KindInvalidAccess,
			Access: acc,
			Addr:   addr,
			Size:   size,
		}
	}

	first := (addr - d.base) >> 3
	last := (end - 1 - d.base) >> 3
	for cell := first; cell <= last; cell++ {
		v := d.sh[cell]
		cellAddr := d.base + cell*Scale
		// Poisoned cell: the whole eight bytes are invalid.
		if v < 0 {
			return &AccessError{
				Kind:     poisonKind(v),
				Access:   acc,
				Addr:     addr,
				Size:     size,
				CellAddr: cellAddr,
				Shadow:   v,
			}
		}
		// Partial tail cell: only the first v bytes are addressable. The
		// access must end within that prefix.
		if v > 0 {
			accessibleEnd := cellAddr + uintptr(v)
			if end > accessibleEnd {
				return &AccessError{
					Kind:     KindHeapOverflow,
					Access:   acc,
					Addr:     addr,
					Size:     size,
					CellAddr: cellAddr,
					Shadow:   v,
				}
			}
		}
	}
	return nil
}

func poisonKind(v int8) ErrorKind {
	switch v {
	case ShadowLeftRedzone:
		return KindHeapUnderflow
	case ShadowRightRedzone:
		return KindHeapOverflow
	case ShadowFreed:
		return KindUseAfterFree
	default:
		return KindInvalidAccess
	}
}

// ShadowAt returns the shadow byte governing addr. The second result is false
// when addr is outside the mapped arena.
func (d *Detector) ShadowAt(addr unsafe.Pointer) (int8, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	off := int(uintptr(addr) - d.base)
	if off < 0 || off >= d.highWater {
		return 0, false
	}
	return d.sh[off/Scale], true
}

// QuarantineBytes reports the total payload bytes currently quarantined.
func (d *Detector) QuarantineBytes() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.qBytes
}

// QuarantineLimit reports the configured quarantine byte limit.
func (d *Detector) QuarantineLimit() int {
	return d.qLimit
}

// Stats reports a snapshot of detector state.
type Stats struct {
	ArenaBytes      int
	MappedBytes     int
	LiveBlocks      int
	Quarantined     int
	QuarantineBytes int
	ReusableBlocks  int
}

// Snapshot returns a snapshot of the current detector state.
func (d *Detector) Snapshot() Stats {
	d.mu.RLock()
	defer d.mu.RUnlock()

	s := Stats{
		ArenaBytes:      d.usable,
		MappedBytes:     d.highWater,
		QuarantineBytes: d.qBytes,
		ReusableBlocks:  len(d.freeList),
	}
	for _, b := range d.blocks {
		switch b.state {
		case stateLive:
			s.LiveBlocks++
		case stateQuarantined:
			s.Quarantined++
		}
	}
	return s
}
