package netsec

import (
	"archive/zip"
	"bufio"
	"compress/flate"
	"encoding/binary"
	"hash"
	"hash/crc32"
	"io"
	"io/fs"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	localHeaderSignature     = 0x04034b50
	centralHeaderSignature   = 0x02014b50
	endOfCentralSignature    = 0x06054b50
	zip64EndSignature        = 0x06064b50
	zip64LocatorSignature    = 0x07064b50
	localHeaderLen           = 30
	centralHeaderLen         = 46
	endOfCentralLen          = 22
	zip64EndLen              = 56
	zip64LocatorLen          = 20
	zipVersion45             = 45
	generalPurposeEncrypted  = 0x1
	generalPurposeDescriptor = 0x8
)

var (
	ErrFormat       = zip.ErrFormat
	ErrChecksum     = zip.ErrChecksum
	ErrAlgorithm    = zip.ErrAlgorithm
	ErrInsecurePath = zip.ErrInsecurePath

	// ErrZIP64Mismatch is returned when the local and central ZIP64 sizes
	// contradict one another. It is an ErrFormat so callers can handle it
	// exactly like any other malformed archive.
	ErrZIP64Mismatch = ErrFormat
)

type FileHeader = zip.FileHeader

const (
	Store   = zip.Store
	Deflate = zip.Deflate
)

type Decompressor = zip.Decompressor

type registry struct {
	mu sync.RWMutex
	m  map[uint16]Decompressor
}

var decompressors = registry{m: map[uint16]Decompressor{
	Store:   func(r io.Reader) io.ReadCloser { return io.NopCloser(r) },
	Deflate: func(r io.Reader) io.ReadCloser { return flate.NewReader(r) },
}}

func RegisterDecompressor(method uint16, dcomp Decompressor) {
	decompressors.mu.Lock()
	defer decompressors.mu.Unlock()
	decompressors.m[method] = dcomp
}

type centralEntry struct {
	extra          []byte
	compressed32   uint32
	uncompressed32 uint32
	compressed64   uint64
	uncompressed64 uint64
	offset32       uint32
	offset64       int64
	disk32         uint16
	crc32          uint32
}

// File is a single entry in a ZIP archive.
type File struct {
	zip.FileHeader

	r            io.ReaderAt
	headerOffset int64
	bodyOffset   int64
	central      centralEntry
	local        *localHeader
}

// HeaderOffset returns the offset of the entry's local file header.
func (f *File) HeaderOffset() int64 {
	return f.headerOffset
}

// DataOffset returns the offset of the entry's possibly-compressed data.
func (f *File) DataOffset() (int64, error) {
	if f.local == nil {
		h, err := readLocalHeader(f.r, f.headerOffset)
		if err != nil {
			return 0, err
		}
		f.local = h
		f.bodyOffset = h.bodyOffset
	}
	return f.headerOffset + f.bodyOffset, nil
}

// Open returns a reader for the uncompressed file contents. The reader verifies
// the declared uncompressed size and CRC-32 as it reaches EOF.
func (f *File) Open() (io.ReadCloser, error) {
	if f.Flags&generalPurposeEncrypted != 0 {
		return nil, ErrAlgorithm
	}
	offset, err := f.DataOffset()
	if err != nil {
		return nil, err
	}
	if strings.HasSuffix(f.Name, "/") {
		if f.UncompressedSize64 != 0 {
			return &dirReader{ErrFormat}, nil
		}
		return &dirReader{io.EOF}, nil
	}
	decompressors.mu.RLock()
	dcomp := decompressors.m[f.Method]
	decompressors.mu.RUnlock()
	if dcomp == nil {
		return nil, ErrAlgorithm
	}
	section := io.NewSectionReader(f.r, offset, int64(f.CompressedSize64))
	return &checksumReader{
		rc:   dcomp(section),
		hash: crc32.NewIEEE(),
		f:    f,
	}, nil
}

// OpenRaw returns the compressed file contents without decompression.
func (f *File) OpenRaw() (io.Reader, error) {
	offset, err := f.DataOffset()
	if err != nil {
		return nil, err
	}
	return io.NewSectionReader(f.r, offset, int64(f.CompressedSize64)), nil
}

type dirReader struct {
	err error
}

func (r *dirReader) Read([]byte) (int, error) { return 0, r.err }
func (r *dirReader) Close() error             { return nil }

type checksumReader struct {
	rc    io.ReadCloser
	hash  hash.Hash32
	nread uint64
	f     *File
	err   error
}

func (r *checksumReader) Stat() (fs.FileInfo, error) {
	return r.f.FileHeader.FileInfo(), nil
}

func (r *checksumReader) Read(p []byte) (int, error) {
	if r.err != nil {
		return 0, r.err
	}
	n, err := r.rc.Read(p)
	if n > 0 {
		r.hash.Write(p[:n])
		r.nread += uint64(n)
		if r.nread > r.f.UncompressedSize64 {
			r.err = ErrFormat
			return 0, r.err
		}
	}
	if err == nil {
		return n, nil
	}
	if err == io.EOF {
		if r.nread != r.f.UncompressedSize64 {
			r.err = io.ErrUnexpectedEOF
		} else if r.hash.Sum32() != r.f.CRC32 {
			r.err = ErrChecksum
		} else {
			r.err = io.EOF
		}
	} else {
		r.err = err
	}
	return n, r.err
}

func (r *checksumReader) Close() error { return r.rc.Close() }

// Reader provides access to the entries in a ZIP archive.
type Reader struct {
	r          io.ReaderAt
	size       int64
	File       []*File
	Comment    string
	baseOffset int64
}

// ReadCloser is a Reader backed by a file that must be closed.
type ReadCloser struct {
	f *os.File
	Reader
}

func (rc *ReadCloser) Close() error {
	if rc.f == nil {
		return nil
	}
	return rc.f.Close()
}

// OpenReader opens a ZIP archive on disk.
func OpenReader(name string) (*ReadCloser, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	zr, err := NewReader(f, info.Size())
	if err != nil {
		f.Close()
		return nil, err
	}
	return &ReadCloser{f: f, Reader: *zr}, nil
}

// NewReader reads a ZIP archive from r. The archive must be at most size bytes.
// NewReader validates every local header against the central directory before
// returning, so a ZIP64 size disagreement is reported as ErrFormat.
func NewReader(r io.ReaderAt, size int64) (*Reader, error) {
	if size < endOfCentralLen {
		return nil, ErrFormat
	}
	zr := &Reader{r: r, size: size}
	if err := zr.readDirectory(); err != nil {
		return nil, err
	}
	return zr, nil
}

// Files returns the entries in the archive.
func (r *Reader) Files() []*File {
	return r.File
}

// Lookup returns the first entry with the given name.
func (r *Reader) Lookup(name string) (*File, error) {
	for _, f := range r.File {
		if f.Name == name {
			return f, nil
		}
	}
	return nil, fs.ErrNotExist
}

// Open opens the named entry.
func (r *Reader) Open(name string) (io.ReadCloser, error) {
	f, err := r.Lookup(name)
	if err != nil {
		return nil, err
	}
	return f.Open()
}

type directoryEnd struct {
	diskNumber    uint16
	directoryDisk uint16
	recordsOnDisk uint16
	records       uint16
	directorySize uint64
	directoryOff  uint64
	comment       string
	endOffset     int64
}

func (r *Reader) readDirectory() error {
	end, err := r.findDirectoryEnd()
	if err != nil {
		return err
	}
	directoryOff := end.directoryOff
	directorySize := end.directorySize
	if directoryOff == uint64(uint32max) || directorySize == uint64(uint32max) ||
		end.records == uint16max || end.directoryDisk == uint16max {
		z64, zerr := r.readZIP64DirectoryEnd(end.endOffset)
		if zerr != nil {
			return zerr
		}
		if z64.records > 0 {
			end.records = uint16(z64.records)
		} else {
			end.records = uint16(uint16max)
		}
		directoryOff = z64.directoryOff
		directorySize = z64.directorySize
		end.endOffset = z64.endOffset
	}
	if directorySize > uint64(r.size) || directoryOff > uint64(r.size)-directorySize {
		return ErrFormat
	}
	r.baseOffset = end.endOffset - int64(directorySize) - int64(directoryOff)
	if r.baseOffset < 0 {
		return ErrFormat
	}
	r.Comment = end.comment

	section := io.NewSectionReader(r.r, r.baseOffset+int64(directoryOff), int64(directorySize))
	buf := bufio.NewReader(section)
	for {
		f := &File{r: r.r}
		cerr := readCentralHeader(f, buf)
		if cerr == ErrFormat || cerr == io.ErrUnexpectedEOF || cerr == io.EOF {
			break
		}
		if cerr != nil {
			return cerr
		}
		f.headerOffset = int64(f.central.offset64) + r.baseOffset
		if err := r.validateLocal(f); err != nil {
			return err
		}
		r.File = append(r.File, f)
	}
	if uint16(len(r.File)) != end.records && end.records != uint16max {
		return ErrFormat
	}
	return nil
}

func (r *Reader) findDirectoryEnd() (*directoryEnd, error) {
	start := r.size - endOfCentralLen - uint16max
	if start < 0 {
		start = 0
	}
	tail := make([]byte, r.size-start)
	if _, err := r.r.ReadAt(tail, start); err != nil && err != io.EOF {
		return nil, err
	}
	for i := len(tail) - endOfCentralLen; i >= 0; i-- {
		if binary.LittleEndian.Uint32(tail[i:i+4]) != endOfCentralSignature {
			continue
		}
		b := tail[i : i+endOfCentralLen]
		commentLen := int(binary.LittleEndian.Uint16(b[20:22]))
		if i+endOfCentralLen+commentLen > len(tail) {
			continue
		}
		e := &directoryEnd{
			diskNumber:    binary.LittleEndian.Uint16(b[4:6]),
			directoryDisk: binary.LittleEndian.Uint16(b[6:8]),
			recordsOnDisk: binary.LittleEndian.Uint16(b[8:10]),
			records:       binary.LittleEndian.Uint16(b[10:12]),
			directorySize: uint64(binary.LittleEndian.Uint32(b[12:16])),
			directoryOff:  uint64(binary.LittleEndian.Uint32(b[16:20])),
			comment:       string(tail[i+endOfCentralLen : i+endOfCentralLen+commentLen]),
			endOffset:     start + int64(i),
		}
		return e, nil
	}
	return nil, ErrFormat
}

type zip64DirectoryEnd struct {
	records       uint64
	directorySize uint64
	directoryOff  uint64
	endOffset     int64
}

func (r *Reader) readZIP64DirectoryEnd(endOffset int64) (*zip64DirectoryEnd, error) {
	locOffset := endOffset - zip64LocatorLen
	if locOffset < 0 {
		return nil, ErrFormat
	}
	var loc [zip64LocatorLen]byte
	if _, err := r.r.ReadAt(loc[:], locOffset); err != nil {
		return nil, err
	}
	if binary.LittleEndian.Uint32(loc[0:4]) != zip64LocatorSignature {
		return nil, ErrFormat
	}
	z64Offset := int64(binary.LittleEndian.Uint64(loc[8:16]))
	if z64Offset < 0 || z64Offset > r.size-zip64EndLen {
		return nil, ErrFormat
	}
	var fixed [zip64EndLen]byte
	if _, err := r.r.ReadAt(fixed[:], z64Offset); err != nil {
		return nil, err
	}
	if binary.LittleEndian.Uint32(fixed[0:4]) != zip64EndSignature {
		return nil, ErrFormat
	}
	return &zip64DirectoryEnd{
		records:       binary.LittleEndian.Uint64(fixed[32:40]),
		directorySize: binary.LittleEndian.Uint64(fixed[40:48]),
		directoryOff:  binary.LittleEndian.Uint64(fixed[48:56]),
		endOffset:     z64Offset,
	}, nil
}

func readCentralHeader(f *File, rd io.Reader) error {
	var fixed [centralHeaderLen]byte
	if _, err := io.ReadFull(rd, fixed[:]); err != nil {
		return err
	}
	if binary.LittleEndian.Uint32(fixed[0:4]) != centralHeaderSignature {
		return ErrFormat
	}
	f.CreatorVersion = binary.LittleEndian.Uint16(fixed[4:6])
	f.ReaderVersion = binary.LittleEndian.Uint16(fixed[6:8])
	f.Flags = binary.LittleEndian.Uint16(fixed[8:10])
	f.Method = binary.LittleEndian.Uint16(fixed[10:12])
	f.ModifiedTime = binary.LittleEndian.Uint16(fixed[12:14])
	f.ModifiedDate = binary.LittleEndian.Uint16(fixed[14:16])
	f.CRC32 = binary.LittleEndian.Uint32(fixed[16:20])
	f.central.crc32 = f.CRC32
	f.central.compressed32 = binary.LittleEndian.Uint32(fixed[20:24])
	f.central.uncompressed32 = binary.LittleEndian.Uint32(fixed[24:28])
	f.central.compressed64 = uint64(f.central.compressed32)
	f.central.uncompressed64 = uint64(f.central.uncompressed32)
	nameLen := int(binary.LittleEndian.Uint16(fixed[28:30]))
	extraLen := int(binary.LittleEndian.Uint16(fixed[30:32]))
	commentLen := int(binary.LittleEndian.Uint16(fixed[32:34]))
	f.central.disk32 = binary.LittleEndian.Uint16(fixed[34:36])
	f.ExternalAttrs = binary.LittleEndian.Uint32(fixed[38:42])
	f.central.offset32 = binary.LittleEndian.Uint32(fixed[42:46])
	f.central.offset64 = int64(f.central.offset32)
	payload := make([]byte, nameLen+extraLen+commentLen)
	if _, err := io.ReadFull(rd, payload); err != nil {
		return err
	}
	f.Name = string(payload[:nameLen])
	f.central.extra = payload[nameLen : nameLen+extraLen]
	f.Extra = f.central.extra
	f.Comment = string(payload[nameLen+extraLen:])
	f.Modified = msDOSTime(f.ModifiedDate, f.ModifiedTime)
	detectUTF8(f)
	return nil
}

func (r *Reader) validateLocal(f *File) error {
	h, err := readLocalHeader(r.r, f.headerOffset)
	if err != nil {
		return err
	}
	f.local = h
	f.bodyOffset = h.bodyOffset
	if string(h.name) != f.Name {
		return ErrFormat
	}
	if h.method != f.Method {
		return ErrFormat
	}
	if err := resolveSizes(f); err != nil {
		return err
	}
	f.CompressedSize = f.central.compressed32
	f.UncompressedSize = f.central.uncompressed32
	f.CompressedSize64 = f.central.compressed64
	f.UncompressedSize64 = f.central.uncompressed64
	return nil
}

func detectUTF8(f *File) {
	valid1 := utf8.ValidString(f.Name)
	valid2 := utf8.ValidString(f.Comment)
	switch {
	case !valid1 || !valid2:
		f.NonUTF8 = true
	case f.Flags&0x800 == 0:
		f.NonUTF8 = false
	default:
		f.NonUTF8 = false
	}
}

func msDOSTime(date, tm uint16) time.Time {
	if date == 0 && tm == 0 {
		return time.Time{}
	}
	year := int(date>>9) + 1980
	month := time.Month((date >> 5) & 0xf)
	day := int(date & 0x1f)
	if month < 1 || month > 12 || day < 1 || day > 31 {
		return time.Time{}
	}
	hour := int(tm >> 11)
	minute := int((tm >> 5) & 0x3f)
	sec := int(tm&0x1f) * 2
	if hour > 23 || minute > 59 || sec > 59 {
		return time.Time{}
	}
	return time.Date(year, month, day, hour, minute, sec, 0, time.UTC)
}
