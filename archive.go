// Package netsec provides bounded reading of ZIP archives.
//
// ZIP entries carry an uncompressed size supplied by the archive itself. That
// value is untrusted. A safe reader must therefore check the declared size and
// the declared compression ratio before handing the entry to any decompressor,
// and it must independently bound the bytes produced by that decompressor.
package netsec

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultMaxEntrySize is the per-entry uncompressed-size cap used when the
	// caller does not provide one.
	DefaultMaxEntrySize int64 = 128 << 20

	// DefaultMaxTotalSize is the archive-wide uncompressed-size cap used when
	// the caller does not provide one.
	DefaultMaxTotalSize int64 = 512 << 20

	// DefaultMaxCompressionRatio is the maximum uncompressed-to-compressed
	// ratio used when the caller does not provide one.
	DefaultMaxCompressionRatio = 100.0
)

var (
	// ErrDeclaredSizeExceeded reports an entry whose size in the ZIP metadata
	// is larger than the configured absolute limit.
	ErrDeclaredSizeExceeded = errors.New("netsec: declared uncompressed size exceeds limit")

	// ErrTotalSizeExceeded reports archives whose sum of declared sizes is
	// larger than the configured archive-wide limit.
	ErrTotalSizeExceeded = errors.New("netsec: total declared size exceeds limit")

	// ErrCompressionRatioExceeded reports an entry whose declared or observed
	// expansion ratio is larger than the caller's limit.
	ErrCompressionRatioExceeded = errors.New("netsec: compression ratio exceeds limit")

	// ErrOutputSizeExceeded reports output that grew past the size declared by
	// the archive or past the configured absolute limit.
	ErrOutputSizeExceeded = errors.New("netsec: decompressed output exceeds limit")

	// ErrEntryCountExceeded reports an archive with too many entries.
	ErrEntryCountExceeded = errors.New("netsec: entry count exceeds limit")

	// ErrDecoder reports a registered decompressor that did not return a
	// reader.
	ErrDecoder = errors.New("netsec: decompressor returned nil")
)

// Limits describes the caller's resource policy. A positive entry, total, or
// ratio value replaces the corresponding default. Zero values select safe
// defaults rather than disabling the check.
//
// MaxEntrySize, MaxTotalSize, and MaxCompressionRatio are the canonical
// fields. The additional names make the policy convenient to express in terms
// of bytes read, output size, or archive size.
type Limits struct {
	MaxEntrySize        int64
	MaxTotalSize        int64
	MaxCompressionRatio float64
	MaxEntries          int

	// Aliases for MaxEntrySize.
	MaxUncompressedSize int64
	MaxOutputSize       int64
	MaxSize             int64

	// Aliases for MaxTotalSize.
	MaxArchiveSize           int64
	MaxTotalUncompressedSize int64

	// Aliases for MaxCompressionRatio.
	MaxRatio         float64
	CompressionRatio float64
	Ratio            float64
}

// ArchiveLimits is an alias for Limits.
type ArchiveLimits = Limits

// Options is an alias for Limits.
type Options = Limits

// DefaultLimits returns a Limits value populated with the package defaults.
func DefaultLimits() Limits {
	return Limits{
		MaxEntrySize:        DefaultMaxEntrySize,
		MaxTotalSize:        DefaultMaxTotalSize,
		MaxCompressionRatio: DefaultMaxCompressionRatio,
	}
}

// DefaultArchiveLimits is an alias for DefaultLimits.
func DefaultArchiveLimits() Limits {
	return DefaultLimits()
}

type normalizedLimits struct {
	maxEntry   int64
	maxTotal   int64
	maxRatio   float64
	maxEntries int
}

func (l Limits) normalize() normalizedLimits {
	n := normalizedLimits{
		maxEntry: DefaultMaxEntrySize,
		maxTotal: DefaultMaxTotalSize,
		maxRatio: DefaultMaxCompressionRatio,
	}

	for _, value := range []int64{
		l.MaxEntrySize,
		l.MaxUncompressedSize,
		l.MaxOutputSize,
		l.MaxSize,
	} {
		if value > 0 {
			if n.maxEntry == DefaultMaxEntrySize || value < n.maxEntry {
				n.maxEntry = value
			}
		}
	}
	for _, value := range []int64{
		l.MaxTotalSize,
		l.MaxArchiveSize,
		l.MaxTotalUncompressedSize,
	} {
		if value > 0 {
			if n.maxTotal == DefaultMaxTotalSize || value < n.maxTotal {
				n.maxTotal = value
			}
		}
	}
	for _, value := range []float64{
		l.MaxCompressionRatio,
		l.MaxRatio,
		l.CompressionRatio,
		l.Ratio,
	} {
		if value > 0 {
			if n.maxRatio == DefaultMaxCompressionRatio || value < n.maxRatio {
				n.maxRatio = value
			}
		}
	}
	if l.MaxEntries > 0 {
		n.maxEntries = l.MaxEntries
	}
	return n
}

// Decompressor is the same function shape used by archive/zip. A decoder can
// be supplied for a method that is not handled by the standard library.
type Decompressor = zip.Decompressor

// Decoder is an alias for Decompressor.
type Decoder = Decompressor

// DecoderFunc is an alias for Decompressor.
type DecoderFunc = Decompressor

// Reader is a bounded view of a ZIP archive.
//
// File, Files, and Entries all refer to the same guarded entries. File is kept
// for familiarity with archive/zip; the guarded Open method makes it safe to
// iterate over it.
type Reader struct {
	File    []*File
	Files   []*File
	Entries []*File
	Comment string

	limits        normalizedLimits
	decompressors map[uint16]Decompressor
	closer        io.Closer
	mu            sync.RWMutex
}

// Archive is an alias for Reader.
type Archive = Reader

// File is a single ZIP entry with bounded accessors.
type File struct {
	Name               string
	Comment            string
	Method             uint16
	Modified           time.Time
	UncompressedSize64 uint64
	CompressedSize64   uint64
	CRC32              uint32
	NonUTF8            bool
	ExternalAttrs      uint32

	zf    *zip.File
	owner *Reader
}

// Info is an immutable description of an archive entry.
type Info struct {
	Name               string
	Method             uint16
	Modified           time.Time
	UncompressedSize64 uint64
	CompressedSize64   uint64
	IsDir              bool
}

// NewReader opens an archive after validating every entry's declared size and
// compression ratio. No decompressor is called until a file is opened.
func NewReader(r io.ReaderAt, size int64, limits Limits) (*Reader, error) {
	return newReader(r, size, limits, nil)
}

// NewReaderWithLimits is an alias for NewReader.
func NewReaderWithLimits(r io.ReaderAt, size int64, limits Limits) (*Reader, error) {
	return NewReader(r, size, limits)
}

// NewReaderWithDecompressors is like NewReader but installs caller-provided
// decoders for the given ZIP methods. Decoders are only invoked after the
// entry metadata has passed the size and ratio checks.
func NewReaderWithDecompressors(r io.ReaderAt, size int64, limits Limits, decompressors map[uint16]Decompressor) (*Reader, error) {
	return newReader(r, size, limits, decompressors)
}

// OpenReader opens a ZIP file from disk using safe defaults and the supplied
// policy.
func OpenReader(path string, limits Limits) (*Reader, error) {
	r, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	stat, err := r.Stat()
	if err != nil {
		r.Close()
		return nil, err
	}
	limitReader, err := NewReader(r, stat.Size(), limits)
	if err != nil {
		r.Close()
		return nil, err
	}
	limitReader.closer = r
	return limitReader, nil
}

// Open is an alias for OpenReader.
func Open(path string, limits Limits) (*Reader, error) {
	return OpenReader(path, limits)
}

// OpenArchive is an alias for OpenReader.
func OpenArchive(path string, limits Limits) (*Reader, error) {
	return OpenReader(path, limits)
}

// NewArchive is an alias for NewReader.
func NewArchive(r io.ReaderAt, size int64, limits Limits) (*Reader, error) {
	return NewReader(r, size, limits)
}

// NewArchiveReader is an alias for NewReader.
func NewArchiveReader(r io.ReaderAt, size int64, limits Limits) (*Reader, error) {
	return NewReader(r, size, limits)
}

// OpenZipReader is an alias for OpenReader.
func OpenZipReader(path string, limits Limits) (*Reader, error) {
	return OpenReader(path, limits)
}

// ValidateArchive checks all entry metadata without opening a decompressor.
func ValidateArchive(r io.ReaderAt, size int64, limits Limits) error {
	_, err := NewReader(r, size, limits)
	return err
}

// ReadArchiveEntry opens an archive and reads one named entry under the
// supplied limits.
func ReadArchiveEntry(r io.ReaderAt, size int64, name string, limits Limits) ([]byte, error) {
	reader, err := NewReader(r, size, limits)
	if err != nil {
		return nil, err
	}
	return reader.ReadFile(name)
}

// ExtractArchive opens an archive and reads every non-directory entry under
// the supplied limits. The returned map is keyed by the entry's archive name.
func ExtractArchive(r io.ReaderAt, size int64, limits Limits) (map[string][]byte, error) {
	reader, err := NewReader(r, size, limits)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]byte, len(reader.File))
	for _, file := range reader.File {
		if file.IsDir() {
			continue
		}
		data, err := file.ReadAll()
		if err != nil {
			return nil, err
		}
		out[file.Name] = data
	}
	return out, nil
}

// ExtractZip is an alias for ExtractArchive.
func ExtractZip(r io.ReaderAt, size int64, limits Limits) (map[string][]byte, error) {
	return ExtractArchive(r, size, limits)
}

func newReader(r io.ReaderAt, size int64, limits Limits, decompressors map[uint16]Decompressor) (*Reader, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, err
	}
	n := limits.normalize()
	if n.maxEntries > 0 && len(zr.File) > n.maxEntries {
		return nil, fmt.Errorf("%w: %d entries, limit %d", ErrEntryCountExceeded, len(zr.File), n.maxEntries)
	}

	reader := &Reader{
		Comment:       zr.Comment,
		limits:        n,
		decompressors: make(map[uint16]Decompressor, len(decompressors)+2),
	}
	for method, decoder := range decompressors {
		if decoder != nil {
			reader.decompressors[method] = decoder
		}
	}

	var declaredTotal uint64
	files := make([]*File, 0, len(zr.File))
	for _, zf := range zr.File {
		declared := zf.UncompressedSize64
		compressed := zf.CompressedSize64

		if err := validateDeclaredEntry(zf.Name, declared, compressed, n); err != nil {
			return nil, err
		}
		offset, err := zf.DataOffset()
		if err != nil {
			return nil, err
		}
		if offset < 0 || uint64(offset) > uint64(size) || compressed > uint64(size)-uint64(offset) {
			return nil, fmt.Errorf("%w: %q compressed data exceeds archive size", zip.ErrFormat, zf.Name)
		}
		if declared > ^uint64(0)-declaredTotal {
			return nil, fmt.Errorf("%w: size overflow in %q", ErrTotalSizeExceeded, zf.Name)
		}
		declaredTotal += declared
		if n.maxTotal > 0 && declaredTotal > uint64(n.maxTotal) {
			return nil, fmt.Errorf("%w: %d bytes, limit %d", ErrTotalSizeExceeded, declaredTotal, n.maxTotal)
		}

		file := &File{
			Name:               zf.Name,
			Comment:            zf.Comment,
			Method:             zf.Method,
			Modified:           zf.Modified,
			UncompressedSize64: declared,
			CompressedSize64:   compressed,
			CRC32:              zf.CRC32,
			NonUTF8:            zf.NonUTF8,
			ExternalAttrs:      zf.ExternalAttrs,
			zf:                 zf,
			owner:              reader,
		}
		files = append(files, file)
	}
	reader.File = files
	reader.Files = files
	reader.Entries = files
	return reader, nil
}

func validateDeclaredEntry(name string, declared, compressed uint64, limits normalizedLimits) error {
	if declared > uint64(limits.maxEntry) {
		return fmt.Errorf("%w: %q declares %d bytes, limit %d", ErrDeclaredSizeExceeded, name, declared, limits.maxEntry)
	}
	if declared == 0 {
		return nil
	}
	if compressed == 0 {
		return fmt.Errorf("%w: %q declares %d bytes with no compressed data", ErrCompressionRatioExceeded, name, declared)
	}
	ratio := float64(declared) / float64(compressed)
	if ratio > limits.maxRatio {
		return fmt.Errorf("%w: %q declares %.2f:1, limit %.2f:1", ErrCompressionRatioExceeded, name, ratio, limits.maxRatio)
	}
	return nil
}

// RegisterDecompressor installs a decoder for one ZIP method. The decoder is
// passed the raw compressed payload only after metadata validation succeeds.
func (r *Reader) RegisterDecompressor(method uint16, decoder Decompressor) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.decompressors == nil {
		r.decompressors = make(map[uint16]Decompressor)
	}
	r.decompressors[method] = decoder
}

// ReadFile reads a named entry with all applicable limits enforced.
func (r *Reader) ReadFile(name string) ([]byte, error) {
	file := r.Find(name)
	if file == nil {
		return nil, fmt.Errorf("netsec: %q not found", name)
	}
	return file.ReadAll()
}

// Find returns the first entry with the exact name.
func (r *Reader) Find(name string) *File {
	for _, file := range r.File {
		if file.Name == name {
			return file
		}
	}
	return nil
}

// FileInfo returns the ZIP file's FileInfo.
func (f *File) FileInfo() os.FileInfo {
	return f.zf.FileInfo()
}

// Mode returns the entry's mode bits.
func (f *File) Mode() os.FileMode {
	return f.zf.Mode()
}

// IsDir reports whether the entry is a directory.
func (f *File) IsDir() bool {
	return f.zf.FileInfo().IsDir()
}

// DataOffset is not exposed by archive/zip. Use OpenRaw for the compressed
// payload or Open for the bounded decompressed stream.

// OpenRaw returns the compressed payload. It does not decompress data and is
// subject to the entry's metadata checks.
func (f *File) OpenRaw() (io.Reader, error) {
	if err := validateDeclaredEntry(f.Name, f.UncompressedSize64, f.CompressedSize64, f.owner.limits); err != nil {
		return nil, err
	}
	return f.zf.OpenRaw()
}

// Open returns a reader whose output is bounded by the archive's declared size
// and by the caller's absolute limit. Exceeding either bound returns an error;
// data is never silently truncated.
func (f *File) Open() (io.ReadCloser, error) {
	if err := validateDeclaredEntry(f.Name, f.UncompressedSize64, f.CompressedSize64, f.owner.limits); err != nil {
		return nil, err
	}

	decoder := f.owner.decompressor(f.Method)
	var (
		src    io.ReadCloser
		err    error
		custom bool
	)
	if decoder != nil {
		raw, rawErr := f.zf.OpenRaw()
		if rawErr != nil {
			return nil, rawErr
		}
		src = decoder(raw)
		if src == nil {
			return nil, ErrDecoder
		}
		custom = true
	} else {
		src, err = f.zf.Open()
		if err != nil {
			return nil, err
		}
	}

	maxOutput := int64(f.UncompressedSize64)
	if maxOutput > f.owner.limits.maxEntry {
		maxOutput = f.owner.limits.maxEntry
	}
	ratioMax := int64(0)
	if f.CompressedSize64 > 0 && f.owner.limits.maxRatio > 0 {
		computed := math.Ceil(float64(f.CompressedSize64) * f.owner.limits.maxRatio)
		if computed >= float64(maxInt64) {
			ratioMax = maxInt64
		} else {
			ratioMax = int64(computed)
		}
		if ratioMax < maxOutput {
			maxOutput = ratioMax
		}
	}
	return &boundedReader{
		src:      src,
		remain:   maxOutput,
		expected: int64(f.UncompressedSize64),
		strict:   custom,
		errLimit: fmt.Errorf(
			"%w: %q declared %d bytes", ErrOutputSizeExceeded, f.Name, f.UncompressedSize64,
		),
	}, nil
}

const maxInt64 = int64(^uint64(0) >> 1)

func (r *Reader) decompressor(method uint16) Decompressor {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.decompressors[method]
}

// ReadAll reads an entry and returns its fully checked contents.
func (f *File) ReadAll() ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// Extract writes one entry to w while applying the same bounds as ReadAll.
func (f *File) Extract(w io.Writer) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	_, err = io.Copy(w, rc)
	return err
}

// ExtractAll writes all non-directory entries beneath dir.
func (r *Reader) ExtractAll(dir string, perm os.FileMode) error {
	for _, file := range r.File {
		if file.IsDir() {
			continue
		}
		target, err := safeJoin(dir, file.Name)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepathDir(target), 0o755); err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
		if err != nil {
			return err
		}
		if err := file.Extract(out); err != nil {
			out.Close()
			return err
		}
		if err := out.Close(); err != nil {
			return err
		}
	}
	return nil
}

// FilesByMethod returns entries using a ZIP compression method. It is useful
// for auditing which decoder will handle an archive.
func (r *Reader) FilesByMethod(method uint16) []*File {
	var files []*File
	for _, file := range r.File {
		if file.Method == method {
			files = append(files, file)
		}
	}
	return files
}

// SupportedMethods returns the methods the reader can currently decode.
func (r *Reader) SupportedMethods() []uint16 {
	methods := []uint16{zip.Store, zip.Deflate}
	r.mu.RLock()
	for method := range r.decompressors {
		methods = append(methods, method)
	}
	r.mu.RUnlock()
	sort.Slice(methods, func(i, j int) bool { return methods[i] < methods[j] })
	return methods
}

// Close closes the underlying file when the Reader was created by OpenReader.
func (r *Reader) Close() error {
	if r.closer == nil {
		return nil
	}
	return r.closer.Close()
}

func safeJoin(dir, name string) (string, error) {
	relative := filepath.FromSlash(name)
	if filepath.IsAbs(relative) {
		return "", fmt.Errorf("netsec: absolute entry path %q", name)
	}
	cleanDir := filepath.Clean(dir)
	target := filepath.Join(cleanDir, relative)
	prefix := cleanDir + string(filepath.Separator)
	if target != cleanDir && !strings.HasPrefix(target, prefix) {
		return "", fmt.Errorf("netsec: entry path escapes destination: %q", name)
	}
	return target, nil
}

func filepathDir(path string) string {
	return filepath.Dir(path)
}

type boundedReader struct {
	src      io.ReadCloser
	remain   int64
	errLimit error
	expected int64
	strict   bool
	count    int64
	done     bool
}

func (r *boundedReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	if r.remain <= 0 {
		var probe [1]byte
		n, err := r.src.Read(probe[:])
		if n > 0 {
			r.done = true
			return 0, r.errLimit
		}
		if err == nil {
			return 0, nil
		}
		if err == io.EOF {
			if r.strict && r.count != r.expected {
				r.done = true
				return 0, r.errLimit
			}
			r.done = true
		}
		return 0, err
	}
	if int64(len(p)) > r.remain {
		p = p[:r.remain]
	}
	n, err := r.src.Read(p)
	r.remain -= int64(n)
	r.count += int64(n)
	if err == io.EOF {
		if r.strict && r.count != r.expected {
			r.done = true
			return n, r.errLimit
		}
		r.done = true
	}
	return n, err
}

func (r *boundedReader) Close() error {
	return r.src.Close()
}
