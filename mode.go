package oci

import "io/fs"

// POSIX file mode constants. They intentionally use the historical octal
// values found in tar headers and Linux inodes.
const (
	ModeSetUID uint32 = 0o4000
	ModeSetGID uint32 = 0o2000
	ModeSticky uint32 = 0o1000

	ModeTypeMask uint32 = 0o170000
	ModePermMask uint32 = 0o7777

	ModeFIFO    uint32 = 0o010000
	ModeChar    uint32 = 0o020000
	ModeDir     uint32 = 0o040000
	ModeBlock   uint32 = 0o060000
	ModeRegular uint32 = 0o100000
	ModeSymlink uint32 = 0o120000
	ModeSocket  uint32 = 0o140000
)

// EntryType is the normalized OCI/tar entry type.
type EntryType string

const (
	TypeRegular   EntryType = "regular"
	TypeDirectory EntryType = "directory"
	TypeSymlink   EntryType = "symlink"
	TypeHardlink  EntryType = "hardlink"
	TypeChar      EntryType = "char-device"
	TypeBlock     EntryType = "block-device"
	TypeFIFO      EntryType = "fifo"
	TypeSocket    EntryType = "socket"
)

// NormalizeMode converts POSIX, tar, and Go os/fs mode representations to a
// full POSIX mode word. The returned word contains type, setuid/setgid,
// sticky, and permission bits.
func NormalizeMode(mode uint32, typ EntryType) uint32 {
	// A value outside the low 16 bits is almost certainly a Go fs.FileMode
	// value rather than a POSIX st_mode word.
	if mode&^0xffff != 0 || fs.FileMode(mode)&(fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky) != 0 {
		fm := fs.FileMode(mode)
		posix := uint32(fm.Perm())
		if fm&fs.ModeSetuid != 0 {
			posix |= ModeSetUID
		}
		if fm&fs.ModeSetgid != 0 {
			posix |= ModeSetGID
		}
		if fm&fs.ModeSticky != 0 {
			posix |= ModeSticky
		}
		switch {
		case fm&fs.ModeDir != 0:
			posix |= ModeDir
		case fm&fs.ModeSymlink != 0:
			posix |= ModeSymlink
		case fm&fs.ModeNamedPipe != 0:
			posix |= ModeFIFO
		case fm&fs.ModeSocket != 0:
			posix |= ModeSocket
		case fm&fs.ModeDevice != 0 && fm&fs.ModeCharDevice != 0:
			posix |= ModeChar
		case fm&fs.ModeDevice != 0:
			posix |= ModeBlock
		default:
			posix |= ModeRegular
		}
		return posix
	}

	if mode&ModeTypeMask != 0 {
		return mode
	}

	out := mode & (ModeSetUID | ModeSetGID | ModeSticky | ModePermMask)
	switch string(typ) {
	case string(TypeDirectory), "dir", "DIR":
		out |= ModeDir
	case string(TypeSymlink), "softlink", "lnk":
		out |= ModeSymlink
	case string(TypeChar), "char", "chardev":
		out |= ModeChar
	case string(TypeBlock), "block", "blockdev":
		out |= ModeBlock
	case string(TypeFIFO), "pipe":
		out |= ModeFIFO
	case string(TypeSocket), "sock":
		out |= ModeSocket
	case string(TypeHardlink), "link", "h":
		// A hard link is another directory entry for a regular inode until
		// its target is resolved and proves otherwise.
		out |= ModeRegular
	default:
		out |= ModeRegular
	}
	return out
}

// ModeType returns the POSIX object type bits.
func ModeType(mode uint32) uint32 { return mode & ModeTypeMask }

// IsSetUID reports the POSIX set-user-ID bit.
func IsSetUID(mode uint32) bool { return mode&ModeSetUID != 0 }

// IsSetGID reports the POSIX set-group-ID bit.
func IsSetGID(mode uint32) bool { return mode&ModeSetGID != 0 }

// OSMode converts a POSIX mode word to fs.FileMode.
func OSMode(mode uint32) fs.FileMode {
	fm := fs.FileMode(mode & ModePermMask)
	if mode&ModeSetUID != 0 {
		fm |= fs.ModeSetuid
	}
	if mode&ModeSetGID != 0 {
		fm |= fs.ModeSetgid
	}
	if mode&ModeSticky != 0 {
		fm |= fs.ModeSticky
	}
	switch mode & ModeTypeMask {
	case ModeDir:
		fm |= fs.ModeDir
	case ModeSymlink:
		fm |= fs.ModeSymlink
	case ModeFIFO:
		fm |= fs.ModeNamedPipe
	case ModeSocket:
		fm |= fs.ModeSocket
	case ModeChar:
		fm |= fs.ModeDevice | fs.ModeCharDevice
	case ModeBlock:
		fm |= fs.ModeDevice
	}
	return fm
}
