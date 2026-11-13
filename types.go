// Package netsec implements an admission controller for kustomization files
// that a continuous-delivery system builds directly from untrusted merge
// requests.
//
// The package never touches the network or the real filesystem on its own.
// Callers hand it a parsed Kustomization, a FileCatalog describing the
// repository contents (including symlink targets) and a remote host
// allowlist. Local references are confined to the repository root, remote
// references are pinned to immutable commit hashes on allowlisted hosts,
// programmatic plugins that can execute external programs are rejected, and
// reference cycles are rejected.
package netsec

// Kustomization models the security-relevant fields of a parsed
// kustomization.yaml. Unknown fields are intentionally ignored.
//
// Use DecodeKustomization to populate it from a generic map produced by a
// YAML decoder such as sigs.k8s.io/yaml or gopkg.in/yaml.v3.
type Kustomization struct {
	// Resources are local paths or pinned remote references.
	Resources []string `yaml:"resources,omitempty" json:"resources,omitempty"`
	// Bases is the deprecated predecessor of Resources and carries the
	// same security requirements.
	Bases []string `yaml:"bases,omitempty" json:"bases,omitempty"`
	// Patches are strategic-merge or JSON6902 patches. Only local path
	// targets are admitted; remote patch targets are rejected.
	Patches []Patch `yaml:"patches,omitempty" json:"patches,omitempty"`
	// Generators reference generator configuration files or carry inline
	// generator specs. Exec-style plugins are rejected.
	Generators []PluginEntry `yaml:"generators,omitempty" json:"generators,omitempty"`
	// Transformers reference transformer configuration files or carry
	// inline transformer specs. Exec-style plugins are rejected.
	Transformers []PluginEntry `yaml:"transformers,omitempty" json:"transformers,omitempty"`
	// HelmCharts describe external charts. Repositories must be
	// allowlisted HTTPS endpoints and every chart must pin a version.
	HelmCharts []HelmChart `yaml:"helmCharts,omitempty" json:"helmCharts,omitempty"`
	// ConfigMapGenerators carry inline config-map specs. Their file
	// references obey the same containment rules as resources.
	ConfigMapGenerators []GeneratorArgs `yaml:"configMapGenerator,omitempty" json:"configMapGenerator,omitempty"`
	// SecretGenerators share the file/envelope shape of
	// ConfigMapGenerators and receive identical path treatment.
	SecretGenerators []GeneratorArgs `yaml:"secretGenerator,omitempty" json:"secretGenerator,omitempty"`
}

// Patch models one entry of the patches field. A patch may carry an inline
// Patch string, in which case Path is empty and no filesystem reference is
// validated.
type Patch struct {
	Path  string `yaml:"path,omitempty" json:"path,omitempty"`
	Patch string `yaml:"patch,omitempty" json:"patch,omitempty"`
}

// PluginEntry models an entry of generators/transformers. A plain string
// entry (a path to a plugin configuration file) decodes into an entry whose
// Path is set; an inline mapping decodes with Spec set and RawSpec holding
// the original mapping bytes for exec inspection.
type PluginEntry struct {
	// Path references a plugin configuration file inside the repository.
	Path string `yaml:"-" json:"-"`
	// Spec is the inline plugin mapping, when the entry was not a string.
	Spec map[string]any `yaml:"-" json:"-"`
	// RawSpec is the YAML/JSON encoding of Spec, used to detect an exec
	// declaration regardless of where it appears in the mapping.
	RawSpec []byte `yaml:"-" json:"-"`
}

// HelmChart models the security-relevant subset of a helmCharts entry.
type HelmChart struct {
	Name    string `yaml:"name,omitempty" json:"name,omitempty"`
	Repo    string `yaml:"repo,omitempty" json:"repo,omitempty"`
	Version string `yaml:"version,omitempty" json:"version,omitempty"`
}

// GeneratorArgs models a configMapGenerator/secretGenerator entry.
type GeneratorArgs struct {
	Name  string   `yaml:"name,omitempty" json:"name,omitempty"`
	Env   string   `yaml:"env,omitempty" json:"env,omitempty"`
	Files []string `yaml:"files,omitempty" json:"files,omitempty"`
}

// FileInfo describes one entry reported by the caller when walking the
// repository root.
type FileInfo struct {
	// RepoPath is the cleaned, root-relative path, using forward slashes
	// (for example "overlays/dev/kustomization.yaml").
	RepoPath string
	// IsSymlink marks the entry as a symbolic link.
	IsSymlink bool
	// LinkTarget is the link target exactly as stored in the link, which
	// may be relative to the link's directory or absolute.
	LinkTarget string
}

// FileCatalog is the read-only view of repository contents used for path
// containment and symlink resolution. It contains no mutable filesystem
// handles, so admission decisions stay deterministic and testable.
type FileCatalog struct {
	entries map[string]FileInfo
	links   map[string]string
}

// NewFileCatalog builds a catalog from the caller-supplied file list.
// RepoPath values are cleaned and must stay within the repository root;
// duplicate entries overwrite earlier ones.
func NewFileCatalog(files []FileInfo) (*FileCatalog, error) {
	catalog := &FileCatalog{
		entries: make(map[string]FileInfo, len(files)),
		links:   make(map[string]string),
	}
	for _, file := range files {
		cleaned := cleanRepoPath(file.RepoPath)
		if cleaned == "" || cleaned == "." {
			return nil, ErrInvalidCatalogEntry
		}
		if !inRoot(cleaned) {
			return nil, ErrCatalogEntryOutsideRoot
		}
		file.RepoPath = cleaned
		catalog.entries[cleaned] = file
		if file.IsSymlink {
			catalog.links[cleaned] = file.LinkTarget
		}
	}
	return catalog, nil
}

// linkAt returns the raw target of the symlink at a root-relative path.
func (catalog *FileCatalog) linkAt(repoPath string) (string, bool) {
	if catalog == nil {
		return "", false
	}
	target, ok := catalog.links[repoPath]
	return target, ok
}

// Violation describes a single rejected reference or configuration entry.
type Violation struct {
	// Field names the kustomization field containing the violation, for
	// example "resources[2]".
	Field string
	// Value is the offending raw value when one exists.
	Value string
	// Reason is one of the Reason* constants and is intended as a stable,
	// machine-readable identifier.
	Reason Reason
	// Detail carries a human-readable explanation.
	Detail string
}

// Reason is a machine-readable classification of a Violation.
type Reason string

const (
	// ReasonEmptyPath rejects empty or missing path values.
	ReasonEmptyPath Reason = "empty_path"
	// ReasonAbsolutePath rejects paths that begin at the filesystem root.
	ReasonAbsolutePath Reason = "absolute_path"
	// ReasonPathEscapesRoot rejects normalized paths containing ".." that
	// resolve outside the repository root.
	ReasonPathEscapesRoot Reason = "path_escapes_root"
	// ReasonRemoteNotAllowed rejects remote references in fields that may
	// only reference local files (for example patches).
	ReasonRemoteNotAllowed Reason = "remote_not_allowed"
	// ReasonSchemeNotHTTPS rejects references whose URL scheme is not
	// https, including scp-style and "::" prefixed kustomize remotes.
	ReasonSchemeNotHTTPS Reason = "scheme_not_https"
	// ReasonHostNotAllowed rejects references whose host is absent from
	// the caller-supplied allowlist.
	ReasonHostNotAllowed Reason = "host_not_allowed"
	// ReasonURLHasUserInfo rejects remote references embedding
	// credentials (user[:pass]@host).
	ReasonURLHasUserInfo Reason = "url_has_userinfo"
	// ReasonMalformedURL rejects references that cannot be parsed as URLs.
	ReasonMalformedURL Reason = "malformed_url"
	// ReasonRefMissing rejects remote references without a ref query
	// parameter. The default ref is mutable and therefore forbidden.
	ReasonRefMissing Reason = "ref_missing"
	// ReasonRefNotCommit rejects refs that are not exactly a 40-character
	// lowercase-or-uppercase hexadecimal commit hash, including branch
	// and tag names.
	ReasonRefNotCommit Reason = "ref_not_commit"
	// ReasonSymlinkEscapesRoot rejects a symlink chain whose target
	// resolves outside the repository root.
	ReasonSymlinkEscapesRoot Reason = "symlink_escapes_root"
	// ReasonSymlinkCycle rejects a symlink chain that visits a path more
	// than once.
	ReasonSymlinkCycle Reason = "symlink_cycle"
	// ReasonSymlinkTooDeep rejects symlink chains exceeding the hop limit.
	ReasonSymlinkTooDeep Reason = "symlink_too_deep"
	// ReasonExecPlugin rejects generators/transformers that declare an
	// exec plugin or carry a top-level exec field.
	ReasonExecPlugin Reason = "exec_plugin_rejected"
	// ReasonHelmRepo rejects helmCharts entries whose repo is missing,
	// not HTTPS, or hosted outside the allowlist.
	ReasonHelmRepo Reason = "helm_repo_rejected"
	// ReasonHelmVersion rejects helmCharts entries without a pinned
	// version.
	ReasonHelmVersion Reason = "helm_version_missing"
	// ReasonReferenceCycle rejects kustomization graphs whose local
	// references form a cycle.
	ReasonReferenceCycle Reason = "reference_cycle"
)

// AdmissionError is returned by the admission entry points when one or more
// rules fail. It implements the error interface and exposes the structured
// violations for callers that want to annotate pull-request checks.
type AdmissionError struct {
	Violations []Violation
}

func (err *AdmissionError) Error() string {
	if err == nil || len(err.Violations) == 0 {
		return "kustomization admission failed"
	}
	return "kustomization admission rejected " +
		itoa(len(err.Violations)) + " reference(s)"
}

// HasReason reports whether any violation carries reason.
func (err *AdmissionError) HasReason(reason Reason) bool {
	if err == nil {
		return false
	}
	for _, violation := range err.Violations {
		if violation.Reason == reason {
			return true
		}
	}
	return false
}

// Sentinel errors returned by catalog and graph construction helpers.
var (
	// ErrInvalidCatalogEntry marks an empty or unusable FileInfo.RepoPath.
	ErrInvalidCatalogEntry = admissionError("file catalog entry has an empty path")
	// ErrCatalogEntryOutsideRoot marks a catalog entry that escapes root.
	ErrCatalogEntryOutsideRoot = admissionError("file catalog entry resolves outside the repository root")
)

type admissionError string

func (err admissionError) Error() string { return string(err) }

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	negative := value < 0
	if negative {
		value = -value
	}
	var digits [20]byte
	index := len(digits)
	for value > 0 {
		index--
		digits[index] = byte('0' + value%10)
		value /= 10
	}
	if negative {
		index--
		digits[index] = '-'
	}
	return string(digits[index:])
}
