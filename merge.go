package oci

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strconv"
	"strings"

	"archive/tar"
)

var ErrPathConflict = errors.New("path type conflict")

// NewFileSystem creates an empty merged root file system.
func NewFileSystem() *FileSystem {
	root := &Node{
		Path:       ".",
		Name:       "/",
		Type:       TypeDirectory,
		Mode:       ModeDir | 0o755,
		UID:        0,
		GID:        0,
		Uid:        0,
		Gid:        0,
		LayerIndex: -1,
		Children:   make(map[string]*Node),
	}
	fs := &FileSystem{
		Root:        root,
		Nodes:       make(map[string]*Node),
		Files:       make(map[string]*Node),
		Inodes:      make(map[uint64]*Inode),
		nextInodeID: 2,
	}
	root.InodeID = 1
	fs.Inodes[1] = &Inode{ID: 1, Mode: root.Mode, Paths: map[string]struct{}{".": {}}, Nlink: 2}
	fs.Nodes["."] = root
	fs.Files["."] = root
	return fs
}

// Merger applies ordered layers while retaining forward hard-link references.
type Merger struct {
	FS      *FileSystem
	pending []*Node
}

// NewMerger creates an empty layer merger.
func NewMerger() *Merger { return &Merger{FS: NewFileSystem()} }

// Snapshot returns a deep clone of the current merged file system.
func (m *Merger) Snapshot() *FileSystem {
	if m == nil || m.FS == nil {
		return NewFileSystem()
	}
	return m.FS.Clone()
}

// Merge composes layers from bottom to top and returns the final rootfs.
func Merge(layers ...Layer) (*FileSystem, error) {
	m := NewMerger()
	for i := range layers {
		if _, err := m.Apply(layers[i]); err != nil {
			return nil, fmt.Errorf("merge layer %d: %w", i, err)
		}
	}
	return m.FS, nil
}

// MergeLayers is the slice-argument form of Merge.
func MergeLayers(layers []Layer) (*FileSystem, error) { return Merge(layers...) }

// Compose is a descriptive alias for Merge.
func Compose(layers ...Layer) (*FileSystem, error) { return Merge(layers...) }

// MergeLayersOrdered explicitly states the required bottom-to-top order.
func MergeLayersOrdered(layers []Layer) (*FileSystem, error) { return Merge(layers...) }

// MergeWithSnapshots merges layers and returns a clone after every layer.
func MergeWithSnapshots(layers ...Layer) (*MergeResult, error) {
	m := NewMerger()
	result := &MergeResult{Snapshots: make([]*FileSystem, 0, len(layers))}
	for i := range layers {
		fs, err := m.Apply(layers[i])
		if err != nil {
			return nil, fmt.Errorf("merge layer %d: %w", i, err)
		}
		clone := fs.Clone()
		clone.SnapshotIndex = i
		result.Snapshots = append(result.Snapshots, clone)
	}
	result.Final = m.FS.Clone()
	result.Final.SnapshotIndex = len(layers) - 1
	result.RootFS = result.Final
	result.Merged = result.Final
	result.FS = result.Final
	return result, nil
}

// MergeLayersWithSnapshots is the slice-argument snapshot API.
func MergeLayersWithSnapshots(layers []Layer) (*MergeResult, error) {
	return MergeWithSnapshots(layers...)
}

// Apply merges one ordered tar layer into the current file system.
func (m *Merger) Apply(layer Layer) (*FileSystem, error) {
	if m == nil || m.FS == nil {
		m = NewMerger()
	}
	fs := m.FS
	layerIndex := fs.LayerCount
	if layer.Index > 0 && layerIndex == 0 {
		layerIndex = layer.Index
	}
	entries, err := entriesFromLayer(layer)
	if err != nil {
		return nil, fmt.Errorf("layer %s: %w", layer.Name, err)
	}

	for order := range entries {
		entry := entries[order]
		rawPath := firstNonEmpty(entry.Path, entry.Name, entry.Filename)
		if rawPath == "" {
			return nil, fmt.Errorf("layer %s entry %d has empty path", layer.Name, order)
		}

		if hasWhiteoutSegment(rawPath) {
			wh, err := ParseWhiteout(rawPath)
			if err != nil {
				return nil, fmt.Errorf("layer %s entry %d: %w", layer.Name, order, err)
			}
			wh.Layer = layerIndex
			wh.Order = order
			fs.Whiteouts = append(fs.Whiteouts, wh)
			if err := fs.applyWhiteout(wh); err != nil {
				return nil, fmt.Errorf("layer %s entry %d whiteout %q: %w", layer.Name, order, rawPath, err)
			}
			continue
		}

		clean, err := CleanPath(rawPath)
		if err != nil {
			return nil, fmt.Errorf("layer %s entry %d: %w", layer.Name, order, err)
		}

		node, err := fs.upsertEntry(entry, clean, layerIndex, order)
		if err != nil {
			return nil, fmt.Errorf("layer %s entry %d path %q: %w", layer.Name, order, rawPath, err)
		}

		if node.IsHardlink() {
			m.pending = append(m.pending, node)
		}
		m.resolvePending()
	}

	m.resolvePending()
	if err := fs.reindex(); err != nil {
		return nil, err
	}
	fs.LayerCount++
	return fs, nil
}

func (m *Merger) resolvePending() {
	if m == nil {
		return
	}
	remaining := m.pending[:0]
	for _, node := range m.pending {
		resolved, record := m.FS.resolveHardlink(node)
		if resolved {
			m.FS.Links = append(m.FS.Links, record)
		} else {
			remaining = append(remaining, node)
		}
	}
	m.pending = remaining
}

func hasWhiteoutSegment(raw string) bool {
	for _, segment := range strings.FieldsFunc(raw, func(r rune) bool { return r == '/' || r == '\\' }) {
		if strings.HasPrefix(segment, WhiteoutPrefix) {
			return true
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func entryType(e Entry) EntryType {
	if e.Type != nil {
		switch value := e.Type.(type) {
		case EntryType:
			return normalizeTypeName(string(value))
		case string:
			return normalizeTypeName(value)
		case byte:
			if typ := tarFlagType(value); typ != "" {
				return typ
			}
		case rune:
			if typ := tarFlagType(byte(value)); typ != "" {
				return typ
			}
		case int:
			if value >= 0 && value <= 255 {
				if typ := tarFlagType(byte(value)); typ != "" {
					return typ
				}
			}
		}
	}

	if e.Typeflag != 0 {
		if typ := tarFlagType(e.Typeflag); typ != "" {
			return typ
		}
	}

	mode := coerceMode(e.Mode)
	if mode&ModeTypeMask != 0 {
		switch mode & ModeTypeMask {
		case ModeDir:
			return TypeDirectory
		case ModeSymlink:
			return TypeSymlink
		case ModeChar:
			return TypeChar
		case ModeBlock:
			return TypeBlock
		case ModeFIFO:
			return TypeFIFO
		case ModeSocket:
			return TypeSocket
		}
	}

	if strings.HasSuffix(firstNonEmpty(e.Path, e.Name, e.Filename), "/") {
		return TypeDirectory
	}
	return TypeRegular
}

func normalizeTypeName(value string) EntryType {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "file", "reg", "regular", "regular-file", "regular file":
		return TypeRegular
	case "dir", "directory":
		return TypeDirectory
	case "symlink", "softlink", "lnk", "symbolic-link":
		return TypeSymlink
	case "hardlink", "hard-link", "link", "h":
		return TypeHardlink
	case "char", "chardev", "character-device":
		return TypeChar
	case "block", "blockdev", "block-device":
		return TypeBlock
	case "fifo", "pipe":
		return TypeFIFO
	case "socket", "sock":
		return TypeSocket
	default:
		return EntryType(value)
	}
}

func tarFlagType(flag byte) EntryType {
	switch flag {
	case tar.TypeDir:
		return TypeDirectory
	case tar.TypeSymlink:
		return TypeSymlink
	case tar.TypeLink:
		return TypeHardlink
	case tar.TypeChar:
		return TypeChar
	case tar.TypeBlock:
		return TypeBlock
	case tar.TypeFifo:
		return TypeFIFO
	case tar.TypeReg, tar.TypeRegA:
		return TypeRegular
	default:
		return ""
	}
}

func coerceMode(value any) uint32 {
	switch mode := value.(type) {
	case nil:
		return 0
	case uint32:
		return mode
	case uint64:
		return uint32(mode)
	case uint16:
		return uint32(mode)
	case uint8:
		return uint32(mode)
	case int:
		return uint32(mode)
	case int64:
		return uint32(mode)
	case int32:
		return uint32(mode)
	case fs.FileMode:
		return uint32(mode)
	case string:
		parsed, err := strconv.ParseUint(strings.TrimPrefix(strings.TrimSpace(mode), "0o"), 8, 32)
		if err == nil {
			return uint32(parsed)
		}
		parsed, err = strconv.ParseUint(mode, 0, 32)
		if err == nil {
			return uint32(parsed)
		}
	}
	return 0
}

func coerceXattrs(value any) map[string][]byte {
	switch attrs := value.(type) {
	case nil:
		return nil
	case map[string][]byte:
		out := make(map[string][]byte, len(attrs))
		for key, data := range attrs {
			out[key] = append([]byte(nil), data...)
		}
		return out
	case XattrMap:
		return coerceXattrs(map[string][]byte(attrs))
	case map[string]string:
		out := make(map[string][]byte, len(attrs))
		for key, data := range attrs {
			out[key] = []byte(data)
		}
		return out
	case map[string]any:
		out := make(map[string][]byte, len(attrs))
		for key, value := range attrs {
			switch v := value.(type) {
			case []byte:
				out[key] = append([]byte(nil), v...)
			case string:
				out[key] = []byte(v)
			}
		}
		return out
	default:
		return nil
	}
}

func (fs *FileSystem) buildNode(e Entry, cleanPath string, layer, order int) *Node {
	typ := entryType(e)
	mode := coerceMode(e.Mode)
	if mode == 0 && e.FileMode != 0 {
		mode = uint32(e.FileMode)
	}
	mode = NormalizeMode(mode, typ)

	uid := e.UID
	if e.Uid != 0 {
		uid = e.Uid
	}
	gid := e.GID
	if e.Gid != 0 {
		gid = e.Gid
	}

	rawTarget := ""
	symlinkTarget := ""
	hardlinkTarget := ""
	linkname := firstNonEmpty(e.Linkname, e.LinkName)
	switch typ {
	case TypeSymlink:
		symlinkTarget = firstNonEmpty(e.Symlink, e.Target, linkname)
		rawTarget = symlinkTarget
	case TypeHardlink:
		hardlinkTarget = firstNonEmpty(linkname, e.Target, e.LinkName)
		rawTarget = hardlinkTarget
	default:
		rawTarget = firstNonEmpty(e.Target, linkname)
	}

	xattrs := coerceXattrs(e.Xattrs)
	if len(xattrs) == 0 {
		xattrs = coerceXattrs(e.XAttrs)
	}
	if len(xattrs) == 0 && len(e.RawXattrs) > 0 {
		xattrs = coerceXattrs(e.RawXattrs)
	}

	var capability *CapabilitySet
	capError := ""
	if e.Capability != nil {
		capability = e.Capability
	} else if e.Cap != nil {
		capability = e.Cap
	} else if raw, ok := findCapabilityXattr(xattrs); ok {
		decoded, err := DecodeCapabilityValue(raw)
		if err != nil {
			capError = err.Error()
		} else {
			capability = decoded
		}
	}

	content := e.Content
	if len(content) == 0 && len(e.Data) > 0 {
		content = e.Data
	}
	if len(content) == 0 && len(e.Body) > 0 {
		content = e.Body
	}

	name := path.Base(cleanPath)
	if cleanPath == "." {
		name = "/"
	}
	node := &Node{
		Path:             cleanPath,
		Name:             name,
		Type:             typ,
		Mode:             mode,
		UID:              uid,
		Uid:              uid,
		GID:              gid,
		Gid:              gid,
		Uname:            firstNonEmpty(e.Uname, e.User),
		Gname:            firstNonEmpty(e.Gname, e.Group),
		Size:             e.Size,
		ModTime:          e.ModTime,
		SymlinkTarget:    symlinkTarget,
		Target:           rawTarget,
		Linkname:         linkname,
		HardlinkTarget:   hardlinkTarget,
		Xattrs:           xattrs,
		Capability:       capability,
		CapabilityError:  capError,
		Content:          append([]byte(nil), content...),
		ContentTruncated: e.ContentTruncated,
		LayerIndex:       layer,
		Order:            order,
		Children:         map[string]*Node{},
	}
	if typ == TypeDirectory {
		node.Children = make(map[string]*Node)
	}

	inode := fs.newInode(node)
	node.InodeID = inode.ID
	return node
}

func findCapabilityXattr(xattrs map[string][]byte) (any, bool) {
	for key, value := range xattrs {
		if key == SecurityCapabilityXattr || strings.HasSuffix(key, "."+SecurityCapabilityXattr) {
			return value, true
		}
	}
	return nil, false
}

func (fs *FileSystem) newInode(node *Node) *Inode {
	if fs.Inodes == nil {
		fs.Inodes = make(map[uint64]*Inode)
	}
	if fs.nextInodeID == 0 {
		fs.nextInodeID = 2
	}
	id := fs.nextInodeID
	fs.nextInodeID++
	inode := &Inode{
		ID:         id,
		Mode:       node.Mode,
		UID:        node.UID,
		GID:        node.GID,
		Uname:      node.Uname,
		Gname:      node.Gname,
		Xattrs:     cloneXattrs(node.Xattrs),
		Capability: node.Capability,
		Content:    append([]byte(nil), node.Content...),
		Paths:      map[string]struct{}{node.Path: {}},
		Nlink:      1,
	}
	fs.Inodes[id] = inode
	return inode
}

func (fs *FileSystem) ensureParent(cleanPath string, layer int) (*Node, string, error) {
	dir := path.Dir(cleanPath)
	base := path.Base(cleanPath)
	if cleanPath == "." || dir == "." && base == "." {
		return fs.Root, "", nil
	}
	if dir == "/" || dir == "" {
		dir = "."
	}

	current := fs.Root
	if dir != "." {
		segments := strings.Split(dir, "/")
		built := ""
		for _, segment := range segments {
			if segment == "" || segment == "." {
				continue
			}
			if built == "" {
				built = segment
			} else {
				built = path.Join(built, segment)
			}
			child, ok := current.Children[segment]
			if !ok {
				child = &Node{
					Path:       built,
					Name:       segment,
					Type:       TypeDirectory,
					Mode:       ModeDir | 0o755,
					UID:        0,
					Uid:        0,
					GID:        0,
					Gid:        0,
					LayerIndex: layer,
					Implicit:   true,
					Children:   make(map[string]*Node),
				}
				inode := fs.newInode(child)
				child.InodeID = inode.ID
				current.Children[segment] = child
			}
			if !child.IsDir() {
				return nil, "", fmt.Errorf("%w: %s is not a directory", ErrPathConflict, child.Path)
			}
			current = child
		}
	}
	return current, base, nil
}

func (fs *FileSystem) upsertEntry(entry Entry, cleanPath string, layer, order int) (*Node, error) {
	if cleanPath == "." {
		if entryType(entry) != TypeDirectory {
			return nil, fmt.Errorf("%w: root path must be directory", ErrInvalidPath)
		}
		built := fs.buildNode(entry, ".", layer, order)
		children := fs.Root.Children
		*fs.Root = *built
		fs.Root.Name = "/"
		fs.Root.Children = children
		if fs.Root.Children == nil {
			fs.Root.Children = map[string]*Node{}
		}
		return fs.Root, nil
	}

	parent, base, err := fs.ensureParent(cleanPath, layer)
	if err != nil {
		return nil, err
	}

	node := fs.buildNode(entry, cleanPath, layer, order)
	existing := parent.Children[base]
	if existing == nil {
		parent.Children[base] = node
		return node, nil
	}

	if existing.IsDir() && node.IsDir() {
		children := existing.Children
		*existing = *node
		existing.Children = children
		if existing.Children == nil {
			existing.Children = make(map[string]*Node)
		}
		if layer > existing.LayerIndex {
			existing.Implicit = false
		}
		return existing, nil
	}

	fs.removeTree(existing, ResidueReplace, layer, "")
	delete(parent.Children, base)
	parent.Children[base] = node
	return node, nil
}

func (fs *FileSystem) applyWhiteout(wh Whiteout) error {
	dir, err := fs.ensureDirectoryPath(wh.Dir, wh.Layer)
	if err != nil {
		return err
	}

	if wh.Opaque {
		dir.Opaque = true
		fs.recordOpaque(dir, wh.Layer, wh.Path)
		for name, child := range dir.Children {
			if !fs.pruneOpaque(child, wh.Layer) {
				delete(dir.Children, name)
			}
		}
		if wh.Layer >= dir.LayerIndex {
			dir.LayerIndex = wh.Layer
		}
		return nil
	}

	child := dir.Children[wh.Target]
	if child != nil {
		fs.removeTree(child, ResidueWhiteout, wh.Layer, wh.Path)
		delete(dir.Children, wh.Target)
	}
	return nil
}

func (fs *FileSystem) ensureDirectoryPath(clean string, layer int) (*Node, error) {
	if clean == "" || clean == "/" || clean == "." {
		return fs.Root, nil
	}
	clean, err := CleanPath(clean)
	if err != nil {
		return nil, err
	}
	parent, base, err := fs.ensureParent(clean, layer)
	if err != nil {
		return nil, err
	}
	node, ok := parent.Children[base]
	if !ok {
		node = &Node{
			Path:       clean,
			Name:       base,
			Type:       TypeDirectory,
			Mode:       ModeDir | 0o755,
			LayerIndex: layer,
			Implicit:   true,
			Children:   make(map[string]*Node),
		}
		inode := fs.newInode(node)
		node.InodeID = inode.ID
		parent.Children[base] = node
		return node, nil
	}
	if !node.IsDir() {
		return nil, fmt.Errorf("%w: whiteout parent %s is not a directory", ErrPathConflict, clean)
	}
	return node, nil
}

func (fs *FileSystem) removeTree(node *Node, reason ResidueReason, removedLayer int, marker string) {
	if node == nil {
		return
	}
	var walk func(*Node)
	walk = func(current *Node) {
		fs.Residue = append(fs.Residue, Residue{
			Path:         current.Path,
			Reason:       reason,
			LayerIndex:   current.LayerIndex,
			RemovedLayer: removedLayer,
			Whiteout:     marker,
			Node:         current.cloneShallow(),
		})
		for _, child := range current.Children {
			walk(child)
		}
	}
	walk(node)
}

func (fs *FileSystem) recordOpaque(dir *Node, layer int, marker string) {
	var walk func(*Node)
	walk = func(current *Node) {
		if current != dir && current.LayerIndex < layer {
			fs.Residue = append(fs.Residue, Residue{
				Path:         current.Path,
				Reason:       ResidueOpaque,
				LayerIndex:   current.LayerIndex,
				RemovedLayer: layer,
				Whiteout:     marker,
				Node:         current.cloneShallow(),
			})
		}
		for _, child := range current.Children {
			walk(child)
		}
	}
	for _, child := range dir.Children {
		walk(child)
	}
}

func (fs *FileSystem) pruneOpaque(node *Node, layer int) bool {
	if node.IsDir() {
		for name, child := range node.Children {
			if !fs.pruneOpaque(child, layer) {
				delete(node.Children, name)
			}
		}
	}
	if node.LayerIndex >= layer {
		return true
	}
	if node.IsDir() && len(node.Children) > 0 {
		node.LayerIndex = layer
		node.Implicit = true
		node.Mode = ModeDir | 0o755
		node.UID, node.Uid = 0, 0
		node.GID, node.Gid = 0, 0
		node.Uname, node.Gname = "", ""
		node.Xattrs = nil
		node.Capability = nil
		node.CapabilityError = ""
		return true
	}
	return false
}

// Stat returns the visible node without following symlinks.
func (fs *FileSystem) Stat(name string) (*Node, error) {
	if fs == nil || fs.Root == nil {
		return nil, errors.New("nil file system")
	}
	clean, err := CleanPath(name)
	if err != nil {
		return nil, err
	}
	if clean == "." {
		return fs.Root, nil
	}
	current := fs.Root
	for _, segment := range strings.Split(clean, "/") {
		if segment == "" || segment == "." {
			continue
		}
		if current == nil || current.Children == nil {
			return nil, fmt.Errorf("%w: %s", ErrInvalidPath, name)
		}
		next, ok := current.Children[segment]
		if !ok {
			return nil, fmt.Errorf("path not found: %s", name)
		}
		current = next
	}
	return current, nil
}

// Lstat is an alias for Stat; OCI merge paths do not automatically follow links.
func (fs *FileSystem) Lstat(name string) (*Node, error) { return fs.Stat(name) }

func (fs *FileSystem) reindex() error {
	if fs == nil || fs.Root == nil {
		return errors.New("nil file system")
	}
	fs.Nodes = make(map[string]*Node)
	fs.Files = fs.Nodes
	var walk func(*Node, string)
	walk = func(node *Node, parent string) {
		if node == nil {
			return
		}
		if node == fs.Root {
			node.Path = "."
			node.Name = "/"
		} else {
			if parent == "." || parent == "" {
				node.Path = node.Name
			} else {
				node.Path = parent + "/" + node.Name
			}
		}
		fs.Nodes[node.Path] = node
		for _, child := range node.Children {
			walk(child, node.Path)
		}
	}
	walk(fs.Root, ".")
	return nil
}
