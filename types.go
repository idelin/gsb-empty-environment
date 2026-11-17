// Package netsec implements a frozen template capability for an autoscaler
// control host.
//
// A workload template is approved once: approval binds the normalized
// template, the pinned image digest, the service identity and every security
// option. Derived instances may only change a declared allow-list of
// non-security fields. Every structured-patch change is evaluated against the
// bound approval and returned with the authority (basis) for the verdict.
package netsec

// WorkloadTemplate is the mutable workload template owned by the autoscaler.
type WorkloadTemplate struct {
	APIVersion string       `json:"apiVersion,omitempty"`
	Kind       string       `json:"kind,omitempty"`
	Metadata   ObjectMeta   `json:"metadata"`
	Spec       WorkloadSpec `json:"spec"`
}

// ObjectMeta carries identifying metadata.
type ObjectMeta struct {
	Name        string            `json:"name"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// WorkloadSpec is the workload body. Identity and security live in dedicated
// fields; ReplicaCount is the representative non-security field.
type WorkloadSpec struct {
	Image        string            `json:"image"`
	Replicas     *int              `json:"replicas,omitempty"`
	Env          []EnvVar          `json:"env,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
	Identity     Identity          `json:"identity"`
	Security     SecurityOptions   `json:"security"`
	Capabilities []string          `json:"capabilities,omitempty"`
	// Extra preserves unknown fields (including the deprecated
	// serviceAccount alias) across normalization round-trips.
	Extra map[string]any `json:"-"`
}

// EnvVar is a single environment entry.
type EnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Identity is the service identity bound to a workload.
type Identity struct {
	// ServiceAccountName is the canonical identity field.
	ServiceAccountName string `json:"serviceAccountName"`
	// GroupNames are the authorized identity groups.
	GroupNames []string `json:"groupNames,omitempty"`
}

// SecurityOptions collects the security-relevant runtime options.
type SecurityOptions struct {
	RunAsNonRoot        *bool `json:"runAsNonRoot"`
	ReadOnlyRootFS      *bool `json:"readOnlyRootFS"`
	AllowPrivilegeEscal *bool `json:"allowPrivilegeEscalation"`
	Privileged          *bool `json:"privileged"`
}

// BoolPtr returns a pointer to v. It keeps security struct literals compact.
func BoolPtr(v bool) *bool { return &v }

// PatchOperation is one RFC 6902-style structured patch operation expressed
// with JSON Pointer paths.
type PatchOperation struct {
	Op    string `json:"op"`
	From  string `json:"from,omitempty"`
	Path  string `json:"path,omitempty"`
	Value any    `json:"value,omitempty"`
}

// Verdict classifies an authorization decision.
type Verdict string

const (
	// VerdictAllow means the change is permitted by the bound approval.
	VerdictAllow Verdict = "ALLOW"
	// VerdictDeny means the change conflicts with the bound approval.
	VerdictDeny Verdict = "DENY"
)

// Basis is the authority cited for an authorization decision.
type Basis string

const (
	// BasisFrozenSecurity is cited when a security-bound field is unchanged
	// after normalization (including restoration of an approved default).
	BasisFrozenSecurity Basis = "FROZEN_SECURITY_BINDING"
	// BasisDigestPinned is cited when an image reference changes but resolves
	// to the approved digest.
	BasisDigestPinned Basis = "APPROVED_IMAGE_DIGEST"
	// BasisMutableAllowList is cited when an allow-listed non-security field
	// changes.
	BasisMutableAllowList Basis = "MUTABLE_FIELD_ALLOWLIST"
	// BasisNoOp is cited when an operation leaves the value unchanged.
	BasisNoOp Basis = "NO_OP"
	// BasisUnregisteredField is cited for a field the approval never
	// classified.
	BasisUnregisteredField Basis = "UNREGISTERED_FIELD"
	// BasisImmutableField is cited when a non-allow-listed, non-security
	// field changes.
	BasisImmutableField Basis = "IMMUTABLE_FIELD"
)

// ChangeDecision records the authorization outcome for one structural change.
type ChangeDecision struct {
	Op       string  `json:"op"`
	Path     string  `json:"path"`
	From     string  `json:"from,omitempty"`
	Verdict  Verdict `json:"verdict"`
	Basis    Basis   `json:"basis"`
	Field    string  `json:"field"`
	Class    string  `json:"class"`
	Detail   string  `json:"detail"`
	OldValue any     `json:"oldValue,omitempty"`
	NewValue any     `json:"newValue,omitempty"`
}

// Approval binds a reviewed template to an immutable security snapshot.
type Approval struct {
	ApprovalID      string
	TemplateID      string
	TemplateVersion int64
	Fingerprint     string
	ImageDigest     string
	MutableFields   []string
	Bound           NormalizedTemplate
}

// NormalizedTemplate is the canonical snapshot an approval freezes.
type NormalizedTemplate struct {
	Template    WorkloadTemplate
	Raw         map[string]any
	Image       ImageRef
	Fingerprint string
}

// ImageRef is a parsed, canonical image reference.
type ImageRef struct {
	Original   string
	Registry   string
	Repository string
	Tag        string
	Digest     string
	Canonical  string
}

// DeriveResult is the outcome of deriving an instance through a patch.
type DeriveResult struct {
	Instance  WorkloadTemplate
	Decisions []ChangeDecision
	Allowed   bool
}

// Instance is a workload materialized from an approved template.
type Instance struct {
	InstanceID   string
	ApprovalID   string
	TemplateID   string
	TemplateName string
	Spec         WorkloadSpec
	Decisions    []ChangeDecision
}

// Field classes used by the registry.
const (
	classSecurity     = "security"
	classMutable      = "mutable"
	classUnregistered = "unregistered"
)

// Sentinel errors returned by the package. They are wrapped with context; use
// errors.Is to match.
var (
	errApproval       = approvalError("invalid approval")
	errImage          = approvalError("invalid image reference")
	errPatch          = approvalError("invalid patch operation")
	errPointer        = approvalError("invalid JSON pointer")
	errConflict       = approvalError("template was updated concurrently")
	errStaleApproval  = approvalError("approval is stale: template changed after approval")
	errDenied         = approvalError("patch contains unauthorized changes")
	errNotFound       = approvalError("template not found")
	errAliasConflict  = approvalError("service account alias conflicts with identity.serviceAccountName")
	errMutableDenied  = approvalError("mutable field allow-list contains a security-bound path")
	errMutableUnknown = approvalError("mutable field allow-list contains an unregistered path")
)

type approvalError string

func (e approvalError) Error() string { return string(e) }
