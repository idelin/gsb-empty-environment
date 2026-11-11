package launch

import (
	"errors"
	"fmt"
	"slices"
)

// ProcState is the kernel-visible security state of the current process.
type ProcState struct {
	UID        int
	GID        int
	Groups     []int
	NoNewPrivs bool
	Bounding   []string // current capability bounding set
}

// Kernel abstracts the syscalls the executor depends on. Tests inject a
// mock; production injects a thin wrapper over prctl/setgroups/setgid/
// setuid/execve.
type Kernel interface {
	State() ProcState
	SetNoNewPrivs() error
	SetBoundingSet(keep []string) error
	SetGroups(gids []int) error
	SetGID(gid int) error
	SetUID(uid int) error
	Exec(path string, argv, env []string) error
}

// ErrPermission reports a demotion step the current process is not
// privileged enough to perform.
var ErrPermission = errors.New("operation not permitted")

// requiredCap maps privileged steps to the capability they need. Root
// (UID 0) satisfies every requirement implicitly.
var requiredCap = map[StepKind]string{
	StepCapBound: "CAP_SETPCAP",
	StepGroups:   "CAP_SETGID",
	StepGID:      "CAP_SETGID",
	StepUID:      "CAP_SETUID",
}

// Apply executes a plan against k in order.
//
// Safety guarantees:
//   - Preflight: every non-trivial demotion step is checked against the
//     privileges held *now*. If any step cannot be satisfied, Apply
//     returns before mutating any state at all.
//   - Fail-fast: the first failing step aborts the sequence; Exec is
//     never reached after a failure, so the plugin cannot inherit
//     pre-demotion state.
//   - Idempotent: steps already satisfied by the current state are
//     skipped, so re-applying a plan to an already-demoted process is
//     safe and does not require privileges it no longer holds.
func Apply(k Kernel, p Plan) error {
	if err := preflight(k.State(), p); err != nil {
		return err
	}
	for _, s := range p.Steps {
		if isNoop(s, k.State()) {
			continue
		}
		if err := applyStep(k, s); err != nil {
			return fmt.Errorf("launch %q: step %s failed: %w", p.Plugin, s.Kind, err)
		}
	}
	return nil
}

// preflight verifies, before any mutation, that every privileged step is
// either already satisfied or permitted by the current state.
func preflight(st ProcState, p Plan) error {
	for _, s := range p.Steps {
		if isNoop(s, st) {
			continue
		}
		cap, ok := requiredCap[s.Kind]
		if !ok {
			continue // unprivileged step (no_new_privs, exec)
		}
		if !hasPrivilege(st, cap) {
			return fmt.Errorf("launch %q: step %s requires %s: %w", p.Plugin, s.Kind, cap, ErrPermission)
		}
	}
	return nil
}

func applyStep(k Kernel, s Step) error {
	switch s.Kind {
	case StepNoNewPrivs:
		return k.SetNoNewPrivs()
	case StepCapBound:
		return k.SetBoundingSet(s.Caps)
	case StepGroups:
		return k.SetGroups(s.GIDs)
	case StepGID:
		return k.SetGID(s.GID)
	case StepUID:
		return k.SetUID(s.UID)
	case StepExec:
		return k.Exec(s.Path, s.Args, s.Env)
	}
	return fmt.Errorf("unknown step kind %d", int(s.Kind))
}

// isNoop reports whether the current state already satisfies the step.
func isNoop(s Step, st ProcState) bool {
	switch s.Kind {
	case StepNoNewPrivs:
		return st.NoNewPrivs
	case StepCapBound:
		// No-op when nothing currently bounded would be dropped.
		for _, c := range st.Bounding {
			if !slices.Contains(s.Caps, c) {
				return false
			}
		}
		return true
	case StepGroups:
		return equalSet(st.Groups, s.GIDs)
	case StepGID:
		return st.GID == s.GID
	case StepUID:
		return st.UID == s.UID
	case StepExec:
		return false
	}
	return false
}

func hasPrivilege(st ProcState, cap string) bool {
	return st.UID == 0 || slices.Contains(st.Bounding, cap)
}

func equalSet(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	counts := make(map[int]int, len(a))
	for _, v := range a {
		counts[v]++
	}
	for _, v := range b {
		counts[v]--
		if counts[v] < 0 {
			return false
		}
	}
	return true
}
