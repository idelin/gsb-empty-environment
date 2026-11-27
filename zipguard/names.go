package zipguard

import (
	"fmt"
	"io/fs"
	"strings"
)

// normalizeName produces the collision key for an entry name: backslashes are
// treated as path separators and ASCII letters are case folded.
func normalizeName(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	var b strings.Builder
	b.Grow(len(name))
	for _, r := range name {
		if r >= 'A' && r <= 'Z' {
			r += 'a' - 'A'
		}
		b.WriteRune(r)
	}
	return b.String()
}

// validateName rejects absolute paths, drive letters, UNC paths and any
// parent-directory traversal segment after separator normalization.
func validateName(raw string) (string, bool, error) {
	if raw == "" {
		return "", false, fmt.Errorf("%w: empty entry name", ErrUnsafeName)
	}
	norm := normalizeName(raw)

	// Drive letter and UNC/rooted forms, expressed through either separator.
	if len(norm) >= 2 && norm[1] == ':' && (norm[0] >= 'a' && norm[0] <= 'z') {
		return "", false, fmt.Errorf("%w: drive-letter path %q", ErrUnsafeName, raw)
	}
	if strings.HasPrefix(norm, "//") {
		return "", false, fmt.Errorf("%w: UNC path %q", ErrUnsafeName, raw)
	}
	if strings.HasPrefix(norm, "/") {
		return "", false, fmt.Errorf("%w: absolute path %q", ErrUnsafeName, raw)
	}

	isDir := strings.HasSuffix(norm, "/")
	segments := strings.Split(norm, "/")
	for _, seg := range segments {
		if seg == ".." {
			return "", false, fmt.Errorf("%w: parent traversal in %q", ErrUnsafeName, raw)
		}
	}
	return norm, isDir, nil
}

// validateMode inspects the external attributes. Non-Unix host systems are
// not treated as carrying Unix modes; a zero high word is a plain regular
// file.
func validateMode(c *cdh, p Policy) (fs.FileMode, error) {
	// Traditional Unix mode_t type bits as stored in the high 16 bits of a
	// ZIP external attributes field. Go's fs.FileMode uses different bit
	// positions, so detection happens on the raw values and the result is
	// translated afterwards.
	const (
		sIfmt   uint16 = 0xF000
		sIfreg  uint16 = 0x8000
		sIfdir  uint16 = 0x4000
		sIfchr  uint16 = 0x2000
		sIfblk  uint16 = 0x6000
		sIffifo uint16 = 0x1000
		sIflnk  uint16 = 0xA000
		sIfsock uint16 = 0xC000
		sIsuid  uint16 = 0o4000
		sIsgid  uint16 = 0o2000
	)

	high := uint16(c.externalAttrs >> 16)

	// Host system 3 is Unix; OS X (19) and some JVM variants also encode
	// Unix modes. When the high word is zero there is no mode information.
	hasUnixMode := high != 0 && (c.hostSystem == 3 || c.hostSystem == 19)
	if high != 0 && !hasUnixMode {
		// Unknown host with non-zero mode bits: fail closed rather than
		// guessing.
		return 0, fmt.Errorf("%w: %q non-unix host %d with mode bits %#o", ErrUnsafeMode, c.name, c.hostSystem, high)
	}
	if !hasUnixMode {
		return 0, nil
	}

	if high&(sIsuid|sIsgid) != 0 && p.RejectSetID {
		return 0, fmt.Errorf("%w: %q carries SUID/SGID bits %#o", ErrUnsafeMode, c.name, high)
	}

	var mode fs.FileMode
	mode |= fs.FileMode(high & 0o777)
	if high&sIsuid != 0 {
		mode |= fs.ModeSetuid
	}
	if high&sIsgid != 0 {
		mode |= fs.ModeSetgid
	}

	switch high & sIfmt {
	case 0, sIfreg:
		// regular file: no type bits
	case sIfdir:
		mode |= fs.ModeDir
	case sIflnk:
		mode |= fs.ModeSymlink
		if p.RejectSymlink {
			return 0, fmt.Errorf("%w: %q is a symbolic link", ErrUnsafeMode, c.name)
		}
	case sIfblk:
		mode |= fs.ModeDevice
		if p.RejectSpecial {
			return 0, fmt.Errorf("%w: %q block device mode %#o", ErrUnsafeMode, c.name, high)
		}
	case sIfchr:
		mode |= fs.ModeDevice | fs.ModeCharDevice
		if p.RejectSpecial {
			return 0, fmt.Errorf("%w: %q character device mode %#o", ErrUnsafeMode, c.name, high)
		}
	case sIffifo:
		mode |= fs.ModeNamedPipe
		if p.RejectSpecial {
			return 0, fmt.Errorf("%w: %q fifo mode %#o", ErrUnsafeMode, c.name, high)
		}
	case sIfsock:
		mode |= fs.ModeSocket
		if p.RejectSpecial {
			return 0, fmt.Errorf("%w: %q socket mode %#o", ErrUnsafeMode, c.name, high)
		}
	default:
		if p.RejectSpecial {
			return 0, fmt.Errorf("%w: %q unknown file type %#o", ErrUnsafeMode, c.name, high)
		}
	}
	return mode, nil
}
