package iac

// Lifecycle models the lifecycle rules that affect graph compilation.
type Lifecycle struct {
	CreateBeforeDestroy bool `json:"create_before_destroy,omitempty"`
	PreventDestroy      bool `json:"prevent_destroy,omitempty"`

	// ForbidCBD marks a resource that cannot have two copies alive at the
	// same time (for example, a globally unique static cloud name). A plan
	// that would need to propagate create_before_destroy to this resource
	// fails with CBD_CONFLICT.
	ForbidCBD bool `json:"forbid_cbd,omitempty"`
}

// ProposedChange is the action computed from configuration, state and
// provider planning before lifecycle propagation is applied.
type ProposedChange struct {
	Action   ActionKind `json:"action,omitempty"`
	ForceNew bool       `json:"force_new,omitempty"`
}

// RemoteState describes the comparable attributes currently observed in the
// remote provider. Desired and Attributes should contain the same attribute
// namespace; provider-computed-only fields should be omitted.
type RemoteState struct {
	Exists     bool           `json:"exists"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

// Resource is the planner input for one managed object.
type Resource struct {
	Address   string   `json:"address"`
	DependsOn []string `json:"depends_on,omitempty"`

	// Change is the primary proposed action. Action and ForceNew are
	// convenience shorthands when Change is otherwise zero-valued.
	Change   ProposedChange `json:"change,omitempty"`
	Action   ActionKind     `json:"action,omitempty"`
	ForceNew bool           `json:"force_new,omitempty"`

	Lifecycle Lifecycle `json:"lifecycle,omitempty"`

	// ReplaceTriggeredBy upgrades this resource to Replace when any listed
	// upstream resource is updated or replaced.
	ReplaceTriggeredBy []string `json:"replace_triggered_by,omitempty"`

	// Remote enables drift detection when no explicit action was supplied.
	Remote  *RemoteState   `json:"remote,omitempty"`
	Desired map[string]any `json:"desired,omitempty"`

	// ForceNewAttributes lists desired attribute names whose change requires
	// replacement rather than an in-place update.
	ForceNewAttributes []string `json:"force_new_attributes,omitempty"`
}

// ChangeReason identifies why a resource received its final action.
type ChangeReason string

const (
	ReasonNoChange         ChangeReason = "no-change"
	ReasonConfigured       ChangeReason = "configured"
	ReasonRemoteMissing    ChangeReason = "remote-missing"
	ReasonRemoteDrift      ChangeReason = "remote-drift"
	ReasonForceNew         ChangeReason = "force-new"
	ReasonTriggeredReplace ChangeReason = "replace-triggered-by"
	ReasonTargetDestroy    ChangeReason = "target-destroy"
)
