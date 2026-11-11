// Package launch builds least-privilege launch plans for plugins and
// applies them against an injectable kernel interface.
//
// A Plan is the output of a pure function: PlanLaunch resolves identities
// and emits an ordered step list without touching any process state.
// Apply executes a plan against a Kernel, enforcing the dependency order
// between demotion steps and guaranteeing that any failure aborts the
// sequence before Exec, so a plugin never runs with pre-demotion state.
package launch

import (
	"errors"
	"fmt"
	"sort"
)

// IdentitySource resolves user and group names to numeric IDs. It is
// injected so PlanLaunch stays a pure, testable function.
type IdentitySource interface {
	LookupUser(name string) (uid, gid int, err error)
	LookupGroup(name string) (gid int, err error)
}

// Spec describes how a plugin process must be confined before exec.
type Spec struct {
	Plugin     string   // plugin name, for diagnostics
	User       string   // login name the plugin must run as
	Groups     []string // supplementary groups to keep
	KeepCaps   []string // capabilities retained in the bounding set
	NoNewPrivs bool     // forbid exec from gaining new privileges
	Path       string   // executable path
	Args       []string // argv, including argv[0]
	Env        []string // environment
}

// StepKind identifies one demotion or hand-off action.
type StepKind int

const (
	// StepNoNewPrivs must come first: once set, exec can never regain
	// privileges, so every later step (including the final Exec) is
	// constrained by it.
	StepNoNewPrivs StepKind = iota
	// StepCapBound shrinks the capability bounding set. It runs before
	// the UID change because the kernel clears permitted/effective caps
	// on a UID transition anyway, and bounding-set manipulation itself
	// may require CAP_SETPCAP which we still hold.
	StepCapBound
	// StepGroups sets supplementary groups. Requires privilege, so it
	// must run before the GID/UID drop.
	StepGroups
	// StepGID drops the primary group. Requires privilege, runs before
	// the UID drop.
	StepGID
	// StepUID drops the user ID. Irreversible, so it is the last
	// demotion step: no privileged operation may be scheduled after it.
	StepUID
	// StepExec hands off to the plugin image. Always last.
	StepExec
)

func (k StepKind) String() string {
	switch k {
	case StepNoNewPrivs:
		return "no_new_privs"
	case StepCapBound:
		return "cap_bound"
	case StepGroups:
		return "groups"
	case StepGID:
		return "gid"
	case StepUID:
		return "uid"
	case StepExec:
		return "exec"
	}
	return fmt.Sprintf("step(%d)", int(k))
}

// Step is one ordered action in a launch plan.
type Step struct {
	Kind StepKind
	Caps []string // StepCapBound: capabilities to keep
	GIDs []int    // StepGroups: supplementary group IDs
	GID  int      // StepGID: primary group ID
	UID  int      // StepUID: user ID
	Path string   // StepExec: executable path
	Args []string // StepExec: argv
	Env  []string // StepExec: environment
}

// Plan is an ordered, immutable launch recipe. Steps must be applied in
// slice order; the order encodes the dependency constraints documented on
// the StepKind constants.
type Plan struct {
	Plugin string
	Steps  []Step
}

// ErrInvalidSpec is returned when a Spec cannot yield a safe plan.
var ErrInvalidSpec = errors.New("invalid launch spec")

// PlanLaunch converts a Spec into an ordered Plan. It performs no I/O
// beyond the injected IdentitySource lookups and never mutates state.
func PlanLaunch(spec Spec, ids IdentitySource) (Plan, error) {
	if spec.Plugin == "" {
		return Plan{}, fmt.Errorf("%w: plugin name is empty", ErrInvalidSpec)
	}
	if spec.User == "" {
		return Plan{}, fmt.Errorf("%w: user is empty", ErrInvalidSpec)
	}
	if spec.Path == "" {
		return Plan{}, fmt.Errorf("%w: exec path is empty", ErrInvalidSpec)
	}

	uid, gid, err := ids.LookupUser(spec.User)
	if err != nil {
		return Plan{}, fmt.Errorf("resolve user %q: %w", spec.User, err)
	}
	if uid == 0 {
		return Plan{}, fmt.Errorf("%w: plugin %q must not run as root", ErrInvalidSpec, spec.Plugin)
	}

	gids, err := resolveGroups(spec.Groups, gid, ids)
	if err != nil {
		return Plan{}, err
	}

	keepCaps := dedupeSorted(spec.KeepCaps)

	p := Plan{Plugin: spec.Plugin}
	if spec.NoNewPrivs {
		p.Steps = append(p.Steps, Step{Kind: StepNoNewPrivs})
	}
	p.Steps = append(p.Steps,
		Step{Kind: StepCapBound, Caps: keepCaps},
		Step{Kind: StepGroups, GIDs: gids},
		Step{Kind: StepGID, GID: gid},
		Step{Kind: StepUID, UID: uid},
		Step{Kind: StepExec, Path: spec.Path, Args: spec.Args, Env: spec.Env},
	)
	return p, nil
}

func resolveGroups(names []string, primaryGID int, ids IdentitySource) ([]int, error) {
	seen := map[int]bool{primaryGID: true} // primary GID needs no supplementary entry
	var gids []int
	for _, name := range names {
		gid, err := ids.LookupGroup(name)
		if err != nil {
			return nil, fmt.Errorf("resolve group %q: %w", name, err)
		}
		if seen[gid] {
			continue
		}
		seen[gid] = true
		gids = append(gids, gid)
	}
	sort.Ints(gids)
	return gids, nil
}

func dedupeSorted(caps []string) []string {
	seen := make(map[string]bool, len(caps))
	var out []string
	for _, c := range caps {
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}
