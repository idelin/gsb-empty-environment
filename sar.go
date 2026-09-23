// Package safearch implements SAR, a minimal tamper-evident archive format
// with a hardened packer and extractor.
//
// Supported serialization scope (v1): regular files and directories only.
// Symlinks, devices, fifos, sockets and other special files are rejected at
// pack time and cannot be represented in the format. Decoding never executes
// host code: entries are parsed manually from a fixed little-endian wire
// layout; no gob/encoding reflection or plugin loading is involved.
//
// Wire format (all integers little-endian):
//
//	magic      4 bytes  "SAR1"
//	version    1 byte   0x01
//	entries    repeated entry records, terminated by a single 0x00 byte
//	digest     32 bytes SHA-256 of every preceding byte
//
// File entry:
//
//	type       1 byte   1
//	nameLen    2 bytes
//	name       nameLen bytes, UTF-8, '/'-separated, already normalized
//	mode       4 bytes  permission bits only (0o000-0o777)
//	size       8 bytes
//	payload    size bytes
//	digest     32 bytes SHA-256 of payload
//
// Dir entry:
//
//	type       1 byte   2
//	nameLen    2 bytes
//	name       nameLen bytes, UTF-8, '/'-separated, already normalized
//	mode       4 bytes  permission bits only
package safearch

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"
	"unicode/utf8"
)

// Format constants.
const (
	Magic      = "SAR1"
	Version    = 1
	typEnd     = 0
	typFile    = 1
	typDir     = 2
	digestLen  = sha256.Size
	maxNameLen = 4096
	maxCount   = 1 << 22 // absolute ceiling, always checked before allocation
)

// Sentinel errors, matchable with errors.Is.
var (
	ErrBadMagic      = errors.New("safearch: bad magic")
	ErrBadVersion    = errors.New("safearch: unsupported version")
	ErrUnsafePath    = errors.New("safearch: unsafe path")
	ErrDuplicatePath = errors.New("safearch: duplicate or conflicting path")
	ErrDigest        = errors.New("safearch: digest mismatch")
	ErrSize          = errors.New("safearch: size limit exceeded")
	ErrFormat        = errors.New("safearch: malformed archive")
	ErrUnsupported   = errors.New("safearch: unsupported file type")
	ErrExists        = errors.New("safearch: output already exists")
)

// Limits caps resources consumed while decoding. Every length field is
// validated against these limits before any allocation or file creation
// based on it happens.
type Limits struct {
	MaxFileSize  int64 // per-file payload bytes
	MaxTotalSize int64 // sum of all payload bytes
	MaxEntries   int   // number of entries
}

// DefaultLimits is used when a zero Limits is passed.
var DefaultLimits = Limits{
	MaxFileSize:  1 << 30,
	MaxTotalSize: 4 << 30,
	MaxEntries:   1 << 20,
}

func (l Limits) withDefaults() Limits {
	if l.MaxFileSize <= 0 {
		l.MaxFileSize = DefaultLimits.MaxFileSize
	}
	if l.MaxTotalSize <= 0 {
		l.MaxTotalSize = DefaultLimits.MaxTotalSize
	}
	if l.MaxEntries <= 0 {
		l.MaxEntries = DefaultLimits.MaxEntries
	}
	return l
}

// Entry describes one archive member.
type Entry struct {
	Name string      // normalized relative path, '/'-separated
	Mode fs.FileMode // permission bits only
	Size int64       // payload bytes (0 for dirs)
	Dir  bool
}

// ValidatePath normalizes and validates an archive path. It rejects absolute
// paths, "." and ".." components, backslashes, drive-letter prefixes, empty
// components, non-UTF-8 and overlong names, so an entry can never escape the
// extraction root lexically.
func ValidatePath(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("%w: empty path", ErrUnsafePath)
	}
	if len(name) > maxNameLen {
		return "", fmt.Errorf("%w: path too long (%d bytes)", ErrUnsafePath, len(name))
	}
	if !utf8.ValidString(name) {
		return "", fmt.Errorf("%w: path is not valid UTF-8", ErrUnsafePath)
	}
	if strings.HasPrefix(name, "/") || strings.HasPrefix(name, `\`) {
		return "", fmt.Errorf("%w: absolute path %q", ErrUnsafePath, name)
	}
	if strings.ContainsRune(name, '\\') {
		return "", fmt.Errorf("%w: backslash in path %q", ErrUnsafePath, name)
	}
	if len(name) >= 2 && name[1] == ':' {
		return "", fmt.Errorf("%w: drive prefix in path %q", ErrUnsafePath, name)
	}
	for _, c := range strings.Split(name, "/") {
		if c == "" || c == "." || c == ".." {
			return "", fmt.Errorf("%w: bad component in path %q", ErrUnsafePath, name)
		}
	}
	return path.Clean(name), nil
}

// entrySet tracks paths already taken: duplicates are rejected and a later
// entry can never overwrite or shadow an earlier one.
type entrySet struct {
	m map[string]bool // path -> isDir
}

func newEntrySet() *entrySet { return &entrySet{m: make(map[string]bool)} }

func (s *entrySet) add(name string, isDir bool) error {
	if _, ok := s.m[name]; ok {
		return fmt.Errorf("%w: %q", ErrDuplicatePath, name)
	}
	for p := path.Dir(name); p != "." && p != ""; p = path.Dir(p) {
		if dir, ok := s.m[p]; ok && !dir {
			return fmt.Errorf("%w: %q inside file %q", ErrDuplicatePath, name, p)
		}
	}
	if isDir {
		prefix := name + "/"
		for p, dir := range s.m {
			if !dir && strings.HasPrefix(p, prefix) {
				return fmt.Errorf("%w: dir %q covers file %q", ErrDuplicatePath, name, p)
			}
		}
	}
	s.m[name] = isDir
	return nil
}
