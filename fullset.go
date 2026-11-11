package netsec

import (
	"sort"
	"sync"
)

// Audit issue kinds reported by the full-set reference.
const (
	// AuditDuplicateDelivery: a small-sequence record was delivered more than
	// once with identical content (a duplicate commitment).
	AuditDuplicateDelivery = "duplicate-delivery"
	// AuditContentConflict: the same sequence was delivered with different
	// content.
	AuditContentConflict = "content-conflict"
	// AuditPresentMismatch: the ledger's present set disagrees with the
	// reference's full-set ground truth.
	AuditPresentMismatch = "present-mismatch"
	// AuditMissingMismatch: the ledger's confirmed-missing intervals disagree
	// with the reference-derived gaps.
	AuditMissingMismatch = "missing-mismatch"
	// AuditCommitNotMonotonic: the ledger's issuance trail diverges from the
	// reference (a boundary violation).
	AuditCommitNotMonotonic = "commit-not-monotonic"
	// AuditStaleIssuance: a received watermark regressed relative to the
	// previously observed issuance.
	AuditStaleIssuance = "stale-issuance"
	// AuditRedundantIssuance: the same high was issued more than once.
	AuditRedundantIssuance = "redundant-issuance"
	// AuditEpochMissing: the reference observed traffic for an epoch the
	// ledger does not contain.
	AuditEpochMissing = "epoch-missing"
	// AuditIncompleteFullSet: the watermark already covers the full-set
	// universe [1, limit], but some small sequences were never delivered.
	AuditIncompleteFullSet = "incomplete-full-set"
)

// AuditIssue is one finding produced by the full-set audit.
type AuditIssue struct {
	Kind   string
	Probe  ProbeID
	Gen    Gen
	Seq    Seq
	Detail string
}

// EpochRef is the reference's full-set view of one generation.
type EpochRef struct {
	Key            EpochKey
	High           Seq
	Commits        []Seq
	Present        map[Seq]ContentHash
	DeliveryCount  map[Seq]int
	Conflicts      map[Seq]struct{}
	Complete       bool
	MissingFullSet []Interval
}

// AuditReport summarizes one full-set audit run.
type AuditReport struct {
	Enabled bool
	Limit   Seq
	Epochs  []EpochRef
	Issues  []AuditIssue
}

// refEpoch is the reference's internal per-generation ground truth.
type refEpoch struct {
	high    Seq
	commits []Seq

	// present holds the first content observed for each small sequence.
	present map[Seq]ContentHash
	// deliveries counts every delivery of each small sequence.
	deliveries map[Seq]int
	// conflicts holds sequences whose content disagreed with the first seen.
	conflicts map[Seq]struct{}

	staleCount     int
	redundantCount int
}

// FullSetReference retains the complete delivery/issuance history for the
// small sequence universe [1, limit] of every generation. It is built
// independently of the ledger's gap accounting, so AuditLedger re-derives the
// truth and checks duplicate commitments, content conflicts and boundaries.
type FullSetReference struct {
	mu     sync.Mutex
	limit  Seq
	epochs map[EpochKey]*refEpoch
}

// NewFullSetReference creates a reference covering sequences 1..limit.
func NewFullSetReference(limit Seq) (*FullSetReference, error) {
	if limit < 1 {
		return nil, ErrLimitRange
	}
	return &FullSetReference{
		limit:  limit,
		epochs: make(map[EpochKey]*refEpoch),
	}, nil
}

// Limit returns the full-set universe upper bound.
func (r *FullSetReference) Limit() Seq {
	return r.limit
}

// ObserveRecord feeds one delivered record into the reference. Records beyond
// the small-sequence universe are accepted but not retained.
func (r *FullSetReference) ObserveRecord(probe ProbeID, gen Gen, seq Seq, hash ContentHash) error {
	if probe == "" {
		return ErrZeroProbeID
	}
	if gen == 0 {
		return ErrZeroGen
	}
	if seq == 0 {
		return ErrZeroSeq
	}
	if len(hash) == 0 {
		return ErrEmptyHash
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if seq > r.limit {
		return nil
	}
	r.observeRecord(probe, gen, seq, append(ContentHash(nil), hash...))
	return nil
}

func (r *FullSetReference) observeRecord(probe ProbeID, gen Gen, seq Seq, hash ContentHash) {
	epoch := r.ensureEpoch(probe, gen)
	epoch.deliveries[seq]++

	existing, ok := epoch.present[seq]
	if !ok {
		epoch.present[seq] = hash
		return
	}
	if !hashEqual(existing, hash) {
		epoch.conflicts[seq] = struct{}{}
	}
}

// ObserveCommit feeds one probe-issued watermark into the reference.
func (r *FullSetReference) ObserveCommit(probe ProbeID, gen Gen, high Seq) error {
	if probe == "" {
		return ErrZeroProbeID
	}
	if gen == 0 {
		return ErrZeroGen
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.observeCommit(probe, gen, high)
	return nil
}

func (r *FullSetReference) observeCommit(probe ProbeID, gen Gen, high Seq) {
	epoch := r.ensureEpoch(probe, gen)
	switch {
	case high < epoch.high:
		epoch.staleCount++
	case high == epoch.high:
		epoch.redundantCount++
	default:
		epoch.high = high
		epoch.commits = append(epoch.commits, high)
	}
}

func (r *FullSetReference) ensureEpoch(probe ProbeID, gen Gen) *refEpoch {
	key := EpochKey{probe, gen}
	if epoch, ok := r.epochs[key]; ok {
		return epoch
	}
	epoch := &refEpoch{
		present:    make(map[Seq]ContentHash),
		deliveries: make(map[Seq]int),
		conflicts:  make(map[Seq]struct{}),
	}
	r.epochs[key] = epoch
	return epoch
}

// ReferenceComplete reports whether the reference's full-set universe for one
// generation is fully covered by an issuance and every small sequence has at
// least one delivery.
func (r *FullSetReference) ReferenceComplete(key EpochKey) (EpochRef, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	epoch, ok := r.epochs[key]
	if !ok {
		return EpochRef{}, false
	}
	view := r.snapshot(key, epoch)
	return view, view.Complete && len(view.MissingFullSet) == 0
}

func (r *FullSetReference) snapshot(key EpochKey, epoch *refEpoch) EpochRef {
	present := make(map[Seq]ContentHash, len(epoch.present))
	for seq, hash := range epoch.present {
		present[seq] = append(ContentHash(nil), hash...)
	}
	deliveryCount := make(map[Seq]int, len(epoch.deliveries))
	for seq, count := range epoch.deliveries {
		deliveryCount[seq] = count
	}
	conflicts := make(map[Seq]struct{}, len(epoch.conflicts))
	for seq := range epoch.conflicts {
		conflicts[seq] = struct{}{}
	}

	view := EpochRef{
		Key:           key,
		High:          epoch.high,
		Commits:       append([]Seq(nil), epoch.commits...),
		Present:       present,
		DeliveryCount: deliveryCount,
		Conflicts:     conflicts,
	}
	if epoch.high >= r.limit {
		view.Complete = true
		view.MissingFullSet = invertPoints(1, r.limit, sortedKeys(epoch.present))
	}
	return view
}

// AuditLedger re-derives the ledger state for the small-sequence universe and
// checks it against the independently observed full-set ground truth. The
// ledger is locked first and the reference lock second, matching the lock
// order used by the ledger ingest/commit paths.
func (r *FullSetReference) AuditLedger(l *Ledger) AuditReport {
	report := AuditReport{Enabled: true, Limit: r.limit}

	l.mu.Lock()
	defer l.mu.Unlock()

	r.mu.Lock()
	defer r.mu.Unlock()

	keys := make([]EpochKey, 0, len(r.epochs))
	for key := range r.epochs {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Probe != keys[j].Probe {
			return keys[i].Probe < keys[j].Probe
		}
		return keys[i].Gen < keys[j].Gen
	})

	for _, key := range keys {
		refEpochState := r.epochs[key]
		view := r.snapshot(key, refEpochState)
		report.Epochs = append(report.Epochs, view)

		// Reference-side duplicate commitments and content conflicts.
		for seq, count := range refEpochState.deliveries {
			if count > 1 {
				report.Issues = append(report.Issues, AuditIssue{
					Kind:   AuditDuplicateDelivery,
					Probe:  key.Probe,
					Gen:    key.Gen,
					Seq:    seq,
					Detail: "small-sequence record delivered more than once",
				})
			}
		}
		for seq := range refEpochState.conflicts {
			report.Issues = append(report.Issues, AuditIssue{
				Kind:   AuditContentConflict,
				Probe:  key.Probe,
				Gen:    key.Gen,
				Seq:    seq,
				Detail: "retransmitted content disagrees with first delivery",
			})
		}
		if refEpochState.staleCount > 0 {
			report.Issues = append(report.Issues, AuditIssue{
				Kind:   AuditStaleIssuance,
				Probe:  key.Probe,
				Gen:    key.Gen,
				Detail: "watermark issuance regressed",
			})
		}
		if refEpochState.redundantCount > 0 {
			report.Issues = append(report.Issues, AuditIssue{
				Kind:   AuditRedundantIssuance,
				Probe:  key.Probe,
				Gen:    key.Gen,
				Detail: "same watermark high issued more than once",
			})
		}

		ledgerEpoch, ok := l.epochs[key]
		if !ok {
			report.Issues = append(report.Issues, AuditIssue{
				Kind:   AuditEpochMissing,
				Probe:  key.Probe,
				Gen:    key.Gen,
				Detail: "reference observed traffic for an epoch absent from ledger",
			})
			continue
		}

		// Boundary check: the ledger issuance trail must match the
		// independently observed one (both strictly increasing).
		if !sortedSeqsEqual(ledgerEpoch.commits, refEpochState.commits) {
			report.Issues = append(report.Issues, AuditIssue{
				Kind:   AuditCommitNotMonotonic,
				Probe:  key.Probe,
				Gen:    key.Gen,
				Detail: "ledger issuance trail diverges from reference",
			})
		}
		if ledgerEpoch.high != refEpochState.high {
			report.Issues = append(report.Issues, AuditIssue{
				Kind:   AuditCommitNotMonotonic,
				Probe:  key.Probe,
				Gen:    key.Gen,
				Detail: "ledger watermark disagrees with reference high",
			})
		}

		// Present-set check over the small-sequence universe.
		refPresentSmall := make(map[Seq]ContentHash)
		for seq, hash := range refEpochState.present {
			if seq <= r.limit {
				refPresentSmall[seq] = hash
			}
		}
		ledgerPresentSmall := make(map[Seq]ContentHash)
		for seq, hash := range ledgerEpoch.present {
			if seq <= r.limit {
				ledgerPresentSmall[seq] = hash
			}
		}
		if !presentMapsEqual(refPresentSmall, ledgerPresentSmall) {
			report.Issues = append(report.Issues, AuditIssue{
				Kind:   AuditPresentMismatch,
				Probe:  key.Probe,
				Gen:    key.Gen,
				Detail: "ledger present set disagrees with full-set reference",
			})
		}

		// Missing-set check: both must derive identical gaps inside the
		// issuance-covered portion of the universe.
		covered := refEpochState.high
		if covered > r.limit {
			covered = r.limit
		}
		if covered > 0 {
			refMissing := invertPoints(1, covered, sortedKeys(refPresentSmall))
			ledgerMissing := clipIntervals(ledgerEpoch.confirmedMissing(), 1, covered)
			if !intervalsEqual(refMissing, ledgerMissing) {
				report.Issues = append(report.Issues, AuditIssue{
					Kind:   AuditMissingMismatch,
					Probe:  key.Probe,
					Gen:    key.Gen,
					Detail: "confirmed-missing intervals disagree with reference gaps",
				})
			}
		}

		// Full-set completeness: once an issuance covers [1, limit], the
		// reference expects every small sequence to have been delivered.
		if view.Complete && len(view.MissingFullSet) > 0 {
			report.Issues = append(report.Issues, AuditIssue{
				Kind:   AuditIncompleteFullSet,
				Probe:  key.Probe,
				Gen:    key.Gen,
				Detail: "full-set universe covered but some small sequences never delivered",
			})
		}
	}

	return report
}

func sortedSeqsEqual(a, b []Seq) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func presentMapsEqual(a, b map[Seq]ContentHash) bool {
	if len(a) != len(b) {
		return false
	}
	for seq, hash := range a {
		other, ok := b[seq]
		if !ok || !hashEqual(hash, other) {
			return false
		}
	}
	return true
}

// clipIntervals restricts inclusive intervals to the universe [lo, hi].
func clipIntervals(intervals []Interval, lo, hi Seq) []Interval {
	var out []Interval
	for _, in := range intervals {
		if in.To < lo || in.From > hi {
			continue
		}
		from := in.From
		if from < lo {
			from = lo
		}
		to := in.To
		if to > hi {
			to = hi
		}
		out = append(out, Interval{From: from, To: to})
	}
	return out
}

func intervalsEqual(a, b []Interval) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
