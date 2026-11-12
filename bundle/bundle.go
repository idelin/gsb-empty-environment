// Package bundle loads and enforces policy bundles for the Go policy
// service.
//
// A bundle is a (optionally gzip-compressed) tar archive containing a
// ".manifest" file together with the data documents listed in that
// manifest. The loader treats every load as an all-or-nothing operation:
// any validation failure rejects the whole archive and leaves the
// previously loaded version serving traffic.
package bundle

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"sync"
)

// maxBundleSize caps the total uncompressed size of a single bundle so a
// malicious archive cannot exhaust service memory.
const maxBundleSize = 512 << 20 // 512 MiB

// ManifestName is the fixed archive path of the manifest.
const ManifestName = ".manifest"

// dataPrefix is the archive namespace that may hold data documents.
const dataPrefix = "data/"

// Sentinel errors describing the reason a load was rejected. Callers can
// inspect them with errors.Is.
var (
	ErrManifest         = errors.New("bundle: invalid manifest")
	ErrRootTraversal    = errors.New("bundle: root outside the data namespace")
	ErrRootEmpty        = errors.New("bundle: manifest declares no roots")
	ErrOutsideRoot      = errors.New("bundle: file outside its own bundle roots")
	ErrRootOverlap      = errors.New("bundle: roots overlap with another bundle")
	ErrDigestMismatch   = errors.New("bundle: sha256 digest mismatch")
	ErrMissingFile      = errors.New("bundle: file listed in manifest is missing")
	ErrUnexpectedFile   = errors.New("bundle: archive contains an unlisted file")
	ErrUnsupportedEntry = errors.New("bundle: unsupported tar entry")
	ErrBundleTooLarge   = errors.New("bundle: archive exceeds size limit")
)

// Manifest mirrors the JSON ".manifest" document.
type Manifest struct {
	// Roots are the data path segments owned by the bundle, for example
	// "team-a/policies". The empty string is the global root and matches
	// every data path.
	Roots []string `json:"roots"`

	// FileDigests maps archive keys (e.g. "/data/team-a/x.json") to the
	// lowercase hex sha256 digest of their contents.
	FileDigests map[string]string `json:"file_digests"`
}

// Bundle is a fully validated, immutable bundle.
type Bundle struct {
	name     string
	roots    []string
	docs     map[string][]byte
	manifest Manifest
}

// Name returns the name under which the bundle was loaded.
func (b *Bundle) Name() string { return b.name }

// Roots returns the normalized data roots owned by the bundle.
func (b *Bundle) Roots() []string {
	out := make([]string, len(b.roots))
	copy(out, b.roots)
	return out
}

// Doc returns the raw bytes of the data document stored at key, where key
// is a canonical archive key such as "/data/team-a/x.json".
func (b *Bundle) Doc(key string) ([]byte, bool) {
	doc, ok := b.docs[key]
	if !ok {
		return nil, false
	}
	out := make([]byte, len(doc))
	copy(out, doc)
	return out, true
}

// Manifest returns a copy of the validated manifest.
func (b *Bundle) Manifest() Manifest {
	roots := make([]string, len(b.manifest.Roots))
	copy(roots, b.manifest.Roots)
	digests := make(map[string]string, len(b.manifest.FileDigests))
	for k, v := range b.manifest.FileDigests {
		digests[k] = v
	}
	return Manifest{Roots: roots, FileDigests: digests}
}

// Parse reads, verifies and validates a bundle archive. r may be a plain
// tar stream or a gzip-compressed tar stream. The returned Bundle is only
// non-nil when every check passes.
func Parse(name string, r io.Reader) (*Bundle, error) {
	br := bufio.NewReader(r)

	tr := tar.NewReader(br)
	gzipped, err := isGzip(br)
	if err != nil {
		return nil, err
	}
	if gzipped {
		zr, err := gzip.NewReader(br)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrManifest, err)
		}
		defer zr.Close()
		tr = tar.NewReader(zr)
	}

	files := make(map[string][]byte)
	var rawManifest []byte
	var total int64

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w: reading archive: %v", ErrManifest, err)
		}

		switch hdr.Typeflag {
		case tar.TypeReg, tar.TypeRegA:
		case tar.TypeDir:
			continue
		default:
			// Symlinks, hardlinks, devices, fifos and other special
			// entries cannot name data documents and are rejected.
			return nil, fmt.Errorf("%w: %s (type %d)", ErrUnsupportedEntry, hdr.Name, hdr.Typeflag)
		}

		key, ok := safeClean(hdr.Name)
		if !ok {
			return nil, fmt.Errorf("%w: %q", ErrRootTraversal, hdr.Name)
		}
		if _, exists := files[key]; exists {
			return nil, fmt.Errorf("%w: duplicate entry %q", ErrManifest, key)
		}

		if total+hdr.Size > maxBundleSize {
			return nil, ErrBundleTooLarge
		}
		content, err := io.ReadAll(io.LimitReader(tr, hdr.Size))
		if err != nil {
			return nil, fmt.Errorf("%w: reading %q: %v", ErrManifest, key, err)
		}
		total += int64(len(content))
		if total > maxBundleSize {
			return nil, ErrBundleTooLarge
		}

		if key == "/"+ManifestName {
			rawManifest = content
			continue
		}
		files[key] = content
	}

	if rawManifest == nil {
		return nil, fmt.Errorf("%w: %s not found", ErrManifest, ManifestName)
	}

	var man Manifest
	if err := json.Unmarshal(rawManifest, &man); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrManifest, err)
	}

	roots, err := normalizeRoots(man.Roots)
	if err != nil {
		return nil, err
	}

	digests := make(map[string]string, len(man.FileDigests))
	for key, sum := range man.FileDigests {
		canonical, ok := canonicalDigestKey(key)
		if !ok {
			return nil, fmt.Errorf("%w: digest key %q", ErrManifest, key)
		}
		if _, err := hex.DecodeString(sum); err != nil || len(sum) != sha256.Size*2 {
			return nil, fmt.Errorf("%w: digest for %q", ErrManifest, key)
		}
		digests[canonical] = strings.ToLower(sum)
	}
	man.FileDigests = digests

	docs := make(map[string][]byte, len(files))
	for key, content := range files {
		if !withinRoots(key, roots) {
			return nil, fmt.Errorf("%w: %q", ErrOutsideRoot, key)
		}
		want, listed := digests[key]
		if !listed {
			return nil, fmt.Errorf("%w: %q", ErrUnexpectedFile, key)
		}
		sum := sha256.Sum256(content)
		if got := hex.EncodeToString(sum[:]); got != want {
			return nil, fmt.Errorf("%w: %q", ErrDigestMismatch, key)
		}
		docs[key] = content
	}

	for key := range digests {
		if _, ok := docs[key]; !ok {
			return nil, fmt.Errorf("%w: %q", ErrMissingFile, key)
		}
	}

	man.Roots = append([]string(nil), roots...)
	return &Bundle{
		name:     name,
		roots:    roots,
		docs:     docs,
		manifest: man,
	}, nil
}

// Loader holds the set of active bundles and serves their documents.
// Loads are atomic: replacing or rejecting a bundle never leaves the
// loader in a partially updated state.
type Loader struct {
	mu      sync.RWMutex
	bundles map[string]*Bundle
}

// NewLoader creates an empty Loader.
func NewLoader() *Loader {
	return &Loader{bundles: make(map[string]*Bundle)}
}

// Load parses r and atomically installs the result under name. A bundle
// with an existing name is replaced; otherwise its roots must be disjoint
// from every other active bundle. When parsing or any check fails the
// previously loaded bundles remain in effect and the error is returned.
func (l *Loader) Load(name string, r io.Reader) (*Bundle, error) {
	b, err := Parse(name, r)
	if err != nil {
		return nil, err
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if _, exists := l.bundles[name]; !exists {
		for _, other := range l.bundles {
			if rootsOverlap(b.roots, other.roots) {
				return nil, fmt.Errorf("%w: %q vs %q", ErrRootOverlap, name, other.name)
			}
		}
	}
	l.bundles[name] = b
	return b, nil
}

// LoadFile is like Load but reads the archive from p.
func (l *Loader) LoadFile(name, p string) (*Bundle, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return l.Load(name, f)
}

// Unload removes the named bundle. It reports whether a bundle was
// present.
func (l *Loader) Unload(name string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.bundles[name]; ok {
		delete(l.bundles, name)
		return true
	}
	return false
}

// Bundle returns the active bundle with name, if any.
func (l *Loader) Bundle(name string) (*Bundle, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	b, ok := l.bundles[name]
	return b, ok
}

// Names returns the names of all active bundles.
func (l *Loader) Names() []string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	names := make([]string, 0, len(l.bundles))
	for name := range l.bundles {
		names = append(names, name)
	}
	return names
}

// LookupDoc returns the document at key (e.g. "/data/team-a/x.json") and
// the name of the bundle that owns it.
func (l *Loader) LookupDoc(key string) ([]byte, string, bool) {
	canonical, ok := canonicalDigestKey(key)
	if !ok {
		return nil, "", false
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	for _, b := range l.bundles {
		if doc, ok := b.docs[canonical]; ok {
			out := make([]byte, len(doc))
			copy(out, doc)
			return out, b.name, true
		}
	}
	return nil, "", false
}

// safeClean canonicalizes an archive entry name. It rejects absolute
// paths, traversal segments and entries outside the data/ and .manifest
// namespaces, returning a key of the form "/data/..." or "/.manifest".
func safeClean(name string) (string, bool) {
	if name == "" || strings.ContainsRune(name, '\\') || path.IsAbs(name) {
		return "", false
	}
	cleaned := path.Clean(name)
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", false
	}
	if cleaned == ManifestName {
		return "/" + ManifestName, true
	}
	if strings.HasPrefix(cleaned, dataPrefix) && cleaned != dataPrefix {
		return "/" + cleaned, true
	}
	return "", false
}

// canonicalDigestKey validates a manifest digest key and returns its
// normalized "/data/..." form.
func canonicalDigestKey(key string) (string, bool) {
	cleaned := path.Clean(strings.TrimPrefix(key, "/"))
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") ||
		!strings.HasPrefix(cleaned, dataPrefix) || cleaned == dataPrefix {
		return "", false
	}
	return "/" + cleaned, true
}

// normalizeRoots validates manifest roots and returns them in canonical
// segment form.
func normalizeRoots(raw []string) ([]string, error) {
	if len(raw) == 0 {
		return nil, ErrRootEmpty
	}
	roots := make([]string, 0, len(raw))
	seen := make(map[string]bool, len(raw))
	for _, root := range raw {
		if root == "" {
			roots = append(roots, "")
			seen[""] = true
			continue
		}
		if strings.ContainsRune(root, '\\') || path.IsAbs(root) {
			return nil, fmt.Errorf("%w: %q", ErrRootTraversal, root)
		}
		cleaned := path.Clean(root)
		if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
			return nil, fmt.Errorf("%w: %q", ErrRootTraversal, root)
		}
		if seen[cleaned] {
			continue
		}
		seen[cleaned] = true
		roots = append(roots, cleaned)
	}
	return roots, nil
}

// withinRoots reports whether the "/data/<segments>" key lives under one
// of the roots using whole path-segment prefix matching.
func withinRoots(key string, roots []string) bool {
	segments := splitSegments(strings.TrimPrefix(key, "/"+dataPrefix))
	for _, root := range roots {
		if root == "" {
			return true
		}
		if segmentPrefix(segments, splitSegments(root)) {
			return true
		}
	}
	return false
}

// rootsOverlap reports whether two root sets share any data path.
func rootsOverlap(a, b []string) bool {
	for _, ra := range a {
		for _, rb := range b {
			if ra == "" || rb == "" {
				return true
			}
			sa, sb := splitSegments(ra), splitSegments(rb)
			if segmentPrefix(sa, sb) || segmentPrefix(sb, sa) {
				return true
			}
		}
	}
	return false
}

// splitSegments splits a slash-separated data path into its segments.
// Empty segments (leading, trailing or duplicated slashes) are dropped.
func splitSegments(p string) []string {
	if p == "" {
		return nil
	}
	var segments []string
	for _, segment := range strings.Split(p, "/") {
		if segment != "" {
			segments = append(segments, segment)
		}
	}
	return segments
}

// segmentPrefix reports whether candidate starts with the leading whole
// segments of prefix. "a/b" therefore matches "a/b/c" but never "a/bc".
func segmentPrefix(candidate, prefix []string) bool {
	if len(prefix) > len(candidate) {
		return false
	}
	for i := range prefix {
		if candidate[i] != prefix[i] {
			return false
		}
	}
	return true
}

// isGzip peeks at the gzip magic number without consuming the bytes.
func isGzip(br *bufio.Reader) (bool, error) {
	magic, err := br.Peek(2)
	if err == io.EOF {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("%w: %v", ErrManifest, err)
	}
	return len(magic) == 2 && magic[0] == 0x1f && magic[1] == 0x8b, nil
}
