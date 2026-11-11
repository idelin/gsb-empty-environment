package fat32

import (
	"encoding/binary"
	"fmt"
	"strings"
)

const (
	dirEntrySize  = 32
	attrLongName  = 0x0F
	attrDirectory = 0x10
	lastFreeEntry = 0x00
	deletedEntry  = 0xE5
)

// DirEntry describes one file or subdirectory found in a FAT32 directory.
type DirEntry struct {
	// Name is the space-trimmed 8.3 short name, lowercased for convenience.
	Name string
	// Cluster is the first data cluster of the entry's chain.
	Cluster uint32
	// Size is the declared data length in bytes.
	Size uint32
	// IsDirectory reports the directory attribute bit.
	IsDirectory bool
}

// RootEntry looks up an 8.3 short name (for example "EVIDENCE.TXT") in the
// root directory. The match is case-insensitive; deleted and long-name
// entries are skipped.
func (v *Volume) RootEntry(shortName string) (*DirEntry, error) {
	return v.lookup(v.rootCluster, strings.ToLower(strings.TrimSpace(shortName)))
}

// OpenRoot is a convenience wrapper that resolves shortName in the root
// directory and returns a ReaderAt view over its chain.
func (v *Volume) OpenRoot(shortName string) (*File, error) {
	entry, err := v.RootEntry(shortName)
	if err != nil {
		return nil, err
	}
	if entry.IsDirectory {
		return nil, fmt.Errorf("fat32: %s is a directory, not a file", shortName)
	}
	return v.Open(entry.Cluster, entry.Size)
}

// lookup scans the cluster chain rooted at dirCluster for an exact short-name
// match. The directory chain itself is trusted only as far as both FAT copies
// agree, via the same ReaderAt machinery used for files.
func (v *Volume) lookup(dirCluster uint32, target string) (*DirEntry, error) {
	dir, err := v.Open(dirCluster, 0)
	if err != nil {
		return nil, err
	}

	chunk := make([]byte, v.clusterBytes)
	var off int64
	for {
		n, rerr := dir.ReadAt(chunk, off)
		for base := 0; base+dirEntrySize <= n; base += dirEntrySize {
			raw := chunk[base : base+dirEntrySize]
			entry, ok := parseDirEntry(raw)
			if !ok {
				if raw[0] == lastFreeEntry {
					return nil, fmt.Errorf("fat32: %q not found", target)
				}
				continue
			}
			if entry.Name == target {
				return entry, nil
			}
		}
		if rerr != nil {
			// Any terminal condition (clean end of chain, truncation, chain
			// damage) means the target is not in the readable directory.
			return nil, fmt.Errorf("fat32: %q not found: %w", target, rerr)
		}
		off += int64(n)
	}
}

// parseDirEntry decodes one 32-byte directory record. Long-name, deleted and
// label-only records yield ok=false.
func parseDirEntry(raw []byte) (*DirEntry, bool) {
	if len(raw) < dirEntrySize {
		return nil, false
	}
	switch raw[0] {
	case lastFreeEntry, deletedEntry:
		return nil, false
	}
	if raw[11] == attrLongName {
		return nil, false
	}

	// The 11-byte field packs an 8-byte name and a 3-byte extension with no
	// separator; reconstruct the conventional "name.ext" form.
	base := strings.TrimRight(string(raw[0:8]), " ")
	ext := strings.TrimRight(string(raw[8:11]), " ")
	name := strings.ToLower(base)
	if ext != "" {
		name += "." + strings.ToLower(ext)
	}
	clusterHi := binary.LittleEndian.Uint16(raw[20:22])
	clusterLo := binary.LittleEndian.Uint16(raw[26:28])
	cluster := uint32(clusterHi)<<16 | uint32(clusterLo)

	return &DirEntry{
		Name:        name,
		Cluster:     cluster,
		Size:        binary.LittleEndian.Uint32(raw[28:32]),
		IsDirectory: raw[11]&attrDirectory != 0,
	}, true
}
