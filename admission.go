package netsec

import (
	"bytes"
	"encoding/json"
	"strings"
)

// krmFunctionAnnotation is the KRM Functions annotation used by kustomize
// exec plugins. Its value names an executable to run.
const krmFunctionAnnotation = "config.kubernetes.io/function"

// Admitter runs the admission policy over parsed kustomizations. It is safe
// for concurrent use: an Admitter holds no per-validation mutable state.
type Admitter struct {
	catalog      *FileCatalog
	allowedHosts []string
}

// NewAdmitter builds an Admitter. catalog may be nil, in which case only
// lexical path containment is enforced (symlink checks are skipped).
func NewAdmitter(catalog *FileCatalog, allowedHosts []string) *Admitter {
	hosts := make([]string, len(allowedHosts))
	copy(hosts, allowedHosts)
	return &Admitter{catalog: catalog, allowedHosts: hosts}
}

// Validate applies every single-document rule to a kustomization located at
// the repository root and returns an *AdmissionError aggregating all
// violations. Use ValidateReferenceGraph in addition to reject cycles across
// multiple kustomizations.
func (admitter *Admitter) Validate(kustomization *Kustomization) error {
	return admitter.validateAt(kustomization, "")
}

// validateAt applies the single-document rules to a document living in
// baseDir, a cleaned root-relative directory ("" for the repository root).
// Local references inside the document are interpreted relative to baseDir.
func (admitter *Admitter) validateAt(kustomization *Kustomization, baseDir string) error {
	if kustomization == nil {
		return &AdmissionError{Violations: []Violation{{
			Field:  "kustomization",
			Reason: ReasonEmptyPath,
			Detail: "kustomization is nil",
		}}}
	}
	collector := violationCollector{}

	for index, resource := range kustomization.Resources {
		admitter.admitReference(&collector, baseDir, "resources", index, resource)
	}
	for index, base := range kustomization.Bases {
		admitter.admitReference(&collector, baseDir, "bases", index, base)
	}
	for index, patch := range kustomization.Patches {
		admitter.admitPatch(&collector, baseDir, index, patch)
	}
	for index, entry := range kustomization.Generators {
		admitter.admitPluginEntry(&collector, baseDir, "generators", index, entry)
	}
	for index, entry := range kustomization.Transformers {
		admitter.admitPluginEntry(&collector, baseDir, "transformers", index, entry)
	}
	for index, chart := range kustomization.HelmCharts {
		admitter.admitHelmChart(&collector, index, chart)
	}
	for index, args := range kustomization.ConfigMapGenerators {
		admitter.admitGeneratorArgs(&collector, baseDir, "configMapGenerator", index, args)
	}
	for index, args := range kustomization.SecretGenerators {
		admitter.admitGeneratorArgs(&collector, baseDir, "secretGenerator", index, args)
	}

	if len(collector.violations) == 0 {
		return nil
	}
	return &AdmissionError{Violations: collector.violations}
}

// admitReference validates one resources/bases entry, which may be either a
// local path or a pinned remote reference.
func (admitter *Admitter) admitReference(collector *violationCollector, baseDir, field string, index int, value string) {
	kind, ok := classifyReference(value)
	if !ok {
		collector.add(field, index, value, ReasonSchemeNotHTTPS,
			"only plain local paths and https URLs are allowed, kustomize :: and scp remotes are rejected")
		return
	}
	switch kind {
	case refRemote:
		if reason, detail := validateRemote(value, admitter.allowedHosts); reason != "" {
			collector.add(field, index, value, reason, detail)
		}
	case refLocal:
		if reason, detail := admitter.checkLocalPath(baseDir, value); reason != "" {
			collector.add(field, index, value, reason, detail)
		}
	}
}

// admitPatch validates a patches entry. Inline patches (Patch set, Path
// empty) carry no file reference; path targets must stay inside the root and
// may never be remote.
func (admitter *Admitter) admitPatch(collector *violationCollector, baseDir string, index int, patch Patch) {
	if patch.Path == "" {
		if strings.TrimSpace(patch.Patch) == "" {
			collector.add("patches", index, "", ReasonEmptyPath,
				"patch entry has neither path nor inline patch content")
		}
		return
	}
	kind, ok := classifyReference(patch.Path)
	if !ok || kind == refRemote {
		collector.add("patches", index, patch.Path, ReasonRemoteNotAllowed,
			"patches must reference local files inside the repository root")
		return
	}
	if reason, detail := admitter.checkLocalPath(baseDir, patch.Path); reason != "" {
		collector.add("patches", index, patch.Path, reason, detail)
	}
}

// admitPluginEntry validates one generators/transformers entry. Inline specs
// are rejected when they declare an exec plugin; path entries must stay
// inside the repository root.
func (admitter *Admitter) admitPluginEntry(collector *violationCollector, baseDir, field string, index int, entry PluginEntry) {
	if entry.Path != "" {
		kind, ok := classifyReference(entry.Path)
		if !ok || kind == refRemote {
			collector.add(field, index, entry.Path, ReasonRemoteNotAllowed,
				field+" must reference local plugin configuration files inside the repository root")
			return
		}
		if reason, detail := admitter.checkLocalPath(baseDir, entry.Path); reason != "" {
			collector.add(field, index, entry.Path, reason, detail)
		}
	}
	if reason, detail := detectExecPlugin(entry); reason != "" {
		value := entry.Path
		if value == "" {
			value = string(entry.RawSpec)
		}
		collector.add(field, index, value, reason, detail)
	}
}

// admitHelmChart enforces an allowlisted HTTPS repository and an explicit,
// non-empty version so chart contents cannot float with upstream changes.
func (admitter *Admitter) admitHelmChart(collector *violationCollector, index int, chart HelmChart) {
	if strings.TrimSpace(chart.Repo) == "" {
		collector.add("helmCharts", index, chart.Repo, ReasonHelmRepo,
			"helm chart "+chart.Name+" must declare an https repository")
	} else {
		kind, ok := classifyReference(chart.Repo)
		if !ok || kind != refRemote {
			collector.add("helmCharts", index, chart.Repo, ReasonHelmRepo,
				"helm repository must be an https URL on the host allowlist")
		} else if reason, detail := validateRemoteRepo(chart.Repo, admitter.allowedHosts); reason != "" {
			collector.add("helmCharts", index, chart.Repo, reason, detail)
		}
	}
	if strings.TrimSpace(chart.Version) == "" {
		collector.add("helmCharts", index, chart.Version, ReasonHelmVersion,
			"helm chart "+chart.Name+" must pin a version")
	}
}

// validateRemoteRepo applies the URL and host rules without requiring a
// commit ref, which applies to Helm chart repositories rather than Git refs.
func validateRemoteRepo(repo string, allowedHosts []string) (Reason, string) {
	reason, detail := validateRemote(repo, allowedHosts)
	switch reason {
	case "", ReasonRefMissing, ReasonRefNotCommit:
		// Helm repos are plain HTTPS endpoints and carry no Git ref.
		return "", ""
	default:
		return ReasonHelmRepo, detail
	}
}

// admitGeneratorArgs validates the file/envelope references of a
// configMapGenerator or secretGenerator entry.
func (admitter *Admitter) admitGeneratorArgs(collector *violationCollector, baseDir, field string, index int, args GeneratorArgs) {
	if args.Env != "" {
		if reason, detail := admitter.checkLocalPath(baseDir, args.Env); reason != "" {
			collector.add(field+"["+itoa(index)+"].env", -1, args.Env, reason, detail)
		}
	}
	for _, file := range args.Files {
		// Entries may use the "key=path" form; only the path is validated.
		pathValue := file
		if equals := strings.Index(file, "="); equals >= 0 {
			pathValue = file[equals+1:]
		}
		if reason, detail := admitter.checkLocalPath(baseDir, pathValue); reason != "" {
			collector.add(field, index, file, reason, detail)
		}
	}
}

// checkLocalPath runs lexical and symlink-aware containment for a reference
// relative to baseDir.
func (admitter *Admitter) checkLocalPath(baseDir, value string) (Reason, string) {
	_, reason, detail := resolveLocalPath(admitter.catalog, baseDir, value)
	return reason, detail
}

// detectExecPlugin rejects any inline plugin spec that can execute an
// external program. Two kustomize mechanisms are covered:
//
//   - a top-level "exec" field, the classic kustomize exec plugin shape;
//   - the KRM "config.kubernetes.io/function" annotation whose value
//     declares an exec function.
func detectExecPlugin(entry PluginEntry) (Reason, string) {
	if entry.Spec != nil {
		if value, ok := entry.Spec["exec"]; ok && !isEmptyValue(value) {
			return ReasonExecPlugin, "exec plugins are forbidden"
		}
	}
	raw := entry.RawSpec
	if len(raw) == 0 && entry.Spec != nil {
		if encoded, err := json.Marshal(entry.Spec); err == nil {
			raw = encoded
		}
	}
	if len(raw) == 0 {
		return "", ""
	}

	var document map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&document); err != nil {
		// YAML rather than JSON: fall back to a textual annotation scan so
		// an exec declaration encoded by either format is still caught.
		if textDeclaresExec(raw) {
			return ReasonExecPlugin, "exec plugins are forbidden"
		}
		return "", ""
	}
	if annotationDeclaresExec(document) {
		return ReasonExecPlugin, "exec plugins are forbidden"
	}
	return "", ""
}

// isEmptyValue treats null and empty mappings/strings as absent, matching
// the intent that a populated exec field is what enables execution.
func isEmptyValue(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(typed) == ""
	case map[string]any:
		return len(typed) == 0
	default:
		return false
	}
}

// annotationDeclaresExec inspects metadata.annotations for a KRM function
// declaration that names an executable.
func annotationDeclaresExec(document map[string]any) bool {
	metadata, ok := document["metadata"].(map[string]any)
	if !ok {
		return false
	}
	annotations, ok := metadata["annotations"].(map[string]any)
	if !ok {
		return false
	}
	value, ok := annotations[krmFunctionAnnotation]
	if !ok {
		return false
	}
	switch typed := value.(type) {
	case string:
		return strings.Contains(typed, "exec")
	case map[string]any:
		if _, hasExec := typed["exec"]; hasExec {
			return true
		}
	}
	return false
}

// textDeclaresExec is the encoding-agnostic fallback for raw YAML specs.
func textDeclaresExec(raw []byte) bool {
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "exec:") {
			return true
		}
		if strings.Contains(trimmed, krmFunctionAnnotation) &&
			strings.Contains(strings.ToLower(trimmed), "exec") {
			return true
		}
	}
	return false
}

// violationCollector accumulates violations across fields so a single run
// reports every problem rather than stopping at the first.
type violationCollector struct {
	violations []Violation
}

func (collector *violationCollector) add(field string, index int, value string, reason Reason, detail string) {
	label := field
	if index >= 0 {
		label = field + "[" + itoa(index) + "]"
	}
	collector.violations = append(collector.violations, Violation{
		Field:  label,
		Value:  value,
		Reason: reason,
		Detail: detail,
	})
}
