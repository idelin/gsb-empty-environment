package netsec

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Device is the backing persistent memory.  Stripes are addressed by logical
// index; every stripe is exactly StripeSize bytes.  Implementations must not
// acknowledge bytes they did not make durable: a count other than len(p), or
// an error, makes the manager reject the stripe as unverified.
type Device interface {
	// Prepare creates the region if needed and validates its size.  It must
	// preserve the bytes of an existing region of the same size.
	Prepare(regionID string, size int64) error
	// WriteStripe writes p to stripe index and returns the number of bytes
	// written.
	WriteStripe(regionID string, index int64, p []byte) (int, error)
	// ReadStripe reads stripe index into p and returns the number of bytes
	// read.
	ReadStripe(regionID string, index int64, p []byte) (int, error)
}

// FileDevice stores one sparse file per region under a directory.  Each
// stripe write can optionally be fsynced before it is acknowledged.
type FileDevice struct {
	dir      string
	syncMode SyncMode

	mu     sync.Mutex
	files  map[string]*os.File
	sizes  map[string]int64
	closed bool
}

// NewFileDevice creates or opens a directory-backed device.
func NewFileDevice(dir string, syncMode SyncMode) (*FileDevice, error) {
	if dir == "" {
		return nil, fmt.Errorf("%w: empty device directory", ErrInvalidRegion)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &FileDevice{
		dir:      dir,
		syncMode: syncMode,
		files:    make(map[string]*os.File),
		sizes:    make(map[string]int64),
	}, nil
}

// Dir returns the device directory.
func (d *FileDevice) Dir() string { return d.dir }

func (d *FileDevice) Prepare(regionID string, size int64) error {
	if err := validateRegionSize(size); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return ErrClosed
	}
	_, err := d.openLocked(regionID, size)
	return err
}

func (d *FileDevice) WriteStripe(regionID string, index int64, p []byte) (int, error) {
	if len(p) != int(StripeSize) {
		return 0, fmt.Errorf("%w: stripe buffer is %d bytes, want %d", ErrInvalidRegion, len(p), StripeSize)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return 0, ErrClosed
	}
	f, err := d.openLocked(regionID, 0)
	if err != nil {
		return 0, err
	}
	size := d.sizes[regionID]
	off, err := stripeOffset(index, size, int64(len(p)))
	if err != nil {
		return 0, err
	}
	n, err := f.WriteAt(p, off)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	if err == nil && d.syncMode == SyncEachStripe {
		err = f.Sync()
	}
	return n, err
}

func (d *FileDevice) ReadStripe(regionID string, index int64, p []byte) (int, error) {
	if len(p) != int(StripeSize) {
		return 0, fmt.Errorf("%w: stripe buffer is %d bytes, want %d", ErrInvalidRegion, len(p), StripeSize)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return 0, ErrClosed
	}
	f, err := d.openLocked(regionID, 0)
	if err != nil {
		return 0, err
	}
	size := d.sizes[regionID]
	off, err := stripeOffset(index, size, int64(len(p)))
	if err != nil {
		return 0, err
	}
	n, err := f.ReadAt(p, off)
	if err == nil && n != len(p) {
		err = io.ErrUnexpectedEOF
	}
	return n, err
}

// Close closes all open region files.
func (d *FileDevice) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil
	}
	d.closed = true
	var errs []error
	for id, f := range d.files {
		if err := f.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close region %q: %w", id, err))
		}
		delete(d.files, id)
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// openLocked opens a region.  If size is zero the already-known size is used.
func (d *FileDevice) openLocked(regionID string, size int64) (*os.File, error) {
	if f, ok := d.files[regionID]; ok {
		if size != 0 && d.sizes[regionID] != size {
			return nil, fmt.Errorf("%w: region %q", ErrRegionSizeMismatch, regionID)
		}
		return f, nil
	}
	if err := validateRegionID(regionID); err != nil {
		return nil, err
	}
	path := regionFilePath(d.dir, regionID)
	info, statErr := os.Stat(path)
	exists := statErr == nil
	if statErr != nil && !os.IsNotExist(statErr) {
		return nil, statErr
	}
	if size == 0 {
		switch {
		case exists:
			size = info.Size()
		default:
			return nil, fmt.Errorf("%w: region %q", ErrRegionNotFound, regionID)
		}
	}
	if exists && info.Size() != 0 && info.Size() != size {
		return nil, fmt.Errorf("%w: region %q has %d bytes, journal says %d", ErrRegionSizeMismatch, regionID, info.Size(), size)
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return nil, err
	}
	if !exists || info.Size() == 0 {
		if err := f.Truncate(size); err != nil {
			f.Close()
			return nil, err
		}
		if err := f.Sync(); err != nil {
			f.Close()
			return nil, err
		}
		if err := syncDir(d.dir); err != nil {
			f.Close()
			return nil, err
		}
	}
	d.files[regionID] = f
	d.sizes[regionID] = size
	return f, nil
}

func stripeOffset(index, size, stripeLen int64) (int64, error) {
	if index < 0 || size < stripeLen || index >= size/stripeLen {
		return 0, fmt.Errorf("%w: stripe %d outside region of %d bytes", ErrInvalidRegion, index, size)
	}
	off := index * StripeSize
	if off+stripeLen > size {
		return 0, fmt.Errorf("%w: stripe %d outside region of %d bytes", ErrInvalidRegion, index, size)
	}
	return off, nil
}

func regionFilePath(dir, regionID string) string {
	sum := sha256.Sum256([]byte(regionID))
	return filepath.Join(dir, hex.EncodeToString(sum[:])+".region")
}

func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

// MemDevice is an in-memory Device useful for tests and embedded simulations.
// It deliberately leaves incomplete writes visible so a skipped stripe can be
// detected by the manager's read-back verification.
type MemDevice struct {
	mu      sync.Mutex
	regions map[string][]byte
	omitted map[string]map[int64]bool
	writes  map[string]map[int64]int
	closed  bool
}

// NewMemDevice constructs an empty in-memory device.
func NewMemDevice() *MemDevice {
	return &MemDevice{
		regions: make(map[string][]byte),
		omitted: make(map[string]map[int64]bool),
		writes:  make(map[string]map[int64]int),
	}
}

func (d *MemDevice) Prepare(regionID string, size int64) error {
	if err := validateRegionID(regionID); err != nil {
		return err
	}
	if err := validateRegionSize(size); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return ErrClosed
	}
	d.initMapsLocked()
	if buf, ok := d.regions[regionID]; ok {
		if int64(len(buf)) != size {
			return fmt.Errorf("%w: region %q", ErrRegionSizeMismatch, regionID)
		}
		return nil
	}
	d.regions[regionID] = make([]byte, size)
	return nil
}

func (d *MemDevice) WriteStripe(regionID string, index int64, p []byte) (int, error) {
	if len(p) != int(StripeSize) {
		return 0, fmt.Errorf("%w: stripe buffer is %d bytes, want %d", ErrInvalidRegion, len(p), StripeSize)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return 0, ErrClosed
	}
	d.initMapsLocked()
	buf, ok := d.regions[regionID]
	if !ok {
		return 0, fmt.Errorf("%w: region %q", ErrRegionNotFound, regionID)
	}
	off, err := stripeOffset(index, int64(len(buf)), int64(len(p)))
	if err != nil {
		return 0, err
	}
	if d.writes[regionID] == nil {
		d.writes[regionID] = make(map[int64]int)
	}
	d.writes[regionID][index]++
	if d.omitted[regionID] != nil && d.omitted[regionID][index] {
		// Model a device that acknowledges a write but drops it.
		return len(p), nil
	}
	copy(buf[off:], p)
	return len(p), nil
}

func (d *MemDevice) ReadStripe(regionID string, index int64, p []byte) (int, error) {
	if len(p) != int(StripeSize) {
		return 0, fmt.Errorf("%w: stripe buffer is %d bytes, want %d", ErrInvalidRegion, len(p), StripeSize)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return 0, ErrClosed
	}
	d.initMapsLocked()
	buf, ok := d.regions[regionID]
	if !ok {
		return 0, fmt.Errorf("%w: region %q", ErrRegionNotFound, regionID)
	}
	off, err := stripeOffset(index, int64(len(buf)), int64(len(p)))
	if err != nil {
		return 0, err
	}
	copy(p, buf[off:off+int64(len(p))])
	return len(p), nil
}

// SetOmitStripe makes WriteStripe acknowledge but discard the selected stripe.
// This models a lost write and is used to prove that read-back confirmation
// prevents a partially erased region from becoming clean.
func (d *MemDevice) SetOmitStripe(regionID string, index int64, omit bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.initMapsLocked()
	if d.omitted[regionID] == nil {
		d.omitted[regionID] = make(map[int64]bool)
	}
	d.omitted[regionID][index] = omit
}

// WriteCount returns how many write attempts targeted a stripe.
func (d *MemDevice) WriteCount(regionID string, index int64) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.writes[regionID] == nil {
		return 0
	}
	return d.writes[regionID][index]
}

// Data returns a copy of the region's current bytes.
func (d *MemDevice) Data(regionID string) []byte {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.initMapsLocked()
	buf := d.regions[regionID]
	out := make([]byte, len(buf))
	copy(out, buf)
	return out
}

// Close marks the device closed and drops its contents.
func (d *MemDevice) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.closed = true
	d.regions = nil
	return nil
}

func (d *MemDevice) initMapsLocked() {
	if d.regions == nil {
		d.regions = make(map[string][]byte)
	}
	if d.omitted == nil {
		d.omitted = make(map[string]map[int64]bool)
	}
	if d.writes == nil {
		d.writes = make(map[string]map[int64]int)
	}
}

func validateRegionSize(size int64) error {
	if size <= 0 || size%StripeSize != 0 {
		return fmt.Errorf("%w: size %d is not a positive multiple of %d", ErrInvalidRegion, size, StripeSize)
	}
	return nil
}

func validateRegionID(id string) error {
	switch {
	case id == "":
		return fmt.Errorf("%w: empty region ID", ErrInvalidRegion)
	case len(id) > 128:
		return fmt.Errorf("%w: region ID longer than 128 bytes", ErrInvalidRegion)
	case strings.ContainsRune(id, '\x00'):
		return fmt.Errorf("%w: region ID contains NUL", ErrInvalidRegion)
	default:
		return nil
	}
}
