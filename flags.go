package netsec

// PathFlags mirrors wasi:filesystem/types.path-flags.
type PathFlags uint32

const (
	// PathFlagSymlinkFollow requests that a symlink in the final path
	// segment be expanded. Intermediate symlinks are always expanded.
	PathFlagSymlinkFollow PathFlags = 1 << 0
)

// OpenFlags mirrors wasi:filesystem/types.open-flags.
type OpenFlags uint32

const (
	OpenCreate    OpenFlags = 1 << 0
	OpenDirectory OpenFlags = 1 << 1
	OpenExclusive OpenFlags = 1 << 2
	OpenTruncate  OpenFlags = 1 << 3
)

// DescriptorFlags mirrors wasi:filesystem/types.descriptor-flags and acts
// as the capability (rights) set carried by a descriptor.
type DescriptorFlags uint64

const (
	FlagRead              DescriptorFlags = 1 << 0
	FlagWrite             DescriptorFlags = 1 << 1
	FlagFileIntegritySync DescriptorFlags = 1 << 2
	FlagDataIntegritySync DescriptorFlags = 1 << 3
	FlagRequestedWrite    DescriptorFlags = 1 << 4
	FlagMutateDirectory   DescriptorFlags = 1 << 5
)
