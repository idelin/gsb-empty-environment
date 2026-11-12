package netsec

import (
	"errors"
	"fmt"
)

// Standard criteria used by the gate.
const (
	SafeToDeploy = "safe-to-deploy"
	SafeToRun    = "safe-to-run"
)

// AnyCriteria matches every criteria. It may appear only in violation
// records; a violation with no criteria is treated as AnyCriteria.
const AnyCriteria = "*"

// Verdict reasons returned by Store.Evaluate.
const (
	// ReasonViolation: the target version lies in a violation range.
	ReasonViolation = "violation"
	// ReasonReachable: a full-audit anchor reaches the target through
	// delta edges whose criteria cover the queried criteria.
	ReasonReachable = "reachable"
	// ReasonBrokenChain: delta edges exist around the target but no path
	// starts at a full audit, so the chain is anchored at an unaudited
	// version.
	ReasonBrokenChain = "broken-chain"
	// ReasonNoAudit: a full-audit anchor exists for the queried
	// criteria but no qualifying delta chain reaches the target.
	ReasonNoAudit = "no-audit"
	// ReasonUnknownCrate: the crate name is not present in the store.
	ReasonUnknownCrate = "unknown-crate"
)

var (
	// ErrUnknownCriteria is returned when an implication or audit uses a
	// criteria the store does not know.
	ErrUnknownCriteria = errors.New("netsec: unknown criteria")
	// ErrImplicationCycle is returned when adding an implication would
	// create a cycle such as A implies B and B implies A.
	ErrImplicationCycle = errors.New("netsec: criteria implication cycle")
	// ErrReservedCriteria is returned when the reserved wildcard is used
	// in an audit or implication record.
	ErrReservedCriteria = errors.New("netsec: reserved criteria")
)

// FullAudit anchors a version: the given version was fully reviewed for
// the criteria. Build metadata in Version is ignored for identity.
type FullAudit struct {
	Crate    string
	Version  string
	Criteria string
}

// DeltaAudit records that the jump From -> To was reviewed for the
// criteria, allowing versions reachable from a full audit to inherit its
// trust without a fresh full review.
type DeltaAudit struct {
	Crate    string
	From     string
	To       string
	Criteria string
}

// Violation marks every version inside Range as untrusted for Criteria.
// A violation always wins over audit evidence. An empty Criteria means
// the violation blocks every criteria (equivalent to AnyCriteria).
type Violation struct {
	Crate    string
	Range    string
	Criteria string
}

// Verdict explains a gate decision.
type Verdict struct {
	Crate    string
	Version  string
	Criteria string
	Allowed  bool
	Reason   string
	// Detail carries the rejecting violation range or the anchoring full
	// audit version that makes the target reachable.
	Detail string
}

type deltaEdge struct {
	from     string
	to       string
	criteria string
}

type parsedViolation struct {
	rangeExpr Range
	criteria  string
}

// Store holds audit evidence and criteria implications for a set of
// crates. A zero Store is not ready for use; create one with NewStore.
// Add methods are intended for a build/load phase and Evaluate for the
// query phase; the type is not safe for concurrent mutation.
type Store struct {
	fulls      map[string]map[string]map[string]bool
	deltas     map[string][]deltaEdge
	violations map[string][]parsedViolation
	implies    map[string]map[string]bool
	known      map[string]bool
	crates     map[string]bool
}

// NewStore creates an empty store seeded with the default implication
// safe-to-deploy -> safe-to-run: passing a deployment review proves the
// run review, but not the reverse.
func NewStore() *Store {
	s := &Store{
		fulls:      make(map[string]map[string]map[string]bool),
		deltas:     make(map[string][]deltaEdge),
		violations: make(map[string][]parsedViolation),
		implies:    make(map[string]map[string]bool),
		known:      make(map[string]bool),
		crates:     make(map[string]bool),
	}
	s.known[SafeToDeploy] = true
	s.known[SafeToRun] = true
	if err := s.AddImplication(SafeToDeploy, SafeToRun); err != nil {
		panic(err)
	}
	return s
}

// AddImplication records that evidence for stronger criteria also
// satisfies weaker criteria (e.g. safe-to-deploy implies safe-to-run).
// Implication is transitive. Adding the reverse edge of an existing
// implication returns ErrImplicationCycle.
func (s *Store) AddImplication(stronger, weaker string) error {
	if stronger == "" || weaker == "" {
		return fmt.Errorf("%w: empty criteria name", ErrUnknownCriteria)
	}
	if stronger == AnyCriteria || weaker == AnyCriteria {
		return ErrReservedCriteria
	}
	if stronger == weaker {
		return fmt.Errorf("%w: %s implies itself", ErrImplicationCycle, stronger)
	}
	if s.reachesExcludingSelf(weaker, stronger) {
		return fmt.Errorf("%w: adding %s -> %s closes a cycle", ErrImplicationCycle, stronger, weaker)
	}
	s.known[stronger] = true
	s.known[weaker] = true
	if s.implies[stronger] == nil {
		s.implies[stronger] = make(map[string]bool)
	}
	s.implies[stronger][weaker] = true
	return nil
}

// reachesExcludingSelf reports whether from can reach target through at
// least one implication edge.
func (s *Store) reachesExcludingSelf(from, target string) bool {
	for direct := range s.implies[from] {
		if direct == target || s.reaches(direct, target, nil) {
			return true
		}
	}
	return false
}

// reaches reports whether criteria from can reach target by following
// direct implication edges.
func (s *Store) reaches(from, target string, seen map[string]bool) bool {
	if from == target {
		return true
	}
	if seen == nil {
		seen = make(map[string]bool)
	}
	if seen[from] {
		return false
	}
	seen[from] = true
	for weaker := range s.implies[from] {
		if s.reaches(weaker, target, seen) {
			return true
		}
	}
	return false
}

// closure returns the transitive set of criteria satisfied by criteria,
// including criteria itself.
func (s *Store) closure(criteria string) map[string]bool {
	out := map[string]bool{criteria: true}
	queue := []string{criteria}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for weaker := range s.implies[cur] {
			if !out[weaker] {
				out[weaker] = true
				queue = append(queue, weaker)
			}
		}
	}
	return out
}

// AddFullAudit records a full (anchoring) audit.
func (s *Store) AddFullAudit(a FullAudit) error {
	if err := s.validateRecord(a.Crate, a.Criteria); err != nil {
		return err
	}
	s.crates[a.Crate] = true
	_, canonical, err := s.canonicalVersion(a.Version)
	if err != nil {
		return err
	}
	if s.fulls[a.Crate] == nil {
		s.fulls[a.Crate] = make(map[string]map[string]bool)
	}
	if s.fulls[a.Crate][canonical] == nil {
		s.fulls[a.Crate][canonical] = make(map[string]bool)
	}
	s.fulls[a.Crate][canonical][a.Criteria] = true
	return nil
}

// AddDeltaAudit records an incremental from -> to audit.
func (s *Store) AddDeltaAudit(a DeltaAudit) error {
	if err := s.validateRecord(a.Crate, a.Criteria); err != nil {
		return err
	}
	s.crates[a.Crate] = true
	_, from, err := s.canonicalVersion(a.From)
	if err != nil {
		return err
	}
	_, to, err := s.canonicalVersion(a.To)
	if err != nil {
		return err
	}
	s.deltas[a.Crate] = append(s.deltas[a.Crate], deltaEdge{
		from:     from,
		to:       to,
		criteria: a.Criteria,
	})
	return nil
}

// AddViolation records a semver range whose versions are rejected.
// An empty Criteria blocks every criteria.
func (s *Store) AddViolation(v Violation) error {
	if v.Crate == "" {
		return errors.New("netsec: violation requires a crate name")
	}
	s.crates[v.Crate] = true
	criteria := v.Criteria
	if criteria == "" {
		criteria = AnyCriteria
	}
	if criteria != AnyCriteria {
		if err := s.validateRecord(v.Crate, criteria); err != nil {
			return err
		}
	} else {
		s.known[AnyCriteria] = false
	}
	rangeExpr, err := ParseRange(v.Range)
	if err != nil {
		return err
	}
	s.violations[v.Crate] = append(s.violations[v.Crate], parsedViolation{
		rangeExpr: rangeExpr,
		criteria:  criteria,
	})
	return nil
}

// Record is a generic audit record for bulk loading. Exactly one of the
// embedded record pointers must be non-nil.
type Record struct {
	Full  *FullAudit
	Delta *DeltaAudit
	Viol  *Violation
}

// AddRecords applies a batch of records and returns the first error.
func (s *Store) AddRecords(records ...Record) error {
	for _, rec := range records {
		switch {
		case rec.Full != nil:
			if err := s.AddFullAudit(*rec.Full); err != nil {
				return err
			}
		case rec.Delta != nil:
			if err := s.AddDeltaAudit(*rec.Delta); err != nil {
				return err
			}
		case rec.Viol != nil:
			if err := s.AddViolation(*rec.Viol); err != nil {
				return err
			}
		default:
			return errors.New("netsec: empty audit record")
		}
	}
	return nil
}

func (s *Store) validateRecord(crate, criteria string) error {
	if crate == "" {
		return errors.New("netsec: audit record requires a crate name")
	}
	if criteria == "" {
		return fmt.Errorf("%w: empty criteria name", ErrUnknownCriteria)
	}
	if criteria == AnyCriteria {
		return ErrReservedCriteria
	}
	if !s.known[criteria] {
		return fmt.Errorf("%w: %q", ErrUnknownCriteria, criteria)
	}
	return nil
}

func (s *Store) canonicalVersion(raw string) (Version, string, error) {
	ver, err := ParseVersion(raw)
	if err != nil {
		return Version{}, "", err
	}
	return ver, ver.Canonical(), nil
}

// Evaluate decides whether crate version passes the given criteria.
//
// The decision rules are:
//   - A violation range containing the version rejects it outright, even
//     when a valid audit chain reaches it. A violation for criteria C
//     blocks query Q when Q implies C; wildcard violations block all.
//   - Otherwise the version passes when a full audit anchors some
//     version for a criteria covering Q, and delta edges whose criteria
//     also cover Q form a directed path from that anchor to the target.
//   - A delta chain whose start was never fully audited is a broken
//     chain and does not pass.
func (s *Store) Evaluate(crate, version, criteria string) (Verdict, error) {
	if criteria == "" || criteria == AnyCriteria {
		return Verdict{}, fmt.Errorf("%w: %q", ErrUnknownCriteria, criteria)
	}
	if !s.known[criteria] {
		return Verdict{}, fmt.Errorf("%w: %q", ErrUnknownCriteria, criteria)
	}
	targetVer, err := ParseVersion(version)
	if err != nil {
		return Verdict{}, err
	}
	target := targetVer.Canonical()

	verdict := Verdict{Crate: crate, Version: target, Criteria: criteria}
	if !s.crates[crate] {
		verdict.Reason = ReasonUnknownCrate
		return verdict, nil
	}

	// Violation takes precedence over any audit evidence.
	if detail, blocked := s.violatedBy(crate, targetVer, criteria); blocked {
		verdict.Allowed = false
		verdict.Reason = ReasonViolation
		verdict.Detail = detail
		return verdict, nil
	}

	// Seed the frontier with full audits whose criteria cover the query.
	visited := make(map[string]bool)
	var anchors []string
	for ver, criteriaSet := range s.fulls[crate] {
		for audited := range criteriaSet {
			if s.covers(audited, criteria) {
				if !visited[ver] {
					visited[ver] = true
					anchors = append(anchors, ver)
				}
			}
		}
	}

	// Walk delta edges forward. An edge carries the query only when its
	// own criteria cover it, so a chain mixing unrelated criteria breaks.
	frontier := anchors
	for len(frontier) > 0 {
		cur := frontier[0]
		frontier = frontier[1:]
		if cur == target {
			verdict.Allowed = true
			verdict.Reason = ReasonReachable
			verdict.Detail = cur
			return verdict, nil
		}
		for _, edge := range s.deltas[crate] {
			if edge.from != cur || visited[edge.to] {
				continue
			}
			if !s.covers(edge.criteria, criteria) {
				continue
			}
			visited[edge.to] = true
			frontier = append(frontier, edge.to)
		}
	}

	if len(anchors) == 0 {
		verdict.Reason = ReasonBrokenChain
	} else {
		verdict.Reason = ReasonNoAudit
	}
	return verdict, nil
}

// covers reports whether evidence for evidenceCriteria satisfies a
// query for queryCriteria, i.e. the query is in the transitive
// implication closure of the evidence criteria (or equal to it).
func (s *Store) covers(evidenceCriteria, queryCriteria string) bool {
	return s.closure(evidenceCriteria)[queryCriteria]
}

// violatedBy returns the raw range expression of the first matching
// violation, if any.
func (s *Store) violatedBy(crate string, target Version, criteria string) (string, bool) {
	for _, v := range s.violations[crate] {
		if v.criteria != AnyCriteria && !s.covers(criteria, v.criteria) {
			continue
		}
		if v.rangeExpr.Contains(target) {
			return v.rangeExpr.String(), true
		}
	}
	return "", false
}

// IsAllowed is a convenience wrapper returning only the boolean decision.
func (s *Store) IsAllowed(crate, version, criteria string) (bool, error) {
	v, err := s.Evaluate(crate, version, criteria)
	if err != nil {
		return false, err
	}
	return v.Allowed, nil
}
