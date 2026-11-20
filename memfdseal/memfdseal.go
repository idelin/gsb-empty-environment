// Package memfdseal simulates the Linux memfd sealing state machine for a
// sandbox host that hands file descriptors to untrusted processes.
//
// The host builds a configuration or executable image in a memfd, seals it,
// and only then passes a duplicated descriptor to the untrusted peer. This
// package models the effects of F_SEAL_SEAL, F_SEAL_SHRINK, F_SEAL_GROW,
// F_SEAL_WRITE and F_SEAL_FUTURE_WRITE so the host can validate an
// operation sequence before performing it on a real descriptor.
//
// Delivery condition: F_SEAL_WRITE, F_SEAL_SHRINK, F_SEAL_GROW and
// F_SEAL_SEAL must all be set, and no writable shared mapping may remain.
// F_SEAL_FUTURE_WRITE alone is not sufficient: mappings that already exist
// stay writable after it is added.
package memfdseal

import "errors"

var (
	// ErrPerm mirrors EPERM: the operation is forbidden by the current
	// seal state or by a missing MFD_ALLOW_SEALING flag.
	ErrPerm = errors.New("memfdseal: operation not permitted (EPERM)")
	// ErrBusy mirrors EBUSY: F_SEAL_WRITE cannot be added while a
	// writable shared mapping exists.
	ErrBusy = errors.New("memfdseal: writable shared mapping exists (EBUSY)")
	// ErrInvalid mirrors EINVAL: bad argument such as a negative size or
	// an out-of-range access.
	ErrInvalid = errors.New("memfdseal: invalid argument (EINVAL)")
	// ErrNotDeliverable is returned by Dup when the delivery condition is
	// not met.
	ErrNotDeliverable = errors.New("memfdseal: seals WRITE|SHRINK|GROW|SEAL not all set or writable mapping remains")
)

// Seal is a bit set of F_SEAL_* values.
type Seal int

const (
	SealSeal        Seal = 1 << iota // F_SEAL_SEAL: no further seals can be added
	SealShrink                       // F_SEAL_SHRINK: file cannot shrink
	SealGrow                         // F_SEAL_GROW: file cannot grow
	SealWrite                        // F_SEAL_WRITE: content is immutable
	SealFutureWrite                  // F_SEAL_FUTURE_WRITE: no new writable mappings/writes
)

// deliverySeals is the full seal set required before handoff.
const deliverySeals = SealWrite | SealShrink | SealGrow | SealSeal

// File is a simulated memfd.
type File struct {
	allowSealing bool
	seals        Seal
	data         []byte
	offset       int64
	mappings     map[int]*Mapping
	nextMapID    int
}

// New creates a memfd. allowSealing mirrors memfd_create(MFD_ALLOW_SEALING);
// without it no seal can ever be added.
func New(allowSealing bool) *File {
	return &File{
		allowSealing: allowSealing,
		mappings:     make(map[int]*Mapping),
	}
}

// Seals reports the currently installed seals.
func (f *File) Seals() Seal { return f.seals }

// Size reports the current file size.
func (f *File) Size() int64 { return int64(len(f.data)) }

// Write writes p at the current offset, growing the file as needed, and
// advances the offset. It fails with ErrPerm once SealWrite or
// SealFutureWrite is installed.
func (f *File) Write(p []byte) (int, error) {
	if f.seals&(SealWrite|SealFutureWrite) != 0 {
		return 0, ErrPerm
	}
	end := f.offset + int64(len(p))
	if grow := end - int64(len(f.data)); grow > 0 {
		f.data = append(f.data, make([]byte, grow)...)
	}
	copy(f.data[f.offset:end], p)
	f.offset = end
	return len(p), nil
}

// SetOffset sets the offset for the next Write.
func (f *File) SetOffset(offset int64) error {
	if offset < 0 {
		return ErrInvalid
	}
	f.offset = offset
	return nil
}

// Truncate mirrors ftruncate. Shrinking requires !SealShrink, growing
// requires !SealGrow.
func (f *File) Truncate(size int64) error {
	if size < 0 {
		return ErrInvalid
	}
	cur := int64(len(f.data))
	switch {
	case size < cur && f.seals&SealShrink != 0:
		return ErrPerm
	case size > cur && f.seals&SealGrow != 0:
		return ErrPerm
	}
	switch {
	case size < cur:
		f.data = f.data[:size]
	case size > cur:
		f.data = append(f.data, make([]byte, size-cur)...)
	}
	if f.offset > size {
		f.offset = size
	}
	return nil
}

// Mmap mirrors mmap of the memfd. A writable shared mapping is refused once
// SealWrite or SealFutureWrite is installed. A private mapping never
// modifies the file (copy-on-write) and is always allowed.
func (f *File) Mmap(shared, writable bool) (*Mapping, error) {
	if shared && writable && f.seals&(SealWrite|SealFutureWrite) != 0 {
		return nil, ErrPerm
	}
	f.nextMapID++
	m := &Mapping{
		id:       f.nextMapID,
		file:     f,
		shared:   shared,
		writable: writable,
	}
	f.mappings[m.id] = m
	return m, nil
}

// AddSeals mirrors fcntl(F_ADD_SEALS). It fails with ErrPerm if the memfd
// was created without MFD_ALLOW_SEALING or if SealSeal is already set, and
// with ErrBusy if SealWrite is requested while a writable shared mapping
// exists.
func (f *File) AddSeals(seals Seal) error {
	if !f.allowSealing {
		return ErrPerm
	}
	if f.seals&SealSeal != 0 {
		return ErrPerm
	}
	if seals&SealWrite != 0 && f.hasWritableSharedMapping() {
		return ErrBusy
	}
	f.seals |= seals
	return nil
}

// WritableMappings reports whether any writable shared mapping exists.
// Private writable mappings are copy-on-write and cannot alter the file, so
// they do not count.
func (f *File) WritableMappings() bool { return f.hasWritableSharedMapping() }

func (f *File) hasWritableSharedMapping() bool {
	for _, m := range f.mappings {
		if m.shared && m.writable {
			return true
		}
	}
	return false
}

// Deliverable reports whether the delivery condition holds: SealWrite,
// SealShrink, SealGrow and SealSeal are all set and no writable shared
// mapping remains.
func (f *File) Deliverable() bool {
	return f.seals&deliverySeals == deliverySeals && !f.hasWritableSharedMapping()
}

// Dup mirrors dup() for handoff to the untrusted process. It must only be
// called when the delivery condition holds; otherwise it returns
// ErrNotDeliverable and no descriptor is leaked.
func (f *File) Dup() (*File, error) {
	if !f.Deliverable() {
		return nil, ErrNotDeliverable
	}
	return f, nil
}

// Mapping is a simulated mmap of the file.
type Mapping struct {
	id       int
	file     *File
	shared   bool
	writable bool
	unmapped bool
}

// Shared reports whether the mapping is MAP_SHARED.
func (m *Mapping) Shared() bool { return m.shared }

// Writable reports whether the mapping is PROT_WRITE.
func (m *Mapping) Writable() bool { return m.writable }

// Write modifies the file through the mapping. A writable shared mapping
// created before SealFutureWrite was installed keeps its write access,
// which is exactly why SealFutureWrite alone does not satisfy the delivery
// condition. Writes stay within the current file size.
func (m *Mapping) Write(offset int64, p []byte) (int, error) {
	if m.unmapped {
		return 0, ErrInvalid
	}
	if !m.writable {
		return 0, ErrPerm
	}
	if !m.shared {
		// Private mapping: copy-on-write, file content is untouched.
		return len(p), nil
	}
	if offset < 0 || offset+int64(len(p)) > int64(len(m.file.data)) {
		return 0, ErrInvalid
	}
	copy(m.file.data[offset:offset+int64(len(p))], p)
	return len(p), nil
}

// Read returns the file content visible through the mapping.
func (m *Mapping) Read() ([]byte, error) {
	if m.unmapped {
		return nil, ErrInvalid
	}
	out := make([]byte, len(m.file.data))
	copy(out, m.file.data)
	return out, nil
}

// Unmap mirrors munmap.
func (m *Mapping) Unmap() error {
	if m.unmapped {
		return ErrInvalid
	}
	m.unmapped = true
	delete(m.file.mappings, m.id)
	return nil
}
