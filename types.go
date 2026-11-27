package oci

import (
	"archive/tar"
	"io"
	"io/fs"
	"time"
)

// Entry is one normalized layer entry. The Xattrs field accepts either
// map[string][]byte or map[string]string; use XAttrs as an alternate spelling.
type Entry struct {
	Path     string `json:"path,omitempty"`
	Name     string `json:"name,omitempty"`
	Filename string `json:"filename,omitempty"`

	// Type accepts EntryType, a string name, or a tar typeflag byte.
	Type     any  `json:"type,omitempty"`
	Typeflag byte `json:"typeflag,omitempty"`

	// Mode accepts a POSIX uint32/int/octal value or fs.FileMode.
	Mode     any         `json:"mode,omitempty"`
	FileMode fs.FileMode `json:"file_mode,omitempty"`

	UID   int    `json:"uid,omitempty"`
	Uid   int    `json:"Uid,omitempty"` // compatibility spelling
	GID   int    `json:"gid,omitempty"`
	Gid   int    `json:"Gid,omitempty"` // compatibility spelling
	Uname string `json:"uname,omitempty"`
	User  string `json:"user,omitempty"`
	Gname string `json:"gname,omitempty"`
	Group string `json:"group,omitempty"`

	Size    int64     `json:"size,omitempty"`
	ModTime time.Time `json:"mod_time,omitempty"`

	// Linkname is the archive/tar spelling and is used for both hard links
	// and symlinks unless Target/Symlink is more specific.
	Linkname string `json:"linkname,omitempty"`
	LinkName string `json:"link_name,omitempty"`
	Target   string `json:"target,omitempty"`
	Symlink  string `json:"symlink,omitempty"`

	Xattrs    any               `json:"-"`
	XAttrs    any               `json:"-"`
	RawXattrs map[string]string `json:"raw_xattrs,omitempty"`

	Capability *CapabilitySet `json:"capability,omitempty"`
	Cap        *CapabilitySet `json:"cap,omitempty"`

	Content          []byte `json:"-"`
	Data             []byte `json:"-"`
	Body             []byte `json:"-"`
	ContentTruncated bool   `json:"content_truncated,omitempty"`

	Header *tar.Header `json:"-"`
}

// TarEntry and File are construction aliases for callers that model entries
// with those names.
type TarEntry = Entry
type File = Entry

// Layer is an ordered OCI tar layer. Entries and Files are both accepted.
type Layer struct {
	ID    string `json:"id,omitempty"`
	Name  string `json:"name,omitempty"`
	Index int    `json:"index,omitempty"`
	// Entries and Files accept []Entry, []*Entry, or map[string]Entry.
	Entries any `json:"entries,omitempty"`
	Files   any `json:"files,omitempty"`

	// Optional tar payload. Apply parses these only when Entries/Files empty.
	Tar      io.Reader `json:"-"`
	Reader   io.Reader `json:"-"`
	TarBytes []byte    `json:"-"`
	Blob     []byte    `json:"-"`
}

// Inode retains identity shared by hard links.
type Inode struct {
	ID         uint64              `json:"id"`
	Mode       uint32              `json:"mode"`
	UID        int                 `json:"uid"`
	GID        int                 `json:"gid"`
	Uname      string              `json:"uname,omitempty"`
	Gname      string              `json:"gname,omitempty"`
	Xattrs     map[string][]byte   `json:"xattrs,omitempty"`
	Capability *CapabilitySet      `json:"capability,omitempty"`
	Content    []byte              `json:"-"`
	Paths      map[string]struct{} `json:"paths,omitempty"`
	Nlink      int                 `json:"nlink"`
}

// Node is one path in a merged file system.
type Node struct {
	Path string `json:"path"`
	Name string `json:"name"`

	Type EntryType `json:"type"`
	Mode uint32    `json:"mode"`

	UID   int    `json:"uid"`
	Uid   int    `json:"Uid"`
	GID   int    `json:"gid"`
	Gid   int    `json:"Gid"`
	Uname string `json:"uname,omitempty"`
	Gname string `json:"gname,omitempty"`

	Size    int64     `json:"size,omitempty"`
	ModTime time.Time `json:"mod_time,omitempty"`

	// SymlinkTarget is the literal symlink payload.
	SymlinkTarget string `json:"symlink_target,omitempty"`
	// Target is a generic alias for symlink/hardlink payloads.
	Target string `json:"target,omitempty"`
	// Linkname is the raw tar link/symlink name.
	Linkname string `json:"linkname,omitempty"`
	// HardlinkTarget is the raw hardlink target as supplied by the layer.
	HardlinkTarget string `json:"hardlink_target,omitempty"`
	// ResolvedHardlinkTarget is the root-relative target found during merge.
	ResolvedHardlinkTarget string `json:"resolved_hardlink_target,omitempty"`

	Xattrs           map[string][]byte `json:"xattrs,omitempty"`
	Capability       *CapabilitySet    `json:"capability,omitempty"`
	CapabilityError  string            `json:"capability_error,omitempty"`
	Content          []byte            `json:"-"`
	ContentTruncated bool              `json:"content_truncated,omitempty"`

	LayerIndex int `json:"layer_index"`
	Order      int `json:"order"`

	InodeID   uint64           `json:"inode_id,omitempty"`
	Children  map[string]*Node `json:"children,omitempty"`
	LinkCount int              `json:"link_count,omitempty"`

	Opaque   bool `json:"opaque,omitempty"`
	Implicit bool `json:"implicit,omitempty"`
}

// IsDir reports whether the merged node is a directory.
func (n *Node) IsDir() bool {
	return n != nil && ModeType(n.Mode) == ModeDir || n != nil && n.Type == TypeDirectory
}

// IsRegular reports whether the node is a regular file.
func (n *Node) IsRegular() bool {
	return n != nil && (ModeType(n.Mode) == ModeRegular || n.Type == TypeRegular || n.Type == TypeHardlink)
}

// IsSymlink reports whether the node is a symbolic link.
func (n *Node) IsSymlink() bool {
	return n != nil && (ModeType(n.Mode) == ModeSymlink || n.Type == TypeSymlink)
}

// IsHardlink reports whether the node was supplied as a tar hard link.
func (n *Node) IsHardlink() bool { return n != nil && n.Type == TypeHardlink }

// SetUID reports the POSIX setuid bit.
func (n *Node) SetUID() bool { return n != nil && IsSetUID(n.Mode) }

// SetGID reports the POSIX setgid bit.
func (n *Node) SetGID() bool { return n != nil && IsSetGID(n.Mode) }

// OSMode returns the mode as fs.FileMode.
func (n *Node) OSMode() fs.FileMode { return OSMode(n.Mode) }

// ResidueReason explains why a node is no longer visible.
type ResidueReason string

const (
	ResidueWhiteout ResidueReason = "whiteout"
	ResidueOpaque   ResidueReason = "opaque-whiteout"
	ResidueReplace  ResidueReason = "type-replacement"
	ResidueOverride ResidueReason = "entry-override"
)

// Residue is a removed/lower entry retained for layer-aware security checks.
type Residue struct {
	Path         string        `json:"path"`
	Reason       ResidueReason `json:"reason"`
	LayerIndex   int           `json:"layer_index"`
	RemovedLayer int           `json:"removed_layer"`
	Whiteout     string        `json:"whiteout,omitempty"`
	Node         *Node         `json:"node,omitempty"`
}

// LinkRecord captures target resolution for a link.
type LinkRecord struct {
	Path           string `json:"path"`
	Kind           string `json:"kind"`
	RawTarget      string `json:"raw_target"`
	ResolvedTarget string `json:"resolved_target,omitempty"`
	Absolute       bool   `json:"absolute,omitempty"`
	EscapesRoot    bool   `json:"escapes_root,omitempty"`
	CrossLayer     bool   `json:"cross_layer,omitempty"`
	TargetLayer    int    `json:"target_layer,omitempty"`
	TargetHidden   bool   `json:"target_hidden,omitempty"`
	Order          int    `json:"order"`
	LayerIndex     int    `json:"layer_index"`
}

// FileSystem is a merged view after zero or more layers.
type FileSystem struct {
	Root       *Node             `json:"root"`
	Nodes      map[string]*Node  `json:"nodes"`
	Files      map[string]*Node  `json:"files"`
	Inodes     map[uint64]*Inode `json:"-"`
	Residue    []Residue         `json:"residue,omitempty"`
	Whiteouts  []Whiteout        `json:"whiteouts,omitempty"`
	Links      []LinkRecord      `json:"links,omitempty"`
	LayerCount int               `json:"layer_count"`
	// SnapshotIndex is set by MergeWithSnapshots; -1 means unspecified/final.
	SnapshotIndex int `json:"snapshot_index"`

	nextInodeID uint64
}

// MergedFS and FS are concise aliases for callers.
type MergedFS = FileSystem
type FS = FileSystem

// MergeResult contains the final rootfs and every post-layer snapshot.
type MergeResult struct {
	Final     *FileSystem   `json:"final"`
	Snapshots []*FileSystem `json:"snapshots"`
	RootFS    *FileSystem   `json:"root_fs,omitempty"`
	Merged    *FileSystem   `json:"merged,omitempty"`
	FS        *FileSystem   `json:"fs,omitempty"`
}

// Snapshot is an alias used by some layer APIs.
type Snapshot = FileSystem

// XattrMap is the preferred binary extended-attribute map type.
type XattrMap map[string][]byte
