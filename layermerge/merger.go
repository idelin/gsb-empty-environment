// Package layermerge computes the final filesystem view of a sequence of
// image-layer tar streams according to OCI/Docker layer semantics.
//
// Two kinds of whiteout markers are supported, both recognized by the
// ".wh." basename prefix:
//
//   - A whiteout file (".wh.<name>") hides the matching entry and every entry
//     underneath it that originated from any lower layer. Entries created in
//     the same layer are never affected.
//   - An opaque-directory marker (".wh..wh..opq") hides every lower-layer
//     entry inside the containing directory, while keeping the directory
//     itself and the entries it contains from the current layer.
//
// Layers are applied in order from the lowest layer to the highest layer.
// Callers may register protected paths; any whiteout or opaque marker that
// would hide a protected entry inherited from a lower layer causes the
// offending layer to be rejected before the merged view is modified.
package layermerge

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
)

const (
	whiteoutPrefix = ".wh."
	opaqueWhiteout = ".wh..wh..opq"
)

// EntryType identifies the kind of filesystem entry represented by an Entry.
type EntryType int

const (
	TypeRegular EntryType = iota
	TypeDir
	TypeSymlink
	TypeHardlink
	TypeCharDevice
	TypeBlockDevice
	TypeFifo
)

// Entry is the metadata of a single path in the merged filesystem view.
// File content is intentionally not retained: the package only computes the
// structural view so that the import service can materialize it elsewhere.
type Entry struct {
	Type     EntryType
	Mode     os.FileMode
	UID      int
	GID      int
	Linkname string
	// Layer is the zero-based index of the highest layer that contributed
	// this entry.
	Layer int
}

// ProtectedPathError reports that a layer attempted to hide a path the caller
// registered with Merger.Protect.
type ProtectedPathError struct {
	// Protected is the normalized registered path that was targeted.
	Protected string
	// Removed is the concrete view key whose removal would hide Protected.
	// It equals Protected, or is an ancestor directory of it.
	Removed string
	// Layer is the zero-based index of the layer that contains the marker.
	Layer int
	// Marker is the normalized tar path of the whiteout marker.
	Marker string
}

func (e *ProtectedPathError) Error() string {
	return fmt.Sprintf("layermerge: layer %d whiteout %q hides protected path %q (removed %q)",
		e.Layer, e.Marker, e.Protected, e.Removed)
}

// rawEntry is a parsed tar header buffered until all markers of a layer are
// known, so same-layer whiteouts can be applied against lower layers only.
type rawEntry struct {
	header *tar.Header
	typ    EntryType
}

// Merger accumulates layers and maintains the resulting filesystem view.
// A Merger is not safe for concurrent use; callers must serialize access.
type Merger struct {
	view      map[string]Entry
	protected map[string]struct{}
	layers    int
}

// New returns an empty Merger with no layers and no protected paths.
func New() *Merger {
	return &Merger{
		view:      make(map[string]Entry),
		protected: make(map[string]struct{}),
	}
}

// LayerCount reports the number of layers successfully applied so far.
func (m *Merger) LayerCount() int {
	return m.layers
}

// Protect registers a normalized filesystem path. A later layer that hides
// the entry at this path inherited from a lower layer is rejected.
// Registering a path that is not present is allowed; protection only triggers
// when an existing protected entry would be removed.
func (m *Merger) Protect(p string) error {
	clean, err := normalize(p)
	if err != nil {
		return err
	}
	m.protectClean(clean)
	return nil
}

func (m *Merger) protectClean(clean string) {
	if clean == "." {
		return
	}
	m.protected[clean] = struct{}{}
}

// ApplyLayer merges one layer tar stream on top of all previously applied
// layers. The layer must be supplied lowest-first.
//
// All whiteout and opaque markers in the layer are evaluated against the
// lower-layer view before any entry of this layer is added, which guarantees
// that same-layer whiteouts cannot remove same-layer files. When a marker
// would hide a protected path, a *ProtectedPathError is returned and the
// merged view is left untouched.
func (m *Merger) ApplyLayer(r io.Reader) error {
	tr := tar.NewReader(r)

	var entries []rawEntry
	var whiteouts []string
	opaqueDirs := make(map[string]struct{})

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("layermerge: reading layer %d: %w", m.layers, err)
		}

		name, err := normalize(hdr.Name)
		if err != nil {
			return fmt.Errorf("layermerge: layer %d entry %q: %w", m.layers, hdr.Name, err)
		}
		if name == "." {
			continue
		}

		base := path.Base(name)
		if strings.HasPrefix(base, whiteoutPrefix) {
			if base == opaqueWhiteout {
				opaqueDirs[path.Dir(name)] = struct{}{}
			} else {
				target := path.Join(path.Dir(name), strings.TrimPrefix(base, whiteoutPrefix))
				whiteouts = append(whiteouts, target)
			}
			continue
		}

		typ, ok := tarEntryType(hdr)
		if !ok {
			return fmt.Errorf("layermerge: layer %d entry %q: unsupported tar type flag %d",
				m.layers, hdr.Name, hdr.Typeflag)
		}
		entries = append(entries, rawEntry{header: hdr, typ: typ})
	}

	if err := m.applyMarkers(whiteouts, opaqueDirs); err != nil {
		return err
	}

	for _, raw := range entries {
		if err := m.addEntry(raw, m.layers); err != nil {
			return fmt.Errorf("layermerge: layer %d entry %q: %w", m.layers, raw.header.Name, err)
		}
	}

	m.layers++
	return nil
}

// applyMarkers deletes lower-layer entries selected by whiteouts and opaque
// markers. The view is mutated only after every protected-path check passes.
func (m *Merger) applyMarkers(whiteouts []string, opaqueDirs map[string]struct{}) error {
	removed := make(map[string]struct{})

	collect := func(key string) {
		if _, ok := m.view[key]; !ok {
			return
		}
		removed[key] = struct{}{}
		prefix := key + "/"
		for k := range m.view {
			if strings.HasPrefix(k, prefix) {
				removed[k] = struct{}{}
			}
		}
	}

	for _, target := range whiteouts {
		collect(target)
	}
	for dir := range opaqueDirs {
		prefix := dir + "/"
		for k := range m.view {
			if strings.HasPrefix(k, prefix) {
				removed[k] = struct{}{}
			}
		}
	}

	if err := m.checkProtected(removed, whiteouts, opaqueDirs); err != nil {
		return err
	}

	for key := range removed {
		delete(m.view, key)
	}
	return nil
}

// checkProtected returns the first violation in deterministic order: a
// registered path that is removed itself, or whose ancestor is removed.
func (m *Merger) checkProtected(removed map[string]struct{}, whiteouts []string, opaqueDirs map[string]struct{}) error {
	if len(m.protected) == 0 || len(removed) == 0 {
		return nil
	}

	protected := make([]string, 0, len(m.protected))
	for p := range m.protected {
		protected = append(protected, p)
	}
	sortStrings(protected)

	for _, p := range protected {
		var offending string
		if _, ok := removed[p]; ok {
			offending = p
		} else {
			offending = removedAncestor(removed, p)
		}
		if offending == "" {
			continue
		}

		marker := findMarker(offending, whiteouts, opaqueDirs)
		return &ProtectedPathError{
			Protected: p,
			Removed:   offending,
			Layer:     m.layers,
			Marker:    marker,
		}
	}
	return nil
}

// addEntry inserts or replaces one view entry and synthesizes any missing
// parent directory so the view always describes a connected tree.
func (m *Merger) addEntry(raw rawEntry, layer int) error {
	hdr := raw.header
	name, err := normalize(hdr.Name)
	if err != nil {
		return err
	}

	for parent := path.Dir(name); parent != "." && parent != "/"; parent = path.Dir(parent) {
		if _, ok := m.view[parent]; !ok {
			m.view[parent] = Entry{Type: TypeDir, Mode: os.ModeDir | 0o755, Layer: layer}
		}
	}

	entry := Entry{
		Type:  raw.typ,
		Mode:  hdr.FileInfo().Mode(),
		UID:   hdr.Uid,
		GID:   hdr.Gid,
		Layer: layer,
	}

	// Replacing a directory (inherited from a lower layer or synthesized for
	// an earlier same-layer child) with a non-directory entry must drop the
	// stale subtree, so old children cannot linger beneath a regular file.
	if raw.typ != TypeDir {
		if existing, ok := m.view[name]; ok && existing.Type == TypeDir {
			prefix := name + "/"
			for k := range m.view {
				if strings.HasPrefix(k, prefix) {
					delete(m.view, k)
				}
			}
		}
	}

	switch raw.typ {
	case TypeSymlink:
		entry.Linkname = hdr.Linkname
	case TypeHardlink:
		target, err := normalize(hdr.Linkname)
		if err != nil {
			return err
		}
		if _, ok := m.view[target]; !ok {
			return fmt.Errorf("hardlink target %q not found in merged view", hdr.Linkname)
		}
		entry.Linkname = target
	}

	m.view[name] = entry
	return nil
}

// Lookup returns the entry at p and whether it exists in the merged view.
func (m *Merger) Lookup(p string) (Entry, bool, error) {
	clean, err := normalize(p)
	if err != nil {
		return Entry{}, false, err
	}
	e, ok := m.view[clean]
	return e, ok, nil
}

// Snapshot returns a copy of the merged view keyed by slash-separated,
// root-relative paths. Mutating the returned map does not affect the Merger.
func (m *Merger) Snapshot() map[string]Entry {
	out := make(map[string]Entry, len(m.view))
	for k, v := range m.view {
		out[k] = v
	}
	return out
}

// View returns the sorted path keys of the merged view.
func (m *Merger) View() []string {
	out := make([]string, 0, len(m.view))
	for k := range m.view {
		out = append(out, k)
	}
	sortStrings(out)
	return out
}

// normalize converts a tar path into a root-relative, slash-separated clean
// path. Absolute paths are rooted at the image root; any ".." element is
// rejected rather than silently resolved, so a tar entry cannot traverse
// outside the merged view. The returned "." denotes the root itself.
func normalize(p string) (string, error) {
	if p == "" {
		return "", errors.New("empty path")
	}
	for _, elem := range strings.Split(p, "/") {
		if elem == ".." {
			return "", fmt.Errorf("path %q escapes filesystem root", p)
		}
	}
	clean := path.Clean(p)
	clean = strings.TrimPrefix(clean, "/")
	if clean == "" || clean == "." {
		return ".", nil
	}
	return clean, nil
}

func tarEntryType(hdr *tar.Header) (EntryType, bool) {
	switch hdr.Typeflag {
	case tar.TypeReg, tar.TypeRegA:
		return TypeRegular, true
	case tar.TypeDir:
		return TypeDir, true
	case tar.TypeSymlink:
		return TypeSymlink, true
	case tar.TypeLink:
		return TypeHardlink, true
	case tar.TypeChar:
		return TypeCharDevice, true
	case tar.TypeBlock:
		return TypeBlockDevice, true
	case tar.TypeFifo:
		return TypeFifo, true
	default:
		return 0, false
	}
}

// removedAncestor returns the nearest removed directory that contains p, or
// the empty string when no removed entry covers p.
func removedAncestor(removed map[string]struct{}, p string) string {
	parent := path.Dir(p)
	for parent != "." && parent != "/" {
		if _, ok := removed[parent]; ok {
			return parent
		}
		parent = path.Dir(parent)
	}
	return ""
}

// findMarker reconstructs a readable marker path for the error report by
// matching the removed key against the layer's whiteouts, falling back to the
// opaque marker of its containing directory.
func findMarker(removed string, whiteouts []string, opaqueDirs map[string]struct{}) string {
	for _, target := range whiteouts {
		if removed == target || strings.HasPrefix(removed, target+"/") {
			return path.Join(path.Dir(target), whiteoutPrefix+path.Base(target))
		}
	}
	parent := removed
	for parent != "." && parent != "/" {
		if _, ok := opaqueDirs[parent]; ok {
			return path.Join(parent, opaqueWhiteout)
		}
		parent = path.Dir(parent)
	}
	return ""
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
