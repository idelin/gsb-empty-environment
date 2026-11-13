package netsec

import (
	"path"
	"sort"
)

// LeakKind classifies why a layer change is considered a leak.
type LeakKind string

const (
	// LeakMountPath means the changed path is a runtime mount point itself or
	// lies below one and must therefore never be snapshotted.
	LeakMountPath LeakKind = "mount_path"

	// LeakSensitiveDigest means the changed content digest matches a
	// caller-registered sensitive source digest, regardless of the path.
	LeakSensitiveDigest LeakKind = "sensitive_digest"
)

// Finding describes a single leaking layer change.
type Finding struct {
	// Layer identifies the layer whose change list contains the leak.
	Layer string

	// Path is the changed file path, cleaned with path.Clean.
	Path string

	// Digest is the content digest recorded for the change.
	Digest string

	// Kinds lists every rule the change violated.
	Kinds []LeakKind

	// MountPoint is the matching runtime mount point for a mount path leak,
	// and empty otherwise.
	MountPoint string
}

// FileChange is one entry of a layer's change list: a changed path and its
// content digest.
type FileChange struct {
	Path   string
	Digest string
}

// LayerChanges groups the file changes attributed to one layer.
type LayerChanges struct {
	Layer   string
	Changes []FileChange
}

// defaultIgnorePaths are paths the snapshotter never commits, mirroring
// Kaniko's built-in ignore list. They are tracked separately from the runtime
// mount points parsed from mountinfo.
var defaultIgnorePaths = []string{
	"/kaniko",
	"/proc",
	"/sys",
	"/dev",
}

// Checker evaluates layer change lists against runtime mount points,
// registered sensitive source digests and built-in ignore paths.
type Checker struct {
	mountPoints      []string
	sensitiveDigests map[string]struct{}
	ignorePaths      []string
}

// Option customizes a Checker.
type Option func(*Checker)

// WithExtraIgnorePaths registers additional ignore paths beyond the built-in
// ones. Every change at or below an ignore path is skipped entirely.
func WithExtraIgnorePaths(paths ...string) Option {
	return func(c *Checker) {
		c.ignorePaths = append(c.ignorePaths, cleanPaths(paths)...)
	}
}

// NewChecker builds a Checker. mountPoints typically comes from
// ParseMountPoints; sensitiveDigests are the content digests of sensitive
// sources registered by the caller (for example mounted credential files).
func NewChecker(mountPoints []string, sensitiveDigests []string, opts ...Option) *Checker {
	parsedMounts := cleanPaths(mountPoints)

	// The root filesystem's own "/" entry is the snapshot target rather than
	// a runtime-injected mount; when real mounts exist it would otherwise flag
	// every layer change. A caller supplying only "/" still gets
	// whole-filesystem protection.
	nonRootMounts := make([]string, 0, len(parsedMounts))
	for _, mountPoint := range parsedMounts {
		if mountPoint != "/" || len(parsedMounts) == 1 {
			nonRootMounts = append(nonRootMounts, mountPoint)
		}
	}

	checker := &Checker{
		mountPoints:      nonRootMounts,
		sensitiveDigests: make(map[string]struct{}, len(sensitiveDigests)),
		ignorePaths:      cleanPaths(defaultIgnorePaths),
	}

	for _, digest := range sensitiveDigests {
		if digest == "" {
			continue
		}
		checker.sensitiveDigests[digest] = struct{}{}
	}

	for _, opt := range opts {
		opt(checker)
	}

	// Longest prefix first so nested mounts win over their parents.
	sort.Slice(checker.mountPoints, func(i, j int) bool {
		return checker.mountPoints[i] > checker.mountPoints[j]
	})
	sort.Strings(checker.ignorePaths)

	return checker
}

// Check evaluates every layer and returns the findings in layer, then path,
// order. A nil result means no leakage was detected.
func (c *Checker) Check(layers []LayerChanges) []Finding {
	var findings []Finding
	for _, layer := range layers {
		findings = append(findings, c.CheckLayer(layer)...)
	}
	return findings
}

// CheckLayer evaluates one layer's change list.
func (c *Checker) CheckLayer(layer LayerChanges) []Finding {
	var findings []Finding

	for _, change := range layer.Changes {
		changePath := path.Clean(change.Path)

		// Built-in and configured ignores are never evaluated: the
		// snapshotter would never commit those paths anyway.
		if c.isIgnored(changePath) {
			continue
		}

		var kinds []LeakKind
		match := ""
		if mp, ok := c.underMount(changePath); ok {
			kinds = append(kinds, LeakMountPath)
			match = mp
		}
		if _, ok := c.sensitiveDigests[change.Digest]; ok && change.Digest != "" {
			kinds = append(kinds, LeakSensitiveDigest)
		}
		if len(kinds) == 0 {
			continue
		}

		findings = append(findings, Finding{
			Layer:      layer.Layer,
			Path:       changePath,
			Digest:     change.Digest,
			Kinds:      kinds,
			MountPoint: match,
		})
	}

	return findings
}

// MountPoints returns the cleaned, nested-first runtime mount points the
// checker was configured with.
func (c *Checker) MountPoints() []string {
	mountPoints := make([]string, len(c.mountPoints))
	copy(mountPoints, c.mountPoints)
	return mountPoints
}

// IgnorePaths returns the effective ignore paths, built-in plus caller
// configured.
func (c *Checker) IgnorePaths() []string {
	ignorePaths := make([]string, len(c.ignorePaths))
	copy(ignorePaths, c.ignorePaths)
	return ignorePaths
}

func (c *Checker) isIgnored(changePath string) bool {
	for _, ignorePath := range c.ignorePaths {
		if pathWithin(changePath, ignorePath) {
			return true
		}
	}
	return false
}

func (c *Checker) underMount(changePath string) (string, bool) {
	for _, mountPoint := range c.mountPoints {
		if pathWithin(changePath, mountPoint) {
			return mountPoint, true
		}
	}
	return "", false
}

// pathWithin reports whether candidate is base itself or nested below base.
// Both arguments must be cleaned absolute paths.
func pathWithin(candidate, base string) bool {
	if base == "/" || candidate == base {
		return true
	}
	return len(candidate) > len(base) &&
		candidate[:len(base)+1] == base+"/"
}

func cleanPaths(paths []string) []string {
	cleaned := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		p = path.Clean(p)
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		cleaned = append(cleaned, p)
	}
	return cleaned
}
