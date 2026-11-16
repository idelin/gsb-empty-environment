package kustomization

// Kustomization is the subset of a parsed kustomization.yaml document that the
// admission gate is able to evaluate.
//
// Polymorphic YAML entries (resources, bases, patches, generators and
// transformers) are kept as any values because kustomize accepts both plain
// strings and inline objects there. A string entry or an object with a "path"
// key is treated as a reference; other objects are treated as inline plugin
// configurations.
type Kustomization struct {
	Resources          []any
	Bases              []any
	Patches            []any
	Generators         []any
	Transformers       []any
	HelmCharts         []HelmChart
	ConfigMapGenerator []ConfigMapEntry
}

// HelmChart describes one entry of helmCharts.
type HelmChart struct {
	Name    string
	Version string
	Repo    string
}

// ConfigMapEntry describes one entry of configMapGenerator.
type ConfigMapEntry struct {
	Name  string
	Files []string
	Env   string
}

// FileEntry describes one path known to exist under the repository root.
//
// Path is slash-separated and relative to the repository root. When the path
// is a symlink, IsSymlink is true and LinkTarget carries the raw target as
// stored on disk (relative targets are interpreted relative to the symlink
// directory, absolute targets require Request.RootDir).
type FileEntry struct {
	Path       string
	IsDir      bool
	IsSymlink  bool
	LinkTarget string
}

// Request is the input of an admission decision.
//
// Entry is the repository-relative path of the kustomization file that the
// build starts from (for example "kustomization.yaml" or "overlay/dev/kustomization.yaml").
// Config holds the parsed entry document. Configs optionally holds every other
// parsed kustomization file in the repository, keyed by its repository-relative
// file path; it is required to detect reference cycles that span more than one
// file. Files is the full repository file list, including symlinks and their
// targets. AllowedHosts whitelists the hostnames of remote references and
// Helm repositories. RootDir is the optional absolute host path of the
// repository checkout and is only needed to validate absolute symlink targets.
type Request struct {
	Entry        string
	Config       *Kustomization
	Configs      map[string]*Kustomization
	Files        []FileEntry
	AllowedHosts []string
	RootDir      string
}

// Violation describes a single rejected construct.
type Violation struct {
	Field  string
	Value  string
	Reason string
}

func (v Violation) Error() string {
	switch {
	case v.Field != "" && v.Value != "":
		return v.Field + " " + quote(v.Value) + ": " + v.Reason
	case v.Field != "":
		return v.Field + ": " + v.Reason
	default:
		return v.Reason
	}
}

// AdmissionError is returned by Admit when one or more violations are found.
type AdmissionError struct {
	Violations []Violation
}

func (e *AdmissionError) Error() string {
	if e == nil || len(e.Violations) == 0 {
		return "kustomization admission failed"
	}
	parts := make([]string, len(e.Violations))
	for i, v := range e.Violations {
		parts[i] = v.Error()
	}
	return joinStrings(parts, "; ")
}

func quote(s string) string {
	return "\"" + s + "\""
}

func joinStrings(parts []string, sep string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += sep
		}
		out += p
	}
	return out
}
