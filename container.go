package netsec

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/bits"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"unicode/utf8"
)

// DType is one of the element types supported by the container format.
type DType string

const (
	DTypeU8  DType = "U8"
	DTypeI32 DType = "I32"
	DTypeF32 DType = "F32"
	DTypeF64 DType = "F64"
)

// ByteWidth returns the number of bytes occupied by one element.
func (d DType) ByteWidth() (uint64, bool) {
	switch d {
	case DTypeU8:
		return 1, true
	case DTypeI32, DTypeF32:
		return 4, true
	case DTypeF64:
		return 8, true
	default:
		return 0, false
	}
}

// Errors returned by the package. Detail errors wrap one of these sentinels,
// so callers should normally use errors.Is rather than compare strings.
var (
	ErrInvalidContainer = errors.New("netsec: invalid tensor container")
	ErrDuplicateKey     = errors.New("netsec: duplicate JSON key")
	ErrInvalidNumber    = errors.New("netsec: invalid integer")
	ErrInvalidDType     = errors.New("netsec: invalid dtype")
	ErrInvalidShape     = errors.New("netsec: invalid shape")
	ErrInvalidOffsets   = errors.New("netsec: invalid data offsets")
	ErrIntegerOverflow  = errors.New("netsec: integer overflow")
	ErrOverlap          = errors.New("netsec: overlapping tensor ranges")
	ErrShortRead        = errors.New("netsec: short read")
	ErrFileChanged      = errors.New("netsec: source file changed")
	ErrClosed           = errors.New("netsec: model closed")
)

const (
	headerSize            = uint64(8)
	defaultMaxMetadataLen = uint64(256 << 20)
)

// MaxMetadataBytes is the largest metadata object accepted by Parse. The
// format itself has no limit; this bound prevents an untrusted length field
// from forcing an unbounded allocation.
var MaxMetadataBytes = defaultMaxMetadataLen

// Tensor describes one tensor and owns a read-only view of its data.
//
// Data is an io.ReaderAt limited to the tensor's [DataOffsets[0],
// DataOffsets[1]) range in the data region. Tensor itself also implements
// io.ReaderAt. Both views share the source generation guard installed by Parse.
type Tensor struct {
	Name        string
	DType       DType
	Shape       []uint64
	DataOffsets [2]uint64

	// Data is the read-only data view. It is never a writable byte slice.
	Data io.ReaderAt

	data       *tensorData
	byteLength uint64
	manifest   [sha256.Size]byte
}

// ByteLength returns the validated byte length of the tensor.
func (t *Tensor) ByteLength() uint64 {
	if t == nil {
		return 0
	}
	return t.byteLength
}

// ElementCount returns the element count when it fits in uint64.
func (t *Tensor) ElementCount() (uint64, bool) {
	if t == nil {
		return 0, false
	}
	count, ok := elementCount(t.Shape)
	return count, ok
}

// Manifest returns the model manifest digest associated with this tensor.
func (t *Tensor) Manifest() [sha256.Size]byte {
	if t == nil {
		return [sha256.Size]byte{}
	}
	return t.manifest
}

// ReaderAt returns a fresh interface value for the tensor's read-only data
// view. The view is still guarded by the same source generation.
func (t *Tensor) ReaderAt() io.ReaderAt {
	if t == nil {
		return nil
	}
	return t.Data
}

// ReadAt implements io.ReaderAt for the tensor's data range.
func (t *Tensor) ReadAt(p []byte, off int64) (int, error) {
	if t == nil || t.data == nil {
		return 0, ErrInvalidContainer
	}
	return t.data.ReadAt(p, off)
}

// Model is a fully validated tensor container.
type Model struct {
	// Manifest is SHA-256 over the canonical metadata encoding.
	Manifest [sha256.Size]byte
	// Tensors is indexed by the exact UTF-8 tensor name.
	Tensors map[string]*Tensor

	canonical []byte
	life      *sourceLife
}

// Lookup returns a tensor by name.
func (m *Model) Lookup(name string) (*Tensor, bool) {
	if m == nil || m.Tensors == nil {
		return nil, false
	}
	t, ok := m.Tensors[name]
	return t, ok
}

// Tensor returns a tensor by name.
func (m *Model) Tensor(name string) (*Tensor, bool) {
	return m.Lookup(name)
}

// Names returns the tensor names in UTF-8 byte order.
func (m *Model) Names() []string {
	if m == nil {
		return nil
	}
	names := make([]string, 0, len(m.Tensors))
	for name := range m.Tensors {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		return bytes.Compare([]byte(names[i]), []byte(names[j])) < 0
	})
	return names
}

// ManifestHex returns the manifest digest as lower-case hexadecimal.
func (m *Model) ManifestHex() string {
	if m == nil {
		return ""
	}
	return hex.EncodeToString(m.Manifest[:])
}

// CanonicalMetadata returns a copy of the canonical metadata bytes used for
// the manifest.
func (m *Model) CanonicalMetadata() []byte {
	if m == nil {
		return nil
	}
	return bytes.Clone(m.canonical)
}

// Close releases the file descriptor, when the model was loaded from a path.
// All tensor views fail after Close. It is safe to call Close more than once.
func (m *Model) Close() error {
	if m == nil || m.life == nil {
		return nil
	}
	return m.life.release()
}

type tensorData struct {
	t        *Tensor
	raw      io.ReaderAt
	absStart uint64
	length   uint64
	life     *sourceLife
	manifest [sha256.Size]byte
}

func (d *tensorData) ReadAt(p []byte, off int64) (int, error) {
	if d == nil || d.t == nil || d.raw == nil || d.life == nil ||
		d.t.manifest != d.manifest {
		return 0, ErrInvalidContainer
	}
	if off < 0 {
		return 0, fmt.Errorf("%w: negative read offset", ErrInvalidOffsets)
	}
	if err := d.life.verify(); err != nil {
		return 0, err
	}
	if uint64(off) >= d.length {
		if len(p) == 0 {
			return 0, nil
		}
		return 0, io.EOF
	}
	want := uint64(len(p))
	if want > d.length-uint64(off) {
		want = d.length - uint64(off)
	}
	abs := d.absStart + uint64(off)
	if abs < d.absStart || abs > uint64(^uint64(0)>>1) {
		return 0, fmt.Errorf("%w: absolute read offset overflow", ErrInvalidOffsets)
	}

	n, err := readAtLeast(d.raw, p[:want], int64(abs))
	if n < 0 || uint64(n) > want {
		return 0, fmt.Errorf("%w: ReaderAt returned an invalid count", ErrInvalidContainer)
	}
	if checkErr := d.life.verify(); checkErr != nil {
		return 0, checkErr
	}
	if uint64(n) < want {
		if err == nil || errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		return n, fmt.Errorf("%w: %w", ErrShortRead, err)
	}
	if uint64(n) < uint64(len(p)) && err == nil {
		err = io.EOF
	}
	return n, err
}

func readAtLeast(r io.ReaderAt, p []byte, off int64) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if off < 0 {
		return 0, fmt.Errorf("%w: negative read offset", ErrInvalidOffsets)
	}
	total := 0
	for len(p) > 0 {
		n, err := r.ReadAt(p, off)
		if n < 0 || n > len(p) {
			return total, fmt.Errorf("%w: ReaderAt returned an invalid count", ErrInvalidContainer)
		}
		total += n
		p = p[n:]
		off += int64(n)
		if err != nil {
			return total, err
		}
		if n == 0 {
			return total, io.ErrUnexpectedEOF
		}
	}
	return total, nil
}

type sourceLife struct {
	closed  atomic.Bool
	check   func() error
	closeFn func() error
	once    sync.Once
	err     error
}

func (s *sourceLife) verify() error {
	if s == nil {
		return ErrInvalidContainer
	}
	if s.closed.Load() {
		return ErrClosed
	}
	if s.check != nil {
		if err := s.check(); err != nil {
			return err
		}
	}
	if s.closed.Load() {
		return ErrClosed
	}
	return nil
}

func (s *sourceLife) release() error {
	if s == nil {
		return nil
	}
	s.closed.Store(true)
	s.once.Do(func() {
		if s.closeFn != nil {
			s.err = s.closeFn()
		}
	})
	return s.err
}

// Option customizes a parse operation. Options are primarily useful with
// non-file ReaderAt implementations that do not expose their own generation.
type Option func(*parseOptions)

type parseOptions struct {
	guard      func() error
	generation func() uint64
}

// WithGuard runs check before and after every source read. A non-nil error
// makes the read fail with that error. The check should report ErrFileChanged
// for a changed source.
func WithGuard(check func() error) Option {
	return func(o *parseOptions) {
		if check != nil {
			o.guard = check
		}
	}
}

// WithGeneration records a monotonically changing source generation. It is
// checked before and after every read. This is useful for sparse or in-memory
// ReaderAt test doubles.
func WithGeneration(generation func() uint64) Option {
	return func(o *parseOptions) {
		if generation != nil {
			o.generation = generation
		}
	}
}

type generationUint interface {
	Generation() uint64
}

type generationInt interface {
	Generation() int64
}

type snapshotUint interface {
	Snapshot() uint64
}

type sizeInt interface {
	Size() int64
}

type sizeUint interface {
	Size() uint64
}

// Parse validates a whole container read through r. size is the current
// source size. No tensor view is returned unless every metadata and range
// check succeeds.
//
// If r is an *os.File, its descriptor size/modification time and path identity
// are checked on every read, but Parse does not take ownership of the handle.
// If r exposes Generation() uint64, Generation() int64, or Snapshot() uint64,
// Parse binds the views to that generation. A size reported by Size() is also
// checked. WithGeneration and WithGuard can supply the same checks explicitly.
func Parse(r io.ReaderAt, size int64, opts ...Option) (*Model, error) {
	if isNilReaderAt(r) {
		return nil, fmt.Errorf("%w: nil ReaderAt", ErrInvalidContainer)
	}
	if size < 0 {
		return nil, fmt.Errorf("%w: negative source size", ErrInvalidContainer)
	}

	o := parseOptions{guard: func() error { return nil }}
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}

	var initialGeneration uint64
	haveGeneration := false
	if o.generation != nil {
		initialGeneration = o.generation()
		haveGeneration = true
	} else {
		switch g := any(r).(type) {
		case generationUint:
			initialGeneration = g.Generation()
			haveGeneration = true
		case generationInt:
			initialGeneration = uint64(g.Generation())
			haveGeneration = true
		case snapshotUint:
			initialGeneration = g.Snapshot()
			haveGeneration = true
		}
	}
	if o.generation == nil && haveGeneration {
		gen := initialGeneration
		o.generation = func() uint64 {
			switch g := any(r).(type) {
			case generationUint:
				return g.Generation()
			case generationInt:
				return uint64(g.Generation())
			case snapshotUint:
				return g.Snapshot()
			default:
				return gen
			}
		}
	}

	checkGeneration := func() error {
		if o.generation != nil && o.generation() != initialGeneration {
			return fmt.Errorf("%w: generation changed", ErrFileChanged)
		}
		switch s := any(r).(type) {
		case sizeInt:
			if s.Size() != size {
				return fmt.Errorf("%w: source size changed", ErrFileChanged)
			}
		case sizeUint:
			if s.Size() != uint64(size) {
				return fmt.Errorf("%w: source size changed", ErrFileChanged)
			}
		}
		if o.guard != nil {
			if err := o.guard(); err != nil {
				return err
			}
		}
		return nil
	}

	var fileCheck func() error
	if f, ok := any(r).(*os.File); ok {
		check, checkErr := makeFileCheck(f, size)
		if checkErr != nil {
			return nil, checkErr
		}
		fileCheck = check
	}
	if fileCheck != nil {
		explicitGuard := checkGeneration
		checkGeneration = func() error {
			if err := fileCheck(); err != nil {
				return err
			}
			return explicitGuard()
		}
	}

	life := &sourceLife{check: checkGeneration}
	parser := parser{
		r:      r,
		size:   uint64(size),
		check:  life.verify,
		active: life,
	}
	model, err := parser.parse()
	if err != nil {
		return nil, err
	}
	if err := life.verify(); err != nil {
		return nil, err
	}
	model.life = life
	return model, nil
}

func isNilReaderAt(r io.ReaderAt) bool {
	if r == nil {
		return true
	}
	v := reflect.ValueOf(r)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}

// ParseReaderAt is an alias for Parse.
func ParseReaderAt(r io.ReaderAt, size int64, opts ...Option) (*Model, error) {
	return Parse(r, size, opts...)
}

// Open opens path once, parses the immutable file it identifies, and binds
// tensor views to that file object. Reads never reopen the path. If the path
// is replaced, or the file is truncated or modified, subsequent reads fail
// with ErrFileChanged.
func Open(path string) (*Model, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		_ = f.Close()
		return nil, fmt.Errorf("%w: source is not a regular file", ErrInvalidContainer)
	}
	if fi.Size() < 0 {
		_ = f.Close()
		return nil, fmt.Errorf("%w: negative file size", ErrInvalidContainer)
	}

	pathInfo, err := os.Stat(path)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if !os.SameFile(fi, pathInfo) {
		_ = f.Close()
		return nil, fmt.Errorf("%w: path changed while opening", ErrFileChanged)
	}

	initialSize := fi.Size()
	fileCheck, err := makeFileCheck(f, initialSize)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	fileState := &sourceLife{
		check:   fileCheck,
		closeFn: f.Close,
	}
	fileState.closed.Store(false)

	if err := fileState.verify(); err != nil {
		_ = f.Close()
		return nil, err
	}

	parser := parser{
		r:      f,
		size:   uint64(initialSize),
		check:  fileState.verify,
		active: fileState,
	}
	model, err := parser.parse()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if err := fileState.verify(); err != nil {
		_ = f.Close()
		return nil, err
	}
	model.life = fileState
	return model, nil
}

// ParseFile is an alias for Open.
func ParseFile(path string) (*Model, error) {
	return Open(path)
}

// OpenFile is an alias for Open.
func OpenFile(path string) (*Model, error) {
	return Open(path)
}

// LoadFile is an alias for Open.
func LoadFile(path string) (*Model, error) {
	return Open(path)
}

func makeFileCheck(f *os.File, expectedSize int64) (func() error, error) {
	fi, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("%w: descriptor stat: %v", ErrFileChanged, err)
	}
	if fi.Size() != expectedSize {
		return nil, fmt.Errorf("%w: descriptor size changed during parse setup", ErrFileChanged)
	}

	name := f.Name()
	if name == "" {
		modTime := fi.ModTime()
		return func() error {
			current, err := f.Stat()
			if err != nil {
				return fmt.Errorf("%w: descriptor stat: %v", ErrFileChanged, err)
			}
			if current.Size() != expectedSize || !current.ModTime().Equal(modTime) {
				return fmt.Errorf("%w: descriptor was truncated or modified", ErrFileChanged)
			}
			return nil
		}, nil
	}

	path, err := filepath.Abs(name)
	if err != nil {
		return nil, fmt.Errorf("%w: resolve path: %v", ErrFileChanged, err)
	}
	pathInfo, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("%w: path stat: %v", ErrFileChanged, err)
	}
	if !os.SameFile(fi, pathInfo) {
		return nil, fmt.Errorf("%w: path changed during parse setup", ErrFileChanged)
	}
	modTime := fi.ModTime()

	return func() error {
		currentFile, err := f.Stat()
		if err != nil {
			return fmt.Errorf("%w: descriptor stat: %v", ErrFileChanged, err)
		}
		if currentFile.Size() != expectedSize || !currentFile.ModTime().Equal(modTime) {
			return fmt.Errorf("%w: descriptor was truncated or modified", ErrFileChanged)
		}
		currentPath, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("%w: path stat: %v", ErrFileChanged, err)
		}
		if !os.SameFile(fi, currentPath) || currentPath.Size() != expectedSize ||
			!currentPath.ModTime().Equal(modTime) {
			return fmt.Errorf("%w: path was replaced or modified", ErrFileChanged)
		}
		return nil
	}, nil
}

type parser struct {
	r      io.ReaderAt
	size   uint64
	check  func() error
	active *sourceLife
}

func (p *parser) readAt(pb []byte, off uint64) error {
	if err := p.check(); err != nil {
		return err
	}
	if off > uint64(^uint64(0)>>1) {
		return fmt.Errorf("%w: offset does not fit int64", ErrInvalidOffsets)
	}
	n, err := readAtLeast(p.r, pb, int64(off))
	if err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return fmt.Errorf("%w: %w at offset %d", ErrShortRead, err, off)
		}
		return err
	}
	if n != len(pb) {
		return fmt.Errorf("%w at offset %d: read %d of %d bytes", ErrShortRead, off, n, len(pb))
	}
	return p.check()
}

func (p *parser) parse() (*Model, error) {
	if p.size < headerSize {
		return nil, fmt.Errorf("%w: file is shorter than the header", ErrShortRead)
	}
	var header [8]byte
	if err := p.readAt(header[:], 0); err != nil {
		return nil, err
	}
	metaLen := binary.LittleEndian.Uint64(header[:])

	if metaLen > p.size-headerSize {
		return nil, fmt.Errorf("%w: metadata length %d exceeds file size %d",
			ErrInvalidContainer, metaLen, p.size)
	}
	if metaLen > MaxMetadataBytes {
		return nil, fmt.Errorf("%w: metadata length %d exceeds limit %d",
			ErrInvalidContainer, metaLen, MaxMetadataBytes)
	}
	if metaLen > uint64(^uint(0)>>1) {
		return nil, fmt.Errorf("%w: metadata length does not fit memory", ErrInvalidContainer)
	}

	dataStart := headerSize + metaLen // safe after the comparison above.
	metadata := make([]byte, int(metaLen))
	if err := p.readAt(metadata, headerSize); err != nil {
		return nil, err
	}
	if !utf8.Valid(metadata) {
		return nil, fmt.Errorf("%w: metadata is not valid UTF-8", ErrInvalidContainer)
	}

	value, err := decodeStrictJSON(metadata)
	if err != nil {
		return nil, err
	}
	top, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: metadata root must be an object", ErrInvalidContainer)
	}

	names := make([]string, 0, len(top))
	for name := range top {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		return bytes.Compare([]byte(names[i]), []byte(names[j])) < 0
	})

	tensors := make(map[string]*Tensor, len(top))
	type interval struct {
		name       string
		begin, end uint64
	}
	nonEmpty := make([]interval, 0, len(top))

	for _, name := range names {
		t, err := parseTensor(name, top[name])
		if err != nil {
			return nil, err
		}
		if t.DataOffsets[0] > t.DataOffsets[1] {
			return nil, fmt.Errorf("%w: tensor %q has begin > end", ErrInvalidOffsets, name)
		}

		dataBytes := p.size - dataStart
		if t.DataOffsets[0] > dataBytes || t.DataOffsets[1] > dataBytes {
			return nil, fmt.Errorf("%w: tensor %q range [%d,%d) is outside the %d-byte data region",
				ErrInvalidOffsets, name, t.DataOffsets[0], t.DataOffsets[1], dataBytes)
		}
		absBegin, ok := addNoWrap(dataStart, t.DataOffsets[0])
		if !ok {
			return nil, fmt.Errorf("%w: tensor %q absolute begin overflows", ErrInvalidOffsets, name)
		}
		absEnd, ok := addNoWrap(dataStart, t.DataOffsets[1])
		if !ok || absEnd > p.size {
			return nil, fmt.Errorf("%w: tensor %q absolute end is outside the file", ErrInvalidOffsets, name)
		}
		if absEnd < absBegin {
			return nil, fmt.Errorf("%w: tensor %q absolute range wraps", ErrInvalidOffsets, name)
		}
		span := t.DataOffsets[1] - t.DataOffsets[0]
		if t.byteLength == 0 {
			if span != 0 {
				return nil, fmt.Errorf("%w: tensor %q is empty but its range is not empty", ErrInvalidOffsets, name)
			}
		} else if span != t.byteLength {
			return nil, fmt.Errorf("%w: tensor %q range length %d != %d bytes",
				ErrInvalidOffsets, name, span, t.byteLength)
		}

		tensors[name] = t
		if span != 0 {
			nonEmpty = append(nonEmpty, interval{name: name, begin: t.DataOffsets[0], end: t.DataOffsets[1]})
		}
	}

	sort.Slice(nonEmpty, func(i, j int) bool {
		if nonEmpty[i].begin != nonEmpty[j].begin {
			return nonEmpty[i].begin < nonEmpty[j].begin
		}
		return nonEmpty[i].end < nonEmpty[j].end
	})
	for i := 1; i < len(nonEmpty); i++ {
		if nonEmpty[i].begin < nonEmpty[i-1].end {
			return nil, fmt.Errorf("%w: tensors %q and %q both own part of [%d,%d)",
				ErrOverlap, nonEmpty[i-1].name, nonEmpty[i].name,
				nonEmpty[i].begin, minUint64(nonEmpty[i-1].end, nonEmpty[i].end))
		}
	}

	canonical, err := canonicalMetadata(names, tensors)
	if err != nil {
		return nil, err
	}
	manifest := sha256.Sum256(canonical)
	model := &Model{
		Manifest:  manifest,
		Tensors:   tensors,
		canonical: canonical,
	}

	for _, name := range names {
		t := tensors[name]
		t.manifest = manifest
		t.data = &tensorData{
			t:        t,
			raw:      p.r,
			absStart: dataStart + t.DataOffsets[0],
			length:   t.byteLength,
			life:     p.active,
			manifest: manifest,
		}
		t.Data = t.data
	}
	return model, nil
}

func decodeStrictJSON(metadata []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(metadata))
	dec.UseNumber()

	value, err := decodeValue(dec, 0)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		if err != nil {
			return nil, fmt.Errorf("%w: trailing metadata: %v", ErrInvalidContainer, err)
		}
		return nil, fmt.Errorf("%w: trailing JSON value in metadata", ErrInvalidContainer)
	}
	return value, nil
}

func decodeValue(dec *json.Decoder, depth int) (any, error) {
	if depth > 8 {
		return nil, fmt.Errorf("%w: JSON nesting is too deep", ErrInvalidContainer)
	}
	tok, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidContainer, err)
	}
	switch v := tok.(type) {
	case json.Delim:
		switch v {
		case '{':
			obj := make(map[string]any)
			for dec.More() {
				keyTok, err := dec.Token()
				if err != nil {
					return nil, fmt.Errorf("%w: object key: %v", ErrInvalidContainer, err)
				}
				key, ok := keyTok.(string)
				if !ok {
					return nil, fmt.Errorf("%w: object key is not a string", ErrInvalidContainer)
				}
				if !utf8.ValidString(key) {
					return nil, fmt.Errorf("%w: object key is not valid UTF-8", ErrInvalidContainer)
				}
				if _, duplicate := obj[key]; duplicate {
					return nil, fmt.Errorf("%w: %q", ErrDuplicateKey, key)
				}
				child, err := decodeValue(dec, depth+1)
				if err != nil {
					return nil, err
				}
				obj[key] = child
			}
			end, err := dec.Token()
			if err != nil || end != json.Delim('}') {
				return nil, fmt.Errorf("%w: object is not terminated", ErrInvalidContainer)
			}
			return obj, nil
		case '[':
			arr := make([]any, 0)
			for dec.More() {
				child, err := decodeValue(dec, depth+1)
				if err != nil {
					return nil, err
				}
				arr = append(arr, child)
			}
			end, err := dec.Token()
			if err != nil || end != json.Delim(']') {
				return nil, fmt.Errorf("%w: array is not terminated", ErrInvalidContainer)
			}
			return arr, nil
		default:
			return nil, fmt.Errorf("%w: unexpected delimiter %q", ErrInvalidContainer, v)
		}
	case json.Number:
		return parseJSONUint(v.String())
	case string:
		if !utf8.ValidString(v) {
			return nil, fmt.Errorf("%w: string is not valid UTF-8", ErrInvalidContainer)
		}
		return v, nil
	case bool, nil:
		return v, nil
	default:
		return nil, fmt.Errorf("%w: unsupported JSON token", ErrInvalidContainer)
	}
}

func parseJSONUint(s string) (uint64, error) {
	if s == "" {
		return 0, fmt.Errorf("%w: empty number", ErrInvalidNumber)
	}
	if s[0] == '-' || bytes.ContainsAny([]byte(s), ".eE") {
		return 0, fmt.Errorf("%w: %q is not a non-negative integer", ErrInvalidNumber, s)
	}
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %q", ErrInvalidNumber, s)
	}
	return v, nil
}

func parseTensor(name string, raw any) (*Tensor, error) {
	obj, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: tensor %q is not an object", ErrInvalidContainer, name)
	}
	if len(obj) != 3 {
		return nil, fmt.Errorf("%w: tensor %q must contain exactly dtype, shape and data_offsets",
			ErrInvalidContainer, name)
	}

	dtypeRaw, ok := obj["dtype"]
	if !ok {
		return nil, fmt.Errorf("%w: tensor %q lacks dtype", ErrInvalidContainer, name)
	}
	dtypeString, ok := dtypeRaw.(string)
	if !ok {
		return nil, fmt.Errorf("%w: tensor %q dtype is not a string", ErrInvalidDType, name)
	}
	dtype := DType(dtypeString)
	if _, ok := dtype.ByteWidth(); !ok {
		return nil, fmt.Errorf("%w: tensor %q has unsupported dtype %q", ErrInvalidDType, name, dtypeString)
	}

	shapeRaw, ok := obj["shape"]
	if !ok {
		return nil, fmt.Errorf("%w: tensor %q lacks shape", ErrInvalidShape, name)
	}
	shapeArr, ok := shapeRaw.([]any)
	if !ok || len(shapeArr) < 1 || len(shapeArr) > 8 {
		return nil, fmt.Errorf("%w: tensor %q shape must be an array of 1 to 8 dimensions", ErrInvalidShape, name)
	}
	shape := make([]uint64, len(shapeArr))
	for i, dimRaw := range shapeArr {
		dim, ok := dimRaw.(uint64)
		if !ok {
			return nil, fmt.Errorf("%w: tensor %q shape[%d] is not a uint64", ErrInvalidShape, name, i)
		}
		shape[i] = dim
	}

	offsetsRaw, ok := obj["data_offsets"]
	if !ok {
		return nil, fmt.Errorf("%w: tensor %q lacks data_offsets", ErrInvalidOffsets, name)
	}
	offsetsArr, ok := offsetsRaw.([]any)
	if !ok || len(offsetsArr) != 2 {
		return nil, fmt.Errorf("%w: tensor %q data_offsets must have two entries", ErrInvalidOffsets, name)
	}
	var offsets [2]uint64
	for i, offRaw := range offsetsArr {
		off, ok := offRaw.(uint64)
		if !ok {
			return nil, fmt.Errorf("%w: tensor %q data_offsets[%d] is not a uint64", ErrInvalidOffsets, name, i)
		}
		offsets[i] = off
	}
	if offsets[0] > offsets[1] {
		return nil, fmt.Errorf("%w: tensor %q begin > end", ErrInvalidOffsets, name)
	}

	byteLength, err := tensorByteLength(dtype, shape)
	if err != nil {
		if errors.Is(err, ErrIntegerOverflow) {
			return nil, fmt.Errorf("%w: %w: tensor %q: %v", ErrInvalidOffsets, ErrIntegerOverflow, name, err)
		}
		return nil, fmt.Errorf("%w: tensor %q: %v", ErrInvalidOffsets, name, err)
	}
	return &Tensor{
		Name:        name,
		DType:       dtype,
		Shape:       shape,
		DataOffsets: offsets,
		byteLength:  byteLength,
	}, nil
}

func tensorByteLength(dtype DType, shape []uint64) (uint64, error) {
	width, ok := dtype.ByteWidth()
	if !ok {
		return 0, fmt.Errorf("unsupported dtype %q", dtype)
	}
	for _, dim := range shape {
		if dim == 0 {
			return 0, nil
		}
	}
	count, ok := elementCount(shape)
	if !ok {
		return 0, fmt.Errorf("%w: element count does not fit uint64", ErrIntegerOverflow)
	}
	hi, lo := bits.Mul64(count, width)
	if hi != 0 {
		return 0, fmt.Errorf("%w: byte length does not fit uint64", ErrIntegerOverflow)
	}
	return lo, nil
}

func elementCount(shape []uint64) (uint64, bool) {
	for _, dim := range shape {
		if dim == 0 {
			return 0, true
		}
	}
	count := uint64(1)
	for _, dim := range shape {
		hi, lo := bits.Mul64(count, dim)
		if hi != 0 {
			return 0, false
		}
		count = lo
	}
	return count, true
}

func canonicalMetadata(names []string, tensors map[string]*Tensor) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, name := range names {
		if i > 0 {
			buf.WriteByte(',')
		}
		key, err := json.Marshal(name)
		if err != nil {
			return nil, fmt.Errorf("%w: encode tensor name: %v", ErrInvalidContainer, err)
		}
		buf.Write(key)
		buf.WriteByte(':')
		t := tensors[name]
		buf.WriteString(`{"dtype":`)
		dtype, err := json.Marshal(string(t.DType))
		if err != nil {
			return nil, fmt.Errorf("%w: encode dtype: %v", ErrInvalidContainer, err)
		}
		buf.Write(dtype)
		buf.WriteString(`,"shape":[`)
		for j, dim := range t.Shape {
			if j > 0 {
				buf.WriteByte(',')
			}
			buf.WriteString(strconv.FormatUint(dim, 10))
		}
		buf.WriteString(`],"data_offsets":[`)
		buf.WriteString(strconv.FormatUint(t.DataOffsets[0], 10))
		buf.WriteByte(',')
		buf.WriteString(strconv.FormatUint(t.DataOffsets[1], 10))
		buf.WriteString(`]}`)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

func addNoWrap(a, b uint64) (uint64, bool) {
	sum, carry := bits.Add64(a, b, 0)
	return sum, carry == 0
}

func minUint64(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}
