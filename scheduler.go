package verisched

import "fmt"

// CheckState is the per-check verdict used in gate reports.
type CheckState int

const (
	CheckPending CheckState = iota
	CheckPassed
	CheckFailed
	CheckRevoked
)

// GateReport is the release-gate decision for one run of the current
// frozen package. Passed is true only when every check of the package
// has a passing receipt bound to that run.
type GateReport struct {
	PackageID string
	RunID     string
	Passed    bool
	Checks    map[string]CheckState
}

// Scheduler owns the current frozen package, the archive of superseded
// packages, and all receipts. Receipts may arrive in any order.
type Scheduler struct {
	current  Package
	hasCur   bool
	runs     map[string]bool // run ids valid for the current package
	runSeq   int
	archive  []Package
	archived map[string]bool                          // package ids that are archived
	receipts map[string]map[string]map[string]Receipt // pkgID -> runID -> checkID -> receipt
	revoked  map[string]map[string]bool               // pkgID -> checkID -> revoked
}

func NewScheduler() *Scheduler {
	return &Scheduler{
		runs:     map[string]bool{},
		archived: map[string]bool{},
		receipts: map[string]map[string]map[string]Receipt{},
		revoked:  map[string]map[string]bool{},
	}
}

// FreezePackage freezes a new package from the input digest and check
// specs, and makes it the current one. The previous current package is
// archived: its receipts are kept for audit but can never satisfy the
// gate again.
func (s *Scheduler) FreezePackage(inputDigest string, checks []CheckSpec) (Package, error) {
	pkg, err := Freeze(inputDigest, checks)
	if err != nil {
		return Package{}, err
	}
	if s.hasCur {
		if s.current.ID == pkg.ID {
			return Package{}, fmt.Errorf("verisched: package %s is already active", pkg.ID)
		}
		s.archive = append(s.archive, s.current)
		s.archived[s.current.ID] = true
	}
	s.current = pkg
	s.hasCur = true
	s.runs = map[string]bool{}
	return pkg, nil
}

// Current returns the active frozen package.
func (s *Scheduler) Current() (Package, bool) {
	return s.current, s.hasCur
}

// Archived returns the superseded packages, oldest first.
func (s *Scheduler) Archived() []Package {
	out := make([]Package, len(s.archive))
	copy(out, s.archive)
	return out
}

// StartRun opens a new run bound to the current frozen package.
func (s *Scheduler) StartRun() (Run, error) {
	if !s.hasCur {
		return Run{}, ErrNoCurrentPackage
	}
	s.runSeq++
	run := Run{ID: fmt.Sprintf("run-%d", s.runSeq), PackageID: s.current.ID}
	s.runs[run.ID] = true
	return run, nil
}

// Submit records a receipt. The receipt's binding (package id, input
// digest, validator version, run id) must match the frozen package and
// run it claims. Receipts for archived packages are recorded for audit
// and reported with ErrPackageArchived; they never satisfy the gate.
func (s *Scheduler) Submit(r Receipt) error {
	if r.Status != StatusPassed && r.Status != StatusFailed {
		return fmt.Errorf("verisched: invalid receipt status %d", int(r.Status))
	}
	pkg, err := s.lookupPackage(r.PackageID)
	if err != nil {
		return err
	}
	spec, ok := pkg.Spec(r.CheckID)
	if !ok {
		return ErrUnknownCheck
	}
	if r.InputDigest != pkg.InputDigest || r.ValidatorVersion != spec.Version {
		return ErrBindingMismatch
	}
	if s.revoked[pkg.ID][r.CheckID] {
		return ErrCheckRevoked
	}
	if !s.archived[pkg.ID] && !s.runs[r.RunID] {
		return ErrUnknownRun
	}
	if s.receipts[pkg.ID] == nil {
		s.receipts[pkg.ID] = map[string]map[string]Receipt{}
	}
	if s.receipts[pkg.ID][r.RunID] == nil {
		s.receipts[pkg.ID][r.RunID] = map[string]Receipt{}
	}
	if _, dup := s.receipts[pkg.ID][r.RunID][r.CheckID]; dup {
		return ErrDuplicateReceipt
	}
	s.receipts[pkg.ID][r.RunID][r.CheckID] = r
	if s.archived[pkg.ID] {
		return ErrPackageArchived
	}
	return nil
}

// RevokeCheck revokes a check of the current frozen package, e.g. when
// its validator is found compromised. Existing receipts for the check
// stop counting and new ones are rejected; the gate cannot pass until a
// new package is frozen without the revoked check.
func (s *Scheduler) RevokeCheck(checkID string) error {
	if !s.hasCur {
		return ErrNoCurrentPackage
	}
	if _, ok := s.current.Spec(checkID); !ok {
		return ErrUnknownCheck
	}
	if s.revoked[s.current.ID] == nil {
		s.revoked[s.current.ID] = map[string]bool{}
	}
	s.revoked[s.current.ID][checkID] = true
	return nil
}

// Gate evaluates the release gate for one run of the current frozen
// package. It passes only if every check has a passing receipt bound to
// this run, this package, this input digest and its validator version.
func (s *Scheduler) Gate(runID string) (GateReport, error) {
	if !s.hasCur {
		return GateReport{}, ErrNoCurrentPackage
	}
	if !s.runs[runID] {
		return GateReport{}, ErrUnknownRun
	}
	rep := GateReport{
		PackageID: s.current.ID,
		RunID:     runID,
		Passed:    true,
		Checks:    map[string]CheckState{},
	}
	for _, spec := range s.current.Checks {
		state := CheckPending
		if s.revoked[s.current.ID][spec.ID] {
			state = CheckRevoked
		} else if r, ok := s.receipts[s.current.ID][runID][spec.ID]; ok {
			if r.Status == StatusPassed {
				state = CheckPassed
			} else {
				state = CheckFailed
			}
		}
		rep.Checks[spec.ID] = state
		if state != CheckPassed {
			rep.Passed = false
		}
	}
	return rep, nil
}

// ArchivedReceipt returns a receipt recorded against an archived
// package, for audit. The boolean is false if none was recorded.
func (s *Scheduler) ArchivedReceipt(pkgID, runID, checkID string) (Receipt, bool) {
	if !s.archived[pkgID] {
		return Receipt{}, false
	}
	r, ok := s.receipts[pkgID][runID][checkID]
	return r, ok
}

func (s *Scheduler) lookupPackage(pkgID string) (Package, error) {
	if s.hasCur && s.current.ID == pkgID {
		return s.current, nil
	}
	for _, p := range s.archive {
		if p.ID == pkgID {
			return p, nil
		}
	}
	return Package{}, ErrUnknownPackage
}
