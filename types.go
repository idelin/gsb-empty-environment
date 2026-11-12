package taskpkg

import (
	"errors"
	"fmt"
)

var (
	// ErrInvalidRequest marks malformed topology, version, or policy input.
	ErrInvalidRequest = errors.New("taskpkg: invalid request")

	// ErrMutableEdgeLimit marks a configuration with more than 12 mutable
	// filter edges. The limit is fixed by the planning contract.
	ErrMutableEdgeLimit = errors.New("taskpkg: mutable edge limit exceeded")

	// ErrNoSafeOrder means that no complete rollout can be published. No
	// partial plan and no first step are returned with this error.
	ErrNoSafeOrder = errors.New("taskpkg: no safe update order; no first step published")
)

// MaxMutableEdges is the maximum number of distinct mutable filter edges
// allowed in any reachable configuration.
const MaxMutableEdges = 12

// Edge is a directed filter edge between two nodes. Mutable edges are owned by
// a region and occur in one of that region's versions. Fixed edges are always
// present.
type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Version is one atomically installable edge set for a region. The order of
// Edges is irrelevant.
type Version struct {
	Name  string `json:"name"`
	Edges []Edge `json:"edges"`
}

// Region describes one administrative update boundary.
//
// CurrentVersion is installed now and TargetVersion is the version that must
// be installed when the rollout completes. The region can replace only the
// edges in one of its Versions, and it does so in one atomic step. When
// CanConfirm is false, the host cannot establish that the replacement took
// effect, so the region's old and new edge sets are both considered during
// verification.
type Region struct {
	ID             string    `json:"id"`
	CurrentVersion string    `json:"current_version"`
	TargetVersion  string    `json:"target_version"`
	Versions       []Version `json:"versions"`
	CanConfirm     bool      `json:"can_confirm"`
}

// ManagementPath is an authenticated control route that must remain
// available after every rollout step.
type ManagementPath struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Request is the complete planner input.
//
// AttackEntries are ingress points that must never reach any node in Secrets.
// FixedEdges do not belong to a region and are present in every configuration.
//
// AssumeOrderedReceipts and AssumeNoRegionLoss relax the default adversarial
// verification. Only set them when the controller can actually guarantee those
// properties. With either flag left false, every subset of issued updates is
// traversed, which models reordered receipts and lost regions.
type Request struct {
	Regions               []Region         `json:"regions"`
	FixedEdges            []Edge           `json:"fixed_edges,omitempty"`
	AttackEntries         []string         `json:"attack_entries"`
	Secrets               []string         `json:"secrets"`
	ManagementPaths       []ManagementPath `json:"management_paths,omitempty"`
	LostRegions           []string         `json:"lost_regions,omitempty"`
	AssumeOrderedReceipts bool             `json:"assume_ordered_receipts,omitempty"`
	AssumeNoRegionLoss    bool             `json:"assume_no_region_loss,omitempty"`
}

// Step is one atomic region replacement. A plan contains at most one Step for
// each region whose current and target versions differ.
type Step struct {
	Region      string `json:"region"`
	FromVersion string `json:"from_version"`
	ToVersion   string `json:"to_version"`
}

// UpdatePlan is a complete, verified rollout. A caller may publish only the
// first Step after receiving this value; Plan never returns a partial plan.
type UpdatePlan struct {
	Steps                  []Step `json:"steps"`
	MinimumSteps           int    `json:"minimum_steps"`
	VerifiedConfigurations int    `json:"verified_configurations"`
}

// FirstStep returns the first step of a complete plan. A rejected plan has no
// caller-visible value because Plan returns a nil plan with ErrNoSafeOrder.
func (p *UpdatePlan) FirstStep() (Step, bool) {
	if p == nil || len(p.Steps) == 0 {
		return Step{}, false
	}
	return p.Steps[0], true
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidRequest, fmt.Sprintf(format, args...))
}
