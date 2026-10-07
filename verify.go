// Package verifysched schedules verification checks against frozen bundles.
//
// A bundle freezes a set of checks together with the input digest and the
// validator versions they must run against. Every result is bound to the
// bundle id, the input digest, the validator version and a per-attempt run
// id, so a result can never satisfy a gate it was not produced for. When
// inputs or rules change a new bundle is frozen and the old one, together
// with its results, is archived: archived results are kept for audit but
// can never satisfy the gate of a newer bundle.
package verifysched

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

var (
	ErrNoBundle        = errors.New("verifysched: no frozen bundle")
	ErrUnknownBundle   = errors.New("verifysched: receipt references an unknown bundle")
	ErrStaleBundle     = errors.New("verifysched: receipt references an archived bundle")
	ErrUnknownCheck    = errors.New("verifysched: receipt references an unknown check")
	ErrRevokedCheck    = errors.New("verifysched: check has been revoked")
	ErrRunMismatch     = errors.New("verifysched: run id does not match the issued run")
	ErrStaleInput      = errors.New("verifysched: receipt input digest does not match the frozen bundle")
	ErrStaleValidator  = errors.New("verifysched: receipt validator version does not match the frozen bundle")
	ErrDuplicateResult = errors.New("verifysched: check already has a terminal result")
)

// Status is the terminal outcome of a check run.
type Status int

const (
	Passed Status = iota
	Failed
	Errored
)

func (s Status) String() string {
	switch s {
	case Passed:
		return "passed"
	case Failed:
		return "failed"
	case Errored:
		return "errored"
	}
	return "unknown"
}

// CheckSpec describes one check of a bundle: which validator runs it and
// at which version.
type CheckSpec struct {
	Name      string
	Validator string
	Version   string
}

// Bundle is an immutable set of checks frozen against one input digest.
type Bundle struct {
	ID          string
	InputDigest string
	RuleDigest  string
	Checks      []CheckSpec
	FrozenAt    time.Time
}

// Dispatch tells a runner to execute one check. RunID uniquely identifies
// the attempt and must be echoed back in the receipt.
type Dispatch struct {
	BundleID  string
	CheckName string
	RunID     string
}

// Receipt reports the outcome of a dispatched run. It is only accepted if
// every binding matches the frozen bundle.
type Receipt struct {
	BundleID         string
	CheckName        string
	RunID            string
	InputDigest      string
	ValidatorVersion string
	Status           Status
	Message          string
}

// ArchivedBundle keeps a superseded bundle and all receipts bound to it,
// including ones that arrived after archival. Archived receipts never
// satisfy a gate.
type ArchivedBundle struct {
	Bundle   Bundle
	Receipts []Receipt
}

// GateReport is the release-gate decision for the current bundle.
type GateReport struct {
	BundleID string
	Passed   bool
	Pending  []string
	Failed   []string
	Errored  []string
	Revoked  []string
}

type runState struct {
	spec    CheckSpec
	runID   string
	attempt int
	result  *Receipt
}

type bundleState struct {
	bundle  Bundle
	runs    map[string]*runState
	revoked map[string]bool
	order   []string
}

// Scheduler freezes bundles, dispatches checks and collects receipts.
type Scheduler struct {
	mu       sync.Mutex
	seq      int
	current  *bundleState
	archived []ArchivedBundle
	now      func() time.Time
}

func New() *Scheduler {
	return &Scheduler{now: time.Now}
}

// Freeze archives the current bundle (if any) and freezes a new one from
// the given input digest and check set. All checks of a release gate must
// belong to this single frozen bundle.
func (s *Scheduler) Freeze(inputDigest string, checks []CheckSpec) (Bundle, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if inputDigest == "" {
		return Bundle{}, errors.New("verifysched: input digest must not be empty")
	}
	if len(checks) == 0 {
		return Bundle{}, errors.New("verifysched: bundle must contain at least one check")
	}
	seen := make(map[string]bool, len(checks))
	for _, c := range checks {
		if c.Name == "" || c.Validator == "" || c.Version == "" {
			return Bundle{}, fmt.Errorf("verifysched: check %q must name a validator and a version", c.Name)
		}
		if seen[c.Name] {
			return Bundle{}, fmt.Errorf("verifysched: duplicate check %q", c.Name)
		}
		seen[c.Name] = true
	}
	if s.current != nil {
		s.archived = append(s.archived, s.archiveLocked())
	}
	s.seq++
	b := Bundle{
		ID:          fmt.Sprintf("bundle-%d", s.seq),
		InputDigest: inputDigest,
		RuleDigest:  ruleDigest(checks),
		Checks:      append([]CheckSpec(nil), checks...),
		FrozenAt:    s.now(),
	}
	st := &bundleState{
		bundle:  b,
		runs:    make(map[string]*runState, len(checks)),
		revoked: make(map[string]bool),
	}
	for _, c := range checks {
		st.order = append(st.order, c.Name)
		rs := &runState{spec: c, attempt: 1}
		rs.runID = makeRunID(b.ID, c.Name, rs.attempt)
		st.runs[c.Name] = rs
	}
	s.current = st
	return b, nil
}

// Dispatch returns the runs that still need to execute for the current
// bundle. Pending checks keep their run id across calls; a check whose
// last attempt errored is re-issued with a fresh run id so the failed
// attempt can never be replayed.
func (s *Scheduler) Dispatch() ([]Dispatch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current == nil {
		return nil, ErrNoBundle
	}
	st := s.current
	var out []Dispatch
	for _, name := range st.order {
		if st.revoked[name] {
			continue
		}
		rs := st.runs[name]
		if rs.result != nil && rs.result.Status != Errored {
			continue
		}
		if rs.result != nil { // errored: re-issue with a new run id
			rs.attempt++
			rs.runID = makeRunID(st.bundle.ID, name, rs.attempt)
			rs.result = nil
		}
		out = append(out, Dispatch{BundleID: st.bundle.ID, CheckName: name, RunID: rs.runID})
	}
	return out, nil
}

// Submit records a receipt. Receipts may arrive in any order, but each
// binding (bundle, run id, input digest, validator version) is verified
// against the frozen bundle; stale or replayed receipts are rejected and,
// when they reference an archived bundle, kept in the archive only.
func (s *Scheduler) Submit(r Receipt) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current == nil {
		return ErrNoBundle
	}
	if r.BundleID != s.current.bundle.ID {
		for i := range s.archived {
			if s.archived[i].Bundle.ID == r.BundleID {
				s.archived[i].Receipts = append(s.archived[i].Receipts, r)
				return ErrStaleBundle
			}
		}
		return ErrUnknownBundle
	}
	st := s.current
	rs, ok := st.runs[r.CheckName]
	if !ok {
		return ErrUnknownCheck
	}
	if st.revoked[r.CheckName] {
		return ErrRevokedCheck
	}
	if r.RunID != rs.runID {
		return ErrRunMismatch
	}
	if r.InputDigest != st.bundle.InputDigest {
		return ErrStaleInput
	}
	if r.ValidatorVersion != rs.spec.Version {
		return ErrStaleValidator
	}
	if rs.result != nil {
		return ErrDuplicateResult
	}
	rc := r
	rs.result = &rc
	return nil
}

// Revoke removes a check from the current bundle's gate. Late receipts for
// a revoked check are rejected.
func (s *Scheduler) Revoke(checkName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current == nil {
		return ErrNoBundle
	}
	if _, ok := s.current.runs[checkName]; !ok {
		return ErrUnknownCheck
	}
	s.current.revoked[checkName] = true
	return nil
}

// Gate evaluates the release gate for the current frozen bundle. It passes
// only when every non-revoked check of this bundle has a passing result
// bound to this bundle.
func (s *Scheduler) Gate() (GateReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current == nil {
		return GateReport{}, ErrNoBundle
	}
	st := s.current
	rep := GateReport{BundleID: st.bundle.ID}
	for _, name := range st.order {
		if st.revoked[name] {
			rep.Revoked = append(rep.Revoked, name)
			continue
		}
		rs := st.runs[name]
		switch {
		case rs.result == nil:
			rep.Pending = append(rep.Pending, name)
		case rs.result.Status == Failed:
			rep.Failed = append(rep.Failed, name)
		case rs.result.Status == Errored:
			rep.Errored = append(rep.Errored, name)
		}
	}
	rep.Passed = len(rep.Pending) == 0 && len(rep.Failed) == 0 && len(rep.Errored) == 0
	return rep, nil
}

// Archived returns every superseded bundle with the receipts bound to it.
func (s *Scheduler) Archived() []ArchivedBundle {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ArchivedBundle, len(s.archived))
	copy(out, s.archived)
	return out
}

func (s *Scheduler) archiveLocked() ArchivedBundle {
	st := s.current
	ab := ArchivedBundle{Bundle: st.bundle}
	for _, name := range st.order {
		if rs := st.runs[name]; rs.result != nil {
			ab.Receipts = append(ab.Receipts, *rs.result)
		}
	}
	return ab
}

func makeRunID(bundleID, check string, attempt int) string {
	return fmt.Sprintf("%s/%s#%d", bundleID, check, attempt)
}

func ruleDigest(checks []CheckSpec) string {
	sorted := append([]CheckSpec(nil), checks...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	h := sha256.New()
	for _, c := range sorted {
		fmt.Fprintf(h, "%s\x00%s\x00%s\x00", c.Name, c.Validator, c.Version)
	}
	return hex.EncodeToString(h.Sum(nil))
}
