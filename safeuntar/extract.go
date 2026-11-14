// Package safeuntar extracts tar archives into a destination directory with
// strict guarantees that no archive entry can read from or write to files
// outside the destination root.
//
// The extractor is designed for untrusted archives uploaded to a build
// service. It defends against the classic tar traversal attacks:
//
//   - absolute or ".." traversing entry names and link targets;
//   - hard links pointing at host files (for example ../../etc/passwd);
//   - a symbolic link planted first and used as an ancestor of a later write;
//   - device nodes, FIFOs and other non-regular entry types;
//   - PAX/GNU long-name records that override the header name.
//
// Extraction is staged in a hidden sibling directory and published with a
// single atomic rename. Any failure removes the staging directory, so a
// rejected archive never leaves partial results at the destination.
package safeuntar

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Sentinel errors returned by Extract. Callers can use errors.Is to detect a
// specific rejection reason; every error also carries the offending entry.
var (
	// ErrPathEscape is returned when an entry name normalizes outside the
	// destination root, is absolute, or contains a ".." component.
	ErrPathEscape = errors.New("safeuntar: path escapes extraction root")

	// ErrLinkEscape is returned when a symbolic link target resolves outside
	// the extraction root or is absolute.
	ErrLinkEscape = errors.New("safeuntar: link target escapes extraction root")

	// ErrHardLinkTarget is returned when a hard link points at something that
	// is not a regular file already created from this archive, or when the
	// target escapes the extraction root.
	ErrHardLinkTarget = errors.New("safeuntar: hard link target is not an in-archive regular file")
	// ErrUnsupportedType is returned for character/block devices, FIFOs and
	// any other entry type the extractor does not materialize.
	ErrUnsupportedType = errors.New("safeuntar: unsupported entry type")

	// ErrDuplicateEntry is returned when an archive contains two entries that
	// normalize to the same path.
	ErrDuplicateEntry = errors.New("safeuntar: duplicate archive entry")

	// ErrMalformedArchive is returned for inconsistent declarations, such as
	// a truncated data section, an invalid sparse map or a corrupt PAX header.
	ErrMalformedArchive = errors.New("safeuntar: malformed archive")

	// ErrEmptyArchive is returned when the archive contains no entries.
	ErrEmptyArchive = errors.New("safeuntar: archive contains no entries")

	// ErrDestinationExists is returned when the requested destination path
	// already exists; Extract never overwrites an existing path.
	ErrDestinationExists = errors.New("safeuntar: destination already exists")
)

// maxNameComponent bounds a single path component to the portable POSIX
// NAME_MAX limit so extraction cannot fail partway on a target filesystem.
const maxNameComponent = 255

// Extract reads a tar stream from r and materializes it at dest.
//
// dest must not already exist. Its parent directory must exist and be
// writable; the archive is extracted into a temporary sibling directory there
// and atomically renamed to dest on success. On any error the temporary
// directory is removed and dest is left untouched.
//
// Only regular files, directories, hard links and symbolic links are
// accepted. Symbolic link targets must stay inside the root, and no later
// entry may write through a directory that is itself a symbolic link.
func Extract(r io.Reader, dest string) error {
	dest = filepath.Clean(dest)
	if _, err := os.Lstat(dest); err == nil {
		return fmt.Errorf("%w: %s", ErrDestinationExists, dest)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("safeuntar: cannot inspect destination %q: %w", dest, err)
	}

	parent := filepath.Dir(dest)
	staging, err := os.MkdirTemp(parent, ".safeuntar-staging-*")
	if err != nil {
		return fmt.Errorf("safeuntar: cannot create staging directory: %w", err)
	}

	tr := tar.NewReader(r)
	ex := &extractor{
		root:         staging,
		seen:         make(map[string]struct{}),
		symlinks:     make(map[string]string),
		regularFiles: make(map[string]struct{}),
	}

	if err := ex.run(tr); err != nil {
		_ = os.RemoveAll(staging)
		return err
	}
	if err := os.Rename(staging, dest); err != nil {
		_ = os.RemoveAll(staging)
		return fmt.Errorf("safeuntar: cannot publish extraction: %w", err)
	}
	return nil
}

// extractor holds the per-archive bookkeeping. All paths in the maps are
// POSIX-style paths relative to the extraction root, normalized with
// path.Clean.
type extractor struct {
	root string

	// seen records every path already occupied by an entry or an implicitly
	// created parent directory, which also implements duplicate detection.
	seen map[string]struct{}

	// symlinks maps an extracted symbolic link to its (validated) target.
	symlinks map[string]string

	// regularFiles records paths eligible as hard link targets.
	regularFiles map[string]struct{}

	entryCount int
}

func (e *extractor) run(tr *tar.Reader) error {
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			if e.entryCount == 0 {
				return ErrEmptyArchive
			}
			return nil
		}
		if err != nil {
			return fmt.Errorf("%w: %v", ErrMalformedArchive, err)
		}
		e.entryCount++

		if err := e.handleEntry(hdr, tr); err != nil {
			return err
		}
	}
}

func (e *extractor) handleEntry(hdr *tar.Header, tr *tar.Reader) error {
	rel, err := validateEntryName(hdr.Name)
	if err != nil {
		return err
	}
	if _, dup := e.seen[rel]; dup {
		return fmt.Errorf("%w: %q", ErrDuplicateEntry, hdr.Name)
	}
	if ancestor := e.symlinkAncestor(rel); ancestor != "" {
		return fmt.Errorf("%w: %q is written through symbolic link %q", ErrPathEscape, hdr.Name, ancestor)
	}

	hostPath := filepath.Join(e.root, filepath.FromSlash(rel))

	switch hdr.Typeflag {
	case tar.TypeDir:
		return e.makeDirectory(hdr, rel, hostPath)
	case tar.TypeReg, tar.TypeRegA:
		return e.makeRegularFile(hdr, rel, hostPath, tr)
	case tar.TypeLink:
		return e.makeHardLink(hdr, rel, hostPath)
	case tar.TypeSymlink:
		return e.makeSymlink(hdr, rel, hostPath)
	default:
		// Character/block devices, FIFOs, the contiguous-file type ('7')
		// and any unknown type are all refused.
		return fmt.Errorf("%w: type flag 0x%02x for %q", ErrUnsupportedType, hdr.Typeflag, hdr.Name)
	}
}

func (e *extractor) makeDirectory(hdr *tar.Header, rel, hostPath string) error {
	if err := e.ensureParent(rel); err != nil {
		return err
	}
	perm := os.FileMode(hdr.Mode) & os.ModePerm
	if perm == 0 {
		perm = 0o755
	}
	// A directory without execute bits cannot be entered, so later entries
	// could never be created inside it. Always keep at least owner rwx.
	perm |= 0o700
	if err := os.Mkdir(hostPath, perm); err != nil {
		return fmt.Errorf("safeuntar: cannot create directory %q: %w", rel, err)
	}
	e.seen[rel] = struct{}{}
	return nil
}

func (e *extractor) makeRegularFile(hdr *tar.Header, rel, hostPath string, tr *tar.Reader) error {
	if err := e.ensureParent(rel); err != nil {
		return err
	}

	// Explicitly validate the declared sparse map when it is visible in PAX
	// records. archive/tar performs the same check internally while reading
	// the data section, so this is defense in depth.
	if err := validateDeclaredSparseMap(hdr); err != nil {
		return err
	}

	perm := os.FileMode(hdr.Mode) & os.ModePerm
	if perm == 0 {
		perm = 0o644
	}

	// O_EXCL refuses to replace anything (including a pre-planted symlink).
	file, err := os.OpenFile(hostPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
	if err != nil {
		return fmt.Errorf("safeuntar: cannot create file %q: %w", rel, err)
	}

	// When the archive entry is sparse, tar.Reader wraps the stream in a
	// reader whose WriteTo seeks over declared holes instead of writing zero
	// bytes. *os.File implements io.WriteSeeker, so io.Copy preserves holes
	// on disk while still enforcing every declared-consistency check.
	written, copyErr := io.Copy(file, tr)
	if closeErr := file.Close(); copyErr == nil && closeErr != nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		return fmt.Errorf("%w: data section of %q: %v", ErrMalformedArchive, rel, copyErr)
	}
	if written != hdr.Size {
		return fmt.Errorf("%w: %q declares %d bytes but stream provided %d", ErrMalformedArchive, rel, hdr.Size, written)
	}

	e.seen[rel] = struct{}{}
	e.regularFiles[rel] = struct{}{}
	return nil
}

func (e *extractor) makeHardLink(hdr *tar.Header, rel, hostPath string) error {
	targetRel, err := validateEntryName(hdr.Linkname)
	if err != nil {
		return fmt.Errorf("%w: hard link %q -> %q: %v", ErrHardLinkTarget, rel, hdr.Linkname, err)
	}
	if _, ok := e.regularFiles[targetRel]; !ok {
		return fmt.Errorf("%w: %q -> %q references a file not previously created from this archive", ErrHardLinkTarget, rel, hdr.Linkname)
	}
	if err := e.ensureParent(rel); err != nil {
		return err
	}

	targetHost := filepath.Join(e.root, filepath.FromSlash(targetRel))
	info, err := os.Stat(targetHost)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("%w: %q -> %q target is not a regular file", ErrHardLinkTarget, rel, hdr.Linkname)
	}
	if err := os.Link(targetHost, hostPath); err != nil {
		return fmt.Errorf("safeuntar: cannot create hard link %q: %w", rel, err)
	}

	// The new name is itself a regular file and may be targeted by later
	// hard link entries.
	e.seen[rel] = struct{}{}
	e.regularFiles[rel] = struct{}{}
	return nil
}

func (e *extractor) makeSymlink(hdr *tar.Header, rel, hostPath string) error {
	if err := validateSymlinkTarget(rel, hdr.Linkname); err != nil {
		return err
	}
	if err := e.ensureParent(rel); err != nil {
		return err
	}
	if err := os.Symlink(hdr.Linkname, hostPath); err != nil {
		return fmt.Errorf("safeuntar: cannot create symbolic link %q: %w", rel, err)
	}
	e.seen[rel] = struct{}{}
	e.symlinks[rel] = hdr.Linkname
	return nil
}

// ensureParent creates every missing parent directory of rel. It is called
// only after symlinkAncestor has cleared the path, so none of the created
// directories can be a symbolic link.
func (e *extractor) ensureParent(rel string) error {
	parent := path.Dir(rel)
	if parent == "." {
		return nil
	}
	if _, ok := e.seen[parent]; ok {
		return nil
	}

	// Walk top-down so each Mkdir has a real directory as its parent.
	components := strings.Split(parent, "/")
	current := ""
	for _, component := range components {
		current = path.Join(current, component)
		if _, ok := e.seen[current]; ok {
			continue
		}
		hostPath := filepath.Join(e.root, filepath.FromSlash(current))
		if err := os.Mkdir(hostPath, 0o700); err != nil {
			return fmt.Errorf("safeuntar: cannot create parent directory %q: %w", current, err)
		}
		e.seen[current] = struct{}{}
	}
	return nil
}

// symlinkAncestor returns the nearest existing symbolic link that is rel
// itself or one of its ancestor directories, or "" when there is none.
func (e *extractor) symlinkAncestor(rel string) string {
	if _, ok := e.symlinks[rel]; ok {
		return rel
	}
	parent := path.Dir(rel)
	for parent != "." {
		if _, ok := e.symlinks[parent]; ok {
			return parent
		}
		parent = path.Dir(parent)
	}
	return ""
}

// validateEntryName applies POSIX lexical normalization to an archive entry
// name (or hard link target) and returns the cleaned root-relative path.
//
// It rejects empty names, NUL bytes, backslashes, absolute paths, any ".."
// component, the root itself and overlong components. Because the returned
// path contains only in-root segments, joining it with the extraction root
// can never escape.
func validateEntryName(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("%w: empty entry name", ErrPathEscape)
	}
	if strings.ContainsRune(name, 0) {
		return "", fmt.Errorf("%w: NUL byte in %q", ErrPathEscape, name)
	}
	if strings.ContainsRune(name, '\\') {
		return "", fmt.Errorf("%w: backslash in %q", ErrPathEscape, name)
	}

	cleaned := path.Clean(name)
	if path.IsAbs(cleaned) {
		return "", fmt.Errorf("%w: absolute path %q", ErrPathEscape, name)
	}
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("%w: %q", ErrPathEscape, name)
	}
	for _, component := range strings.Split(cleaned, "/") {
		if component == ".." {
			return "", fmt.Errorf("%w: %q contains a parent-directory component", ErrPathEscape, name)
		}
		if len(component) > maxNameComponent {
			return "", fmt.Errorf("%w: component of %q exceeds %d bytes", ErrPathEscape, name, maxNameComponent)
		}
	}
	return cleaned, nil
}

// validateSymlinkTarget checks the target of a symbolic link located at
// linkRel. Absolute targets are refused. Relative targets are normalized
// together with the link's directory and must resolve within the root; the
// target itself may not exist yet (links can point at a later entry).
func validateSymlinkTarget(linkRel, target string) error {
	if target == "" {
		return fmt.Errorf("%w: symbolic link %q has an empty target", ErrLinkEscape, linkRel)
	}
	if strings.ContainsRune(target, 0) || strings.ContainsRune(target, '\\') {
		return fmt.Errorf("%w: symbolic link %q -> %q", ErrLinkEscape, linkRel, target)
	}
	if path.IsAbs(target) {
		return fmt.Errorf("%w: absolute symbolic link %q -> %q", ErrLinkEscape, linkRel, target)
	}

	resolved := path.Clean(path.Join(path.Dir(linkRel), target))
	if resolved == ".." || strings.HasPrefix(resolved, "../") {
		return fmt.Errorf("%w: symbolic link %q -> %q", ErrLinkEscape, linkRel, target)
	}
	return nil
}
