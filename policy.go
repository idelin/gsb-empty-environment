package oci

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityLow      Severity = "low"
	SeverityMedium   Severity = "medium"
	SeverityHigh     Severity = "high"
	SeverityCritical Severity = "critical"
)

type FindingType string

const (
	FindingSUID                    FindingType = "suid_unwhitelisted"
	FindingSGID                    FindingType = "sgid_unwhitelisted"
	FindingDangerousCapability     FindingType = "dangerous_capability"
	FindingInvalidCapability       FindingType = "invalid_capability_xattr"
	FindingCredentialResidue       FindingType = "hidden_credential_residue"
	FindingVisibleCredential       FindingType = "visible_credential"
	FindingSymlinkEscape           FindingType = "symlink_escape"
	FindingHardlinkEscape          FindingType = "hardlink_escape"
	FindingHardlinkShadow          FindingType = "hardlink_shadow"
	FindingUnresolvedHardlink      FindingType = "unresolved_hardlink"
	FindingInvalidWhiteoutRecorded FindingType = "invalid_whiteout"
)

// Finding is one policy violation or risky residue observation.
type Finding struct {
	Type     FindingType `json:"type"`
	Rule     string      `json:"rule"`
	Kind     string      `json:"kind"`
	Severity Severity    `json:"severity"`
	Message  string      `json:"message"`

	Path          string `json:"path"`
	SnapshotIndex int    `json:"snapshot_index"`
	LayerIndex    int    `json:"layer_index"`

	Mode uint32 `json:"mode,omitempty"`
	UID  int    `json:"uid,omitempty"`
	GID  int    `json:"gid,omitempty"`

	Capability   *CapabilitySet `json:"capability,omitempty"`
	Capabilities []string       `json:"capabilities,omitempty"`

	Target        string `json:"target,omitempty"`
	ResolvedPath  string `json:"resolved_path,omitempty"`
	CrossLayer    bool   `json:"cross_layer,omitempty"`
	TargetLayer   int    `json:"target_layer,omitempty"`
	RestrictedDir string `json:"restricted_dir,omitempty"`

	Hidden         bool   `json:"hidden,omitempty"`
	Whiteout       string `json:"whiteout,omitempty"`
	CredentialKind string `json:"credential_kind,omitempty"`
}

// Issue and Violation are report aliases for hosts using different vocabulary.
type Issue = Finding
type Violation = Finding

// Report contains policy findings and optional per-snapshot reports.
type Report struct {
	Findings        []Finding `json:"findings"`
	Issues          []Finding `json:"issues"`
	Vulnerabilities []Finding `json:"vulnerabilities"`

	SnapshotReports []*Report `json:"snapshot_reports,omitempty"`
	LayerReports    []*Report `json:"layer_reports,omitempty"`
	FinalReport     *Report   `json:"final_report,omitempty"`
	LayerCount      int       `json:"layer_count,omitempty"`
}

// GetVulnerabilities returns findings under a vulnerability-oriented name.
func (r *Report) GetVulnerabilities() []Finding { return r.Findings }

// OK reports whether no findings were produced.
func (r *Report) OK() bool { return r == nil || len(r.Findings) == 0 }

// HasFindings reports whether at least one finding exists.
func (r *Report) HasFindings() bool { return r != nil && len(r.Findings) > 0 }

// Count returns the finding count.
func (r *Report) Count() int {
	if r == nil {
		return 0
	}
	return len(r.Findings)
}

// HasType reports whether a finding of typ exists.
func (r *Report) HasType(typ FindingType) bool {
	if r == nil {
		return false
	}
	for _, finding := range r.Findings {
		if finding.Type == typ {
			return true
		}
	}
	return false
}

// OfType returns findings matching typ.
func (r *Report) OfType(typ FindingType) []Finding {
	if r == nil {
		return nil
	}
	var out []Finding
	for _, finding := range r.Findings {
		if finding.Type == typ {
			out = append(out, finding)
		}
	}
	return out
}

// Paths returns unique finding paths in report order.
func (r *Report) Paths() []string {
	if r == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, finding := range r.Findings {
		if finding.Path != "" && !seen[finding.Path] {
			seen[finding.Path] = true
			out = append(out, finding.Path)
		}
	}
	return out
}

// CapabilityGrant allows one path to carry selected capabilities.
type CapabilityGrant struct {
	Path         string       `json:"path,omitempty"`
	Capabilities []Capability `json:"capabilities,omitempty"`
	Names        []string     `json:"names,omitempty"`
}

// Whitelist contains allowed privileged paths and capabilities.
type Whitelist struct {
	Paths    []string `json:"paths,omitempty"`
	Binaries []string `json:"binaries,omitempty"`
	Allowed  []string `json:"allowed,omitempty"`

	SUID      []string `json:"suid,omitempty"`
	SGID      []string `json:"sgid,omitempty"`
	SUIDPaths []string `json:"suid_paths,omitempty"`
	SGIDPaths []string `json:"sgid_paths,omitempty"`

	Capabilities        []CapabilityGrant `json:"capabilities,omitempty"`
	CapabilityRules     []CapabilityGrant `json:"capability_rules,omitempty"`
	AllowedCapabilities []string          `json:"allowed_capabilities,omitempty"`
}

// Policy configures validation. A zero Policy enables the secure defaults.
type Policy struct {
	Whitelist `json:"whitelist,omitempty"`

	AllowedPaths    []string `json:"allowed_paths,omitempty"`
	AllowedBinaries []string `json:"allowed_binaries,omitempty"`

	AllowedSUID     []string `json:"allowed_suid,omitempty"`
	AllowedSGID     []string `json:"allowed_sgid,omitempty"`
	SUIDWhitelist   []string `json:"suid_whitelist,omitempty"`
	SGIDWhitelist   []string `json:"sgid_whitelist,omitempty"`
	WhitelistedSUID []string `json:"whitelisted_suid,omitempty"`
	WhitelistedSGID []string `json:"whitelisted_sgid,omitempty"`

	// Flexible aliases accept []string, map[string]bool, or map[string]struct{}.
	AllowSUID       any `json:"allow_suid,omitempty"`
	AllowSGID       any `json:"allow_sgid,omitempty"`
	AllowedSetUID   any `json:"allowed_setuid,omitempty"`
	AllowedSetGID   any `json:"allowed_setgid,omitempty"`
	SetUIDAllowlist any `json:"setuid_allowlist,omitempty"`
	SetGIDAllowlist any `json:"setgid_allowlist,omitempty"`

	AllowedCapabilityRules   []CapabilityGrant `json:"allowed_capability_rules,omitempty"`
	CapabilityWhitelistRules []CapabilityGrant `json:"capability_whitelist_rules,omitempty"`
	AllowedCapabilityStrings []string          `json:"allowed_capability_strings,omitempty"`

	RestrictedDirectories []string     `json:"restricted_directories,omitempty"`
	RestrictedPaths       []string     `json:"restricted_paths,omitempty"`
	SensitivePathGlobs    []string     `json:"sensitive_path_globs,omitempty"`
	DangerousCapabilities []Capability `json:"dangerous_capabilities,omitempty"`

	SkipSUID                     bool `json:"skip_suid,omitempty"`
	SkipSGID                     bool `json:"skip_sgid,omitempty"`
	SkipCapabilities             bool `json:"skip_capabilities,omitempty"`
	SkipCredentialResidue        bool `json:"skip_credential_residue,omitempty"`
	SkipSymlinkEscapes           bool `json:"skip_symlink_escapes,omitempty"`
	SkipHardlinkEscapes          bool `json:"skip_hardlink_escapes,omitempty"`
	CheckUnresolvedHardlinks     bool `json:"check_unresolved_hardlinks,omitempty"`
	CheckVisibleCredentials      bool `json:"check_visible_credentials,omitempty"`
	IncludeReplacedResidue       bool `json:"include_replaced_residue,omitempty"`
	CheckInheritableCapabilities bool `json:"check_inheritable_capabilities,omitempty"`
}

// DefaultRestrictedDirectories contains directories a cross-layer link should
// not point at without an explicit policy decision.
var DefaultRestrictedDirectories = []string{
	"/",
	"etc",
	"root",
	"proc",
	"sys",
	"dev",
	"boot",
	"run/secrets",
	"var/run/secrets",
}

// DefaultPolicy returns a strict policy with no privileged-path exceptions.
func DefaultPolicy() Policy {
	return Policy{
		RestrictedDirectories: append([]string(nil), DefaultRestrictedDirectories...),
		SensitivePathGlobs:    append([]string(nil), DefaultSensitivePathGlobs...),
		DangerousCapabilities: append([]Capability(nil), DefaultDangerousCapabilities...),
	}
}

// StrictPolicy is an explicit alias for DefaultPolicy.
func StrictPolicy() Policy { return DefaultPolicy() }

func coercePolicy(input any) Policy {
	switch value := input.(type) {
	case nil:
		return DefaultPolicy()
	case Policy:
		return value.withDefaults()
	case *Policy:
		if value == nil {
			return DefaultPolicy()
		}
		return value.withDefaults()
	case Whitelist:
		p := DefaultPolicy()
		p.Whitelist = value
		return p.withDefaults()
	case *Whitelist:
		p := DefaultPolicy()
		if value != nil {
			p.Whitelist = *value
		}
		return p.withDefaults()
	case []string:
		p := DefaultPolicy()
		p.Paths = append(p.Paths, value...)
		return p
	default:
		return DefaultPolicy()
	}
}

func (p Policy) withDefaults() Policy {
	if len(p.RestrictedDirectories) == 0 {
		p.RestrictedDirectories = append([]string(nil), DefaultRestrictedDirectories...)
	}
	if len(p.SensitivePathGlobs) == 0 {
		p.SensitivePathGlobs = append([]string(nil), DefaultSensitivePathGlobs...)
	}
	if len(p.DangerousCapabilities) == 0 {
		p.DangerousCapabilities = append([]Capability(nil), DefaultDangerousCapabilities...)
	}
	return p
}

// Validate checks one merged rootfs or intermediate snapshot. The policy may
// be omitted, a Policy, *Policy, Whitelist, or a []string path whitelist.
func Validate(fs *FileSystem, policies ...any) (*Report, error) {
	if fs == nil {
		return nil, fmt.Errorf("nil file system")
	}
	var policyInput any
	if len(policies) > 0 {
		policyInput = policies[0]
	}
	policy := coercePolicy(policyInput)
	report := &Report{}

	paths := make([]string, 0, len(fs.Nodes))
	for candidate := range fs.Nodes {
		paths = append(paths, candidate)
	}
	if len(paths) == 0 && fs.Root != nil {
		_ = fs.reindex()
		for candidate := range fs.Nodes {
			paths = append(paths, candidate)
		}
	}
	sort.Strings(paths)

	for _, nodePath := range paths {
		node := fs.Nodes[nodePath]
		if node == nil {
			continue
		}
		privilegeFindings := checkNodePrivileges(node, policy)
		for i := range privilegeFindings {
			privilegeFindings[i].SnapshotIndex = fs.SnapshotIndex
		}
		report.Findings = append(report.Findings, privilegeFindings...)
		report.Findings = append(report.Findings, checkNodeLinks(fs, node, policy)...)
		if policy.CheckVisibleCredentials {
			if kind, sensitive := IsSensitiveCredential(node.Path, node.Content); sensitive {
				report.Findings = append(report.Findings, Finding{
					Type:           FindingVisibleCredential,
					Rule:           string(FindingVisibleCredential),
					Kind:           string(FindingVisibleCredential),
					Severity:       SeverityHigh,
					Path:           node.Path,
					SnapshotIndex:  fs.SnapshotIndex,
					LayerIndex:     node.LayerIndex,
					Message:        "sensitive credential is visible in merged rootfs",
					CredentialKind: kind,
				})
			}
		}
	}

	if !policy.SkipCredentialResidue {
		for _, residue := range fs.Residue {
			if residue.Reason != ResidueWhiteout && residue.Reason != ResidueOpaque {
				if !(policy.IncludeReplacedResidue && residue.Reason == ResidueReplace) {
					continue
				}
			}
			var content []byte
			layer := residue.LayerIndex
			if residue.Node != nil {
				content = residue.Node.Content
			}
			kind, sensitive := IsSensitiveCredential(residue.Path, content)
			if !sensitive {
				for _, pattern := range policy.SensitivePathGlobs {
					if matchPathPattern(pattern, residue.Path) {
						sensitive = true
						kind = "policy-sensitive-path"
						break
					}
				}
			}
			if sensitive {
				report.Findings = append(report.Findings, Finding{
					Type:           FindingCredentialResidue,
					Rule:           string(FindingCredentialResidue),
					Kind:           string(FindingCredentialResidue),
					Severity:       SeverityCritical,
					Path:           residue.Path,
					SnapshotIndex:  fs.SnapshotIndex,
					LayerIndex:     layer,
					Message:        "credential remains in a lower layer after whiteout/opaque override",
					Hidden:         true,
					Whiteout:       residue.Whiteout,
					CredentialKind: kind,
				})
			}
		}
	}

	sort.SliceStable(report.Findings, func(i, j int) bool {
		if report.Findings[i].Path != report.Findings[j].Path {
			return report.Findings[i].Path < report.Findings[j].Path
		}
		return report.Findings[i].Type < report.Findings[j].Type
	})
	report.Issues = append([]Finding(nil), report.Findings...)
	report.Vulnerabilities = report.Findings
	return report, nil
}

// Check is the single-return validation helper for concise callers.
func Check(fs *FileSystem, policies ...any) []Finding {
	report, err := Validate(fs, policies...)
	if err != nil || report == nil {
		return nil
	}
	return report.Findings
}

func checkNodePrivileges(node *Node, policy Policy) []Finding {
	var findings []Finding
	base := Finding{
		Path:          node.Path,
		LayerIndex:    node.LayerIndex,
		Mode:          node.Mode,
		UID:           node.UID,
		GID:           node.GID,
		SnapshotIndex: 0,
	}

	if !policy.SkipSUID && node.SetUID() && !policy.suidAllowed(node.Path) {
		finding := base
		finding.Type = FindingSUID
		finding.Rule = string(FindingSUID)
		finding.Kind = string(FindingSUID)
		finding.Severity = SeverityHigh
		if node.UID == 0 {
			finding.Severity = SeverityCritical
		}
		finding.Message = "unwhitelisted setuid bit"
		findings = append(findings, finding)
	}

	if !policy.SkipSGID && node.SetGID() && !policy.sgidAllowed(node.Path) {
		finding := base
		finding.Type = FindingSGID
		finding.Rule = string(FindingSGID)
		finding.Kind = string(FindingSGID)
		finding.Severity = SeverityHigh
		if node.GID == 0 {
			finding.Severity = SeverityCritical
		}
		finding.Message = "unwhitelisted setgid bit"
		findings = append(findings, finding)
	}

	if !policy.SkipCapabilities {
		if node.CapabilityError != "" {
			finding := base
			finding.Type = FindingInvalidCapability
			finding.Rule = string(FindingInvalidCapability)
			finding.Kind = string(FindingInvalidCapability)
			finding.Severity = SeverityHigh
			finding.Message = "security.capability xattr failed binary decode: " + node.CapabilityError
			findings = append(findings, finding)
		} else if node.Capability != nil {
			dangerous := node.Capability.Dangerous(policy.DangerousCapabilities...)
			var unauthorized []Capability
			for _, cap := range dangerous {
				if !policy.capabilityAllowed(node.Path, cap) {
					unauthorized = append(unauthorized, cap)
				}
			}
			if policy.CheckInheritableCapabilities {
				for _, cap := range policy.DangerousCapabilities {
					if node.Capability.HasInheritable(cap) && !policy.capabilityAllowed(node.Path, cap) {
						unauthorized = appendUniqueCapability(unauthorized, cap)
					}
				}
			}
			if len(unauthorized) > 0 {
				names := make([]string, 0, len(unauthorized))
				for _, cap := range unauthorized {
					names = append(names, CapabilityName(cap))
				}
				finding := base
				finding.Type = FindingDangerousCapability
				finding.Rule = string(FindingDangerousCapability)
				finding.Kind = string(FindingDangerousCapability)
				finding.Severity = SeverityCritical
				finding.Capability = node.Capability
				finding.Capabilities = names
				finding.Message = "unwhitelisted dangerous file capabilities: " + strings.Join(names, ", ")
				findings = append(findings, finding)
			}
		}
	}

	return findings
}

func appendUniqueCapability(caps []Capability, want Capability) []Capability {
	for _, cap := range caps {
		if cap == want {
			return caps
		}
	}
	return append(caps, want)
}

func checkNodeLinks(fs *FileSystem, node *Node, policy Policy) []Finding {
	var findings []Finding

	if node.IsHardlink() || node.HardlinkTarget != "" {
		raw := firstNonEmpty(node.HardlinkTarget, node.Linkname, node.Target)
		location := AnalyzeLinkTarget(node.Path, raw)
		if location.EscapesRoot && !policy.SkipHardlinkEscapes {
			findings = append(findings, Finding{
				Type:          FindingHardlinkEscape,
				Rule:          string(FindingHardlinkEscape),
				Kind:          string(FindingHardlinkEscape),
				Severity:      SeverityCritical,
				Path:          node.Path,
				LayerIndex:    node.LayerIndex,
				Mode:          node.Mode,
				Target:        raw,
				ResolvedPath:  location.Resolved,
				Hidden:        true,
				Message:       "hard-link target traverses outside the synthesized image root",
				SnapshotIndex: fs.SnapshotIndex,
			})
		}
		if !location.EscapesRoot && node.ResolvedHardlinkTarget != "" && !policy.SkipHardlinkEscapes {
			if _, err := fs.Stat(node.ResolvedHardlinkTarget); err != nil {
				findings = append(findings, Finding{
					Type:          FindingHardlinkShadow,
					Rule:          string(FindingHardlinkShadow),
					Kind:          string(FindingHardlinkShadow),
					Severity:      SeverityHigh,
					Path:          node.Path,
					LayerIndex:    node.LayerIndex,
					Mode:          node.Mode,
					Target:        raw,
					ResolvedPath:  node.ResolvedHardlinkTarget,
					Hidden:        true,
					Message:       "hard link keeps an inode reachable after its original path was whiteouted or replaced",
					SnapshotIndex: fs.SnapshotIndex,
				})
			}
		}
		if policy.CheckUnresolvedHardlinks && node.ResolvedHardlinkTarget == "" && !location.EscapesRoot {
			findings = append(findings, Finding{
				Type:          FindingUnresolvedHardlink,
				Rule:          string(FindingUnresolvedHardlink),
				Kind:          string(FindingUnresolvedHardlink),
				Severity:      SeverityMedium,
				Path:          node.Path,
				LayerIndex:    node.LayerIndex,
				Target:        raw,
				Message:       "hard-link target is absent from all merged and removed paths",
				SnapshotIndex: fs.SnapshotIndex,
			})
		}
	}

	if node.IsSymlink() || node.SymlinkTarget != "" {
		raw := firstNonEmpty(node.SymlinkTarget, node.Target, node.Linkname)
		location := AnalyzeLinkTarget(node.Path, raw)
		restrictedDir, restricted := policy.restrictedMatch(location.Resolved)
		escapes := location.EscapesRoot
		if !policy.SkipSymlinkEscapes && (escapes || restricted) {
			finding := Finding{
				Type:          FindingSymlinkEscape,
				Rule:          string(FindingSymlinkEscape),
				Kind:          string(FindingSymlinkEscape),
				Severity:      SeverityHigh,
				Path:          node.Path,
				LayerIndex:    node.LayerIndex,
				Target:        raw,
				ResolvedPath:  location.Resolved,
				RestrictedDir: restrictedDir,
				Message:       "cross-layer symbolic link reaches an escaping or restricted path",
				SnapshotIndex: fs.SnapshotIndex,
			}
			if escapes {
				finding.Severity = SeverityCritical
			}
			if target, terminal, err := fs.ResolvePath(node.Path); err == nil && target != nil {
				finding.ResolvedPath = terminal
				finding.TargetLayer = target.LayerIndex
				finding.CrossLayer = target.LayerIndex != node.LayerIndex
			} else if target, err := fs.Stat(location.Resolved); err == nil && target != nil {
				finding.TargetLayer = target.LayerIndex
				finding.CrossLayer = target.LayerIndex != node.LayerIndex
			}
			findings = append(findings, finding)
		}
	}

	return findings
}

func (p Policy) restrictedMatch(resolved string) (string, bool) {
	if resolved == "" {
		return "", false
	}
	clean := strings.TrimPrefix(resolved, "./")
	for _, dir := range p.RestrictedDirectories {
		normalized, err := CleanPath(dir)
		if err != nil {
			normalized = strings.Trim(filepathToSlash(dir), "/")
		}
		if clean == normalized || strings.HasPrefix(clean, normalized+"/") {
			return normalized, true
		}
		if ok, _ := path.Match(dir, clean); ok {
			return dir, true
		}
	}
	for _, restricted := range p.RestrictedPaths {
		if matchPathPattern(restricted, clean) {
			return restricted, true
		}
	}
	return "", false
}

func (p Policy) genericAllowed(pathName string) bool {
	lists := [][]string{
		p.Paths, p.Binaries, p.Allowed,
		p.AllowedPaths, p.AllowedBinaries,
	}
	for _, list := range lists {
		if pathAllowed(list, pathName) {
			return true
		}
	}
	return false
}

func (p Policy) suidAllowed(pathName string) bool {
	if p.genericAllowed(pathName) {
		return true
	}
	return pathAllowed(p.SUID, pathName) ||
		pathAllowed(p.SUIDPaths, pathName) ||
		pathAllowed(p.AllowedSUID, pathName) ||
		pathAllowed(p.SUIDWhitelist, pathName) ||
		pathAllowed(p.WhitelistedSUID, pathName) ||
		pathAllowedAny(p.AllowSUID, pathName) ||
		pathAllowedAny(p.AllowedSetUID, pathName) ||
		pathAllowedAny(p.SetUIDAllowlist, pathName)
}

func (p Policy) sgidAllowed(pathName string) bool {
	if p.genericAllowed(pathName) {
		return true
	}
	return pathAllowed(p.SGID, pathName) ||
		pathAllowed(p.SGIDPaths, pathName) ||
		pathAllowed(p.AllowedSGID, pathName) ||
		pathAllowed(p.SGIDWhitelist, pathName) ||
		pathAllowed(p.WhitelistedSGID, pathName) ||
		pathAllowedAny(p.AllowSGID, pathName) ||
		pathAllowedAny(p.AllowedSetGID, pathName) ||
		pathAllowedAny(p.SetGIDAllowlist, pathName)
}

func pathAllowedAny(value any, target string) bool {
	for _, candidate := range stringSet(value) {
		if matchPathPattern(candidate, target) {
			return true
		}
	}
	return false
}

func stringSet(value any) []string {
	switch set := value.(type) {
	case nil:
		return nil
	case []string:
		return set
	case map[string]bool:
		out := make([]string, 0, len(set))
		for key, allowed := range set {
			if allowed {
				out = append(out, key)
			}
		}
		return out
	case map[string]struct{}:
		out := make([]string, 0, len(set))
		for key := range set {
			out = append(out, key)
		}
		return out
	case map[string]any:
		out := make([]string, 0, len(set))
		for key, allowed := range set {
			if truthy(allowed) {
				out = append(out, key)
			}
		}
		return out
	default:
		return nil
	}
}

func truthy(value any) bool {
	switch v := value.(type) {
	case bool:
		return v
	case nil:
		return false
	default:
		return true
	}
}

func pathAllowed(patterns []string, target string) bool {
	for _, pattern := range patterns {
		if matchPathPattern(pattern, target) {
			return true
		}
	}
	return false
}

func (p Policy) capabilityAllowed(pathName string, cap Capability) bool {
	if p.genericAllowed(pathName) {
		// A generic privileged-path allowlist intentionally trusts the path's
		// complete privilege set.
		return true
	}
	grants := append([]CapabilityGrant(nil), p.Capabilities...)
	grants = append(grants, p.CapabilityRules...)
	grants = append(grants, p.AllowedCapabilityRules...)
	grants = append(grants, p.CapabilityWhitelistRules...)
	for _, grant := range grants {
		if grant.Path != "" && !matchPathPattern(grant.Path, pathName) {
			continue
		}
		for _, granted := range grant.Capabilities {
			if granted == cap {
				return true
			}
		}
		for _, name := range grant.Names {
			if parsed, ok := ParseCapabilityName(name); ok && parsed == cap {
				return true
			}
		}
	}
	for _, rule := range p.AllowedCapabilities {
		if stringRuleAllows(rule, pathName, cap) {
			return true
		}
	}
	for _, rule := range p.AllowedCapabilityStrings {
		if stringRuleAllows(rule, pathName, cap) {
			return true
		}
	}
	return false
}

func stringRuleAllows(rule, pathName string, cap Capability) bool {
	rule = strings.TrimSpace(rule)
	if rule == "" {
		return false
	}
	var rulePath, capsPart string
	if idx := strings.IndexAny(rule, ":="); idx >= 0 {
		rulePath = rule[:idx]
		capsPart = rule[idx+1:]
	} else {
		capsPart = rule
	}
	if rulePath != "" && !matchPathPattern(rulePath, pathName) {
		return false
	}
	for _, item := range strings.FieldsFunc(capsPart, func(r rune) bool { return r == ',' || r == '|' || r == ' ' }) {
		if parsed, ok := ParseCapabilityName(item); ok && parsed == cap {
			return true
		}
	}
	return false
}

// ValidateSnapshots validates each intermediate snapshot independently and
// returns a combined report. Each finding retains its SnapshotIndex.
func ValidateSnapshots(snapshots []*FileSystem, policies ...any) (*Report, error) {
	combined := &Report{}
	for i, fs := range snapshots {
		if fs == nil {
			continue
		}
		if fs.SnapshotIndex < 0 {
			fs.SnapshotIndex = i
		}
		report, err := Validate(fs, policies...)
		if err != nil {
			return combined, err
		}
		report.LayerCount = fs.LayerCount
		combined.SnapshotReports = append(combined.SnapshotReports, report)
		combined.LayerReports = combined.SnapshotReports
		combined.Findings = append(combined.Findings, report.Findings...)
	}
	combined.Issues = append([]Finding(nil), combined.Findings...)
	combined.Vulnerabilities = combined.Findings
	return combined, nil
}

// VerifyLayers merges bottom-to-top layers, validates every post-layer
// snapshot, and retains the final report separately.
func VerifyLayers(layers []Layer, policies ...any) (*Report, *MergeResult, error) {
	merged, err := MergeWithSnapshots(layers...)
	if err != nil {
		return nil, nil, err
	}
	report, err := ValidateSnapshots(merged.Snapshots, policies...)
	if err != nil {
		return report, merged, err
	}
	if len(merged.Snapshots) > 0 {
		report.FinalReport = report.SnapshotReports[len(report.SnapshotReports)-1]
	}
	report.LayerCount = len(layers)
	return report, merged, nil
}

// Verify is a flexible wrapper: Verify([]Layer{...}, policy) or
// Verify(layers, policy).
func Verify(layers any, policies ...any) (*Report, *MergeResult, error) {
	var normalized []Layer
	switch value := layers.(type) {
	case []Layer:
		normalized = value
	case Layer:
		normalized = []Layer{value}
	case [][]Entry:
		for _, entries := range value {
			normalized = append(normalized, Layer{Entries: entries})
		}
	default:
		return nil, nil, fmt.Errorf("unsupported layers type %T", layers)
	}
	report, merged, err := VerifyLayers(normalized, policies...)
	return report, merged, err
}

// Scan is an alias for Verify that returns only the report.
func Scan(layers any, policies ...any) (*Report, error) {
	report, _, err := Verify(layers, policies...)
	return report, err
}
