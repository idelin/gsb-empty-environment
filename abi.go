package netsec

import (
	"strconv"
	"strings"
)

// ABI is a Landlock kernel ABI version. Landlock ABIs are cumulative: every
// ABI supports all filesystem access rights introduced by earlier ABIs.
type ABI int

const (
	// ABI1 is the initial Landlock ABI, available since Linux 5.13.
	ABI1 ABI = 1
	// ABI2 is available since Linux 5.19 and adds the REFER right.
	ABI2 ABI = 2
	// ABI3 is available since Linux 6.2 and adds the TRUNCATE right.
	ABI3 ABI = 3
	// ABI4 is available since Linux 6.5 and adds no filesystem access right.
	ABI4 ABI = 4
	// ABI5 is available since Linux 6.7 and adds NET-related access rights,
	// which are outside the scope of this filesystem-focused package.
	ABI5 ABI = 5
	// ABI6 is available since Linux 6.10 and adds IOCTL_DEV.
	ABI6 ABI = 6
)

// AccessFS is a bitmask of Landlock filesystem access rights. The numeric
// values mirror include/uapi/linux/landlock.h so masks can be compared with
// the kernel UAPI one to one.
type AccessFS uint64

const (
	// AccessFSExecute matches LANDLOCK_ACCESS_FS_EXECUTE (ABI 1).
	AccessFSExecute AccessFS = 1 << 0
	// AccessFSWriteFile matches LANDLOCK_ACCESS_FS_WRITE_FILE (ABI 1).
	AccessFSWriteFile AccessFS = 1 << 1
	// AccessFSReadFile matches LANDLOCK_ACCESS_FS_READ_FILE (ABI 1).
	AccessFSReadFile AccessFS = 1 << 2
	// AccessFSReadDir matches LANDLOCK_ACCESS_FS_READ_DIR (ABI 1).
	AccessFSReadDir AccessFS = 1 << 3
	// AccessFSRemoveDir matches LANDLOCK_ACCESS_FS_REMOVE_DIR (ABI 1).
	AccessFSRemoveDir AccessFS = 1 << 4
	// AccessFSRemoveFile matches LANDLOCK_ACCESS_FS_REMOVE_FILE (ABI 1).
	AccessFSRemoveFile AccessFS = 1 << 5
	// AccessFSMakeChar matches LANDLOCK_ACCESS_FS_MAKE_CHAR (ABI 1).
	AccessFSMakeChar AccessFS = 1 << 6
	// AccessFSMakeDir matches LANDLOCK_ACCESS_FS_MAKE_DIR (ABI 1).
	AccessFSMakeDir AccessFS = 1 << 7
	// AccessFSMakeReg matches LANDLOCK_ACCESS_FS_MAKE_REG (ABI 1).
	AccessFSMakeReg AccessFS = 1 << 8
	// AccessFSMakeSock matches LANDLOCK_ACCESS_FS_MAKE_SOCK (ABI 1).
	AccessFSMakeSock AccessFS = 1 << 9
	// AccessFSMakeFifo matches LANDLOCK_ACCESS_FS_MAKE_FIFO (ABI 1).
	AccessFSMakeFifo AccessFS = 1 << 10
	// AccessFSMakeBlock matches LANDLOCK_ACCESS_FS_MAKE_BLOCK (ABI 1).
	AccessFSMakeBlock AccessFS = 1 << 11
	// AccessFSMakeSym matches LANDLOCK_ACCESS_FS_MAKE_SYM (ABI 1).
	AccessFSMakeSym AccessFS = 1 << 12
	// AccessFSRefer matches LANDLOCK_ACCESS_FS_REFER (ABI 2).
	AccessFSRefer AccessFS = 1 << 13
	// AccessFSTruncate matches LANDLOCK_ACCESS_FS_TRUNCATE (ABI 3).
	AccessFSTruncate AccessFS = 1 << 14
	// AccessFSIoctlDev matches LANDLOCK_ACCESS_FS_IOCTL_DEV (ABI 6).
	AccessFSIoctlDev AccessFS = 1 << 15
)

// accessFSNames lists each named right in bit order for String.
var accessFSNames = []struct {
	mask AccessFS
	name string
}{
	{AccessFSExecute, "EXECUTE"},
	{AccessFSWriteFile, "WRITE_FILE"},
	{AccessFSReadFile, "READ_FILE"},
	{AccessFSReadDir, "READ_DIR"},
	{AccessFSRemoveDir, "REMOVE_DIR"},
	{AccessFSRemoveFile, "REMOVE_FILE"},
	{AccessFSMakeChar, "MAKE_CHAR"},
	{AccessFSMakeDir, "MAKE_DIR"},
	{AccessFSMakeReg, "MAKE_REG"},
	{AccessFSMakeSock, "MAKE_SOCK"},
	{AccessFSMakeFifo, "MAKE_FIFO"},
	{AccessFSMakeBlock, "MAKE_BLOCK"},
	{AccessFSMakeSym, "MAKE_SYM"},
	{AccessFSRefer, "REFER"},
	{AccessFSTruncate, "TRUNCATE"},
	{AccessFSIoctlDev, "IOCTL_DEV"},
}

// String renders the named bits in a mask, joined with "|". Unknown bits are
// rendered in hexadecimal.
func (a AccessFS) String() string {
	var parts []string
	for _, entry := range accessFSNames {
		if a&entry.mask != 0 {
			parts = append(parts, entry.name)
			a &^= entry.mask
		}
	}
	if a != 0 {
		parts = append(parts, "0x"+strconv.FormatUint(uint64(a), 16))
	}
	if len(parts) == 0 {
		return "0"
	}
	return strings.Join(parts, "|")
}

// Contains reports whether a includes every right in other.
func (a AccessFS) Contains(other AccessFS) bool {
	return a&other == other
}

// SupportedAccessFS returns the filesystem access rights the ABI understands.
// Rights outside this mask cannot be governed by the target kernel and must
// be rejected rather than silently dropped.
func (abi ABI) SupportedAccessFS() AccessFS {
	switch abi {
	case ABI1:
		return AccessFSExecute |
			AccessFSWriteFile |
			AccessFSReadFile |
			AccessFSReadDir |
			AccessFSRemoveDir |
			AccessFSRemoveFile |
			AccessFSMakeChar |
			AccessFSMakeDir |
			AccessFSMakeReg |
			AccessFSMakeSock |
			AccessFSMakeFifo |
			AccessFSMakeBlock |
			AccessFSMakeSym
	case ABI2:
		return ABI1.SupportedAccessFS() | AccessFSRefer
	case ABI3, ABI4, ABI5:
		return ABI2.SupportedAccessFS() | AccessFSTruncate
	case ABI6:
		return ABI5.SupportedAccessFS() | AccessFSIoctlDev
	default:
		return 0
	}
}

// String returns the canonical ABI name.
func (abi ABI) String() string {
	switch abi {
	case ABI1:
		return "Landlock ABI 1 (Linux 5.13)"
	case ABI2:
		return "Landlock ABI 2 (Linux 5.19)"
	case ABI3:
		return "Landlock ABI 3 (Linux 6.2)"
	case ABI4:
		return "Landlock ABI 4 (Linux 6.5)"
	case ABI5:
		return "Landlock ABI 5 (Linux 6.7)"
	case ABI6:
		return "Landlock ABI 6 (Linux 6.10)"
	default:
		return "unknown Landlock ABI"
	}
}
