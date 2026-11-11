package netsec

import (
	"errors"
	"sort"
	"sync"
)

// ProbeID identifies a reporting probe.
type ProbeID string

// Gen is the boot generation (epoch) of a probe. Every probe restart starts a
// new generation; sequence spaces of different generations must never be mixed.
// Valid generations start at 1.
type Gen uint64

// Seq is a monotonically increasing record sequence number within one
// generation. Valid sequence numbers start at 1.
type Seq uint64

// ContentHash identifies record payload contents. Two records carrying the
// same (probe, generation, sequence) but different hashes are content
// conflicts and are never silently overwritten.
type ContentHash []byte

// Validation errors returned by ledger and reference methods.
var (
	ErrZeroProbeID   = errors.New("netsec: probe id must not be empty")
	ErrZeroGen       = errors.New("netsec: generation must be >= 1")
	ErrZeroSeq       = errors.New("netsec: sequence must be >= 1")
	ErrEmptyHash     = errors.New("netsec: content hash must not be empty")
	ErrReversedRange = errors.New("netsec: range query requires from <= to")
	ErrUnknownEpoch  = errors.New("netsec: no ledger state for probe/generation")
	ErrLimitRange    = errors.New("netsec: full-set limit must be >= 1")
)

// EpochKey uniquely identifies one boot generation of one probe.
type EpochKey struct {
	Probe ProbeID
	Gen   Gen
}

// Record is one delivered (possibly retransmitted or out-of-order) security
// log record.
type Record struct {
	Probe ProbeID
	Gen   Gen
	Seq   Seq
	Hash  ContentHash
}

// Watermark is the highest record sequence number a probe has generated in a
// generation, issued by the probe itself. Only gaps covered by a committed
// watermark may be declared confirmed missing.
type Watermark struct {
	Probe ProbeID
	Gen   Gen
	High  Seq
}

// Interval is an inclusive integer range [From, To] of sequence numbers.
// From <= To always holds.
type Interval struct {
	From Seq
	To   Seq
}

// Delivery outcomes for a received record.
const (
	// IngestAccepted: a new record was stored.
	IngestAccepted = "accepted"
	// IngestDuplicate: the record was already present with the same content
	// (a retransmission); it never counts as missing or as a new record.
	IngestDuplicate = "duplicate"
	// IngestConflict: the sequence was already present carrying different
	// content; the original record is retained.
	IngestConflict = "conflict"
)

// IngestResult reports the disposition of one Record.
type IngestResult struct {
	Outcome string
	// FilledMissing is true when an accepted record lands on a sequence that
	// had previously been confirmed missing (a retransmission repair).
	FilledMissing bool
	// ConfirmedUnder is the watermark under which the sequence was confirmed
	// missing, meaningful only when FilledMissing is true.
	ConfirmedUnder Seq
	// Correction is appended to the generation correction log when
	// FilledMissing is true, otherwise it is nil.
	Correction *Correction
}

// Commit outcomes for a received watermark.
const (
	// CommitAdvanced: the watermark moved forward.
	CommitAdvanced = "advanced"
	// CommitRedundant: the same high had already been committed.
	CommitRedundant = "redundant"
	// CommitStale: the watermark went backwards and was rejected.
	CommitStale = "stale"
)

// CommitResult reports the disposition of one Watermark.
type CommitResult struct {
	Outcome string
	High    Seq
	// NewlyConfirmed lists gaps that this commit newly confirms as missing.
	NewlyConfirmed []Interval
}

// Correction permanently records a confirmed-missing sequence that was later
// repaired by a retransmission. Corrections are append-only.
type Correction struct {
	Probe          ProbeID
	Gen            Gen
	Seq            Seq
	Hash           ContentHash
	ConfirmedUnder Seq
	Watermark      Seq
	Event          uint64
}

// RangeStatus categorizes one interval returned by a historical range query.
const (
	// RangePresent: the sequence has a received record.
	RangePresent = "present"
	// RangeMissing: the sequence is a gap covered by a committed watermark,
	// hence confirmed missing.
	RangeMissing = "missing"
	// RangePending: no record yet, but no watermark covers the sequence, so
	// loss is not provable.
	RangePending = "pending"
)

// RangeReport answers a historical interval query on one generation.
type RangeReport struct {
	Probe     ProbeID
	Gen       Gen
	From      Seq
	To        Seq
	Watermark Seq
	Present   []Interval
	Missing   []Interval
	Pending   []Interval
}

// EpochReport is the per-generation slice element of a probe-wide query.
type EpochReport struct {
	Gen       Gen
	Watermark Seq
	Present   []Interval
	Missing   []Interval
	Pending   []Interval
}

// epochLedger holds all state for one (probe, generation). Its methods run
// without their own locking; the registry serializes access.
type epochLedger struct {
	probe ProbeID
	gen   Gen

	// present maps sequence number to its content hash.
	present map[Seq]ContentHash
	// high is the latest committed watermark (0 when nothing committed yet).
	high Seq
	// commits remembers every distinct advanced watermark in increasing order;
	// it also provides the audit trail used for boundary verification.
	commits []Seq

	// conflictAt records sequences whose retransmitted content disagreed with
	// the stored content.
	conflictAt map[Seq]struct{}
	// conflictCount counts conflicting deliveries (including repeats).
	conflictCount int
	// duplicateCount counts redundant deliveries of already-present content.
	duplicateCount int

	corrections []Correction
}

func newEpochLedger(probe ProbeID, gen Gen) *epochLedger {
	return &epochLedger{
		probe:      probe,
		gen:        gen,
		present:    make(map[Seq]ContentHash),
		conflictAt: make(map[Seq]struct{}),
	}
}

func (e *epochLedger) ingest(seq Seq, hash ContentHash, event uint64) IngestResult {
	if existing, ok := e.present[seq]; ok {
		if hashEqual(existing, hash) {
			e.duplicateCount++
			return IngestResult{Outcome: IngestDuplicate}
		}
		e.conflictCount++
		e.conflictAt[seq] = struct{}{}
		return IngestResult{Outcome: IngestConflict}
	}

	e.present[seq] = hash
	if seq > e.high {
		return IngestResult{Outcome: IngestAccepted}
	}

	// The committed watermark already covers this sequence: it was a
	// confirmed-missing gap that is now being repaired.
	confirmedUnder := e.confirmingWatermark(seq)
	correction := &Correction{
		Probe:          e.probe,
		Gen:            e.gen,
		Seq:            seq,
		Hash:           append(ContentHash(nil), hash...),
		ConfirmedUnder: confirmedUnder,
		Watermark:      e.high,
		Event:          event,
	}
	e.corrections = append(e.corrections, *correction)
	return IngestResult{
		Outcome:        IngestAccepted,
		FilledMissing:  true,
		ConfirmedUnder: confirmedUnder,
		Correction:     correction,
	}
}

// confirmingWatermark returns the smallest committed watermark that covered
// seq, i.e. the issuance under which seq was first provably missing.
func (e *epochLedger) confirmingWatermark(seq Seq) Seq {
	idx := sort.Search(len(e.commits), func(i int) bool { return e.commits[i] >= seq })
	if idx < len(e.commits) {
		return e.commits[idx]
	}
	return e.high
}

func (e *epochLedger) commit(high Seq) CommitResult {
	switch {
	case high < e.high:
		return CommitResult{Outcome: CommitStale, High: e.high}
	case high == e.high:
		return CommitResult{Outcome: CommitRedundant, High: e.high}
	}

	previous := e.high
	e.high = high
	e.commits = append(e.commits, high)

	return CommitResult{
		Outcome:        CommitAdvanced,
		High:           high,
		NewlyConfirmed: e.gapsBetween(safeNext(previous), high),
	}
}

// gapsBetween returns the confirmed-missing intervals within [from, to],
// where to is assumed covered by a committed watermark.
func (e *epochLedger) gapsBetween(from, to Seq) []Interval {
	if from > to {
		return nil
	}
	points := make([]Seq, 0)
	for seq := range e.present {
		if seq >= from && seq <= to {
			points = append(points, seq)
		}
	}
	sort.Slice(points, func(i, j int) bool { return points[i] < points[j] })
	return invertPoints(from, to, points)
}

// confirmedMissing returns all currently missing intervals under the
// committed watermark (i.e. [1, high] minus present sequences).
func (e *epochLedger) confirmedMissing() []Interval {
	if e.high == 0 {
		return nil
	}
	return e.gapsBetween(1, e.high)
}

func (e *epochLedger) presentIntervals() []Interval {
	return pointsToIntervals(sortedKeys(e.present))
}

func (e *epochLedger) query(from, to Seq) RangeReport {
	present := make(map[Seq]struct{})
	for seq := range e.present {
		if seq >= from && seq <= to {
			present[seq] = struct{}{}
		}
	}

	var missingPoints, pendingPoints []Seq
	for seq := from; ; seq++ {
		if _, ok := present[seq]; ok {
			if seq == to {
				break
			}
			continue
		}
		if seq <= e.high {
			missingPoints = append(missingPoints, seq)
		} else {
			pendingPoints = append(pendingPoints, seq)
		}
		if seq == to {
			break
		}
	}

	return RangeReport{
		Probe:     e.probe,
		Gen:       e.gen,
		From:      from,
		To:        to,
		Watermark: e.high,
		Present:   pointsToIntervals(sortedKeys(present)),
		Missing:   pointsToIntervals(missingPoints),
		Pending:   pointsToIntervals(pendingPoints),
	}
}

// Ledger is the concurrency-safe interval ledger a Go receiving host calls.
// State is strictly partitioned per probe and per boot generation.
type Ledger struct {
	mu sync.Mutex

	epochs map[EpochKey]*epochLedger
	// gens keeps each probe's known generations sorted ascending.
	gens map[ProbeID][]Gen

	// ref, when non-nil, is fed a copy of every small-sequence delivery and
	// every watermark issuance for independent full-set auditing.
	ref *FullSetReference

	// event is a registry-wide monotonic counter ordering corrections and
	// audit observations.
	event uint64
}

// NewLedger creates an empty ledger without a full-set reference.
func NewLedger() *Ledger {
	return &Ledger{
		epochs: make(map[EpochKey]*epochLedger),
		gens:   make(map[ProbeID][]Gen),
	}
}

// NewLedgerWithReference creates a ledger whose small-sequence deliveries and
// watermark issuances are mirrored into a full-set reference. The reference
// retains complete ground truth for sequences up to limit and powers
// duplicate-commitment, content-conflict and boundary audits.
func NewLedgerWithReference(limit Seq) (*Ledger, error) {
	ref, err := NewFullSetReference(limit)
	if err != nil {
		return nil, err
	}
	ledger := NewLedger()
	ledger.ref = ref
	return ledger, nil
}

// Reference returns the configured full-set reference, or nil.
func (l *Ledger) Reference() *FullSetReference {
	return l.ref
}

// IngestRecord accepts a delivered record. Out-of-order delivery is fine; a
// retransmission of identical content is reported as a duplicate and never
// counted as missing; conflicting content is rejected while the original is
// retained. Generations are isolated: records are never merged across boots.
func (l *Ledger) IngestRecord(rec Record) (IngestResult, error) {
	if rec.Probe == "" {
		return IngestResult{}, ErrZeroProbeID
	}
	if rec.Gen == 0 {
		return IngestResult{}, ErrZeroGen
	}
	if rec.Seq == 0 {
		return IngestResult{}, ErrZeroSeq
	}
	if len(rec.Hash) == 0 {
		return IngestResult{}, ErrEmptyHash
	}

	hash := append(ContentHash(nil), rec.Hash...)

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.ref != nil && rec.Seq <= l.ref.limit {
		l.ref.observeRecord(rec.Probe, rec.Gen, rec.Seq, hash)
	}

	l.event++
	epoch := l.ensureEpoch(rec.Probe, rec.Gen)
	return epoch.ingest(rec.Seq, hash, l.event), nil
}

// CommitWatermark records a probe-issued highest-generated sequence number.
// Only gaps covered by an advanced watermark become confirmed missing. A
// stale (regressing) issuance is rejected; a repeated issuance is redundant.
func (l *Ledger) CommitWatermark(wm Watermark) (CommitResult, error) {
	if wm.Probe == "" {
		return CommitResult{}, ErrZeroProbeID
	}
	if wm.Gen == 0 {
		return CommitResult{}, ErrZeroGen
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.ref != nil {
		l.ref.observeCommit(wm.Probe, wm.Gen, wm.High)
	}

	epoch := l.ensureEpoch(wm.Probe, wm.Gen)
	return epoch.commit(wm.High), nil
}

// QueryRange answers a historical interval query for one generation.
func (l *Ledger) QueryRange(probe ProbeID, gen Gen, from, to Seq) (RangeReport, error) {
	if probe == "" {
		return RangeReport{}, ErrZeroProbeID
	}
	if gen == 0 {
		return RangeReport{}, ErrZeroGen
	}
	if from == 0 {
		return RangeReport{}, ErrZeroSeq
	}
	if from > to {
		return RangeReport{}, ErrReversedRange
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	epoch, ok := l.epochs[EpochKey{probe, gen}]
	if !ok {
		return RangeReport{}, ErrUnknownEpoch
	}
	return epoch.query(from, to), nil
}

// QueryProbe answers historical interval queries for every known generation
// of a probe. Generations are reported separately and never merged.
func (l *Ledger) QueryProbe(probe ProbeID) ([]EpochReport, error) {
	if probe == "" {
		return nil, ErrZeroProbeID
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	gens, ok := l.gens[probe]
	if !ok {
		return nil, ErrUnknownEpoch
	}

	reports := make([]EpochReport, 0, len(gens))
	for _, gen := range gens {
		epoch := l.epochs[EpochKey{probe, gen}]
		var pending []Interval
		if epoch.high > 0 {
			pending = invertAbove(epoch.high, epoch.present)
		} else {
			pending = epoch.presentIntervals()
		}
		reports = append(reports, EpochReport{
			Gen:       gen,
			Watermark: epoch.high,
			Present:   epoch.presentIntervals(),
			Missing:   epoch.confirmedMissing(),
			Pending:   pending,
		})
	}
	return reports, nil
}

// Watermark returns the latest committed watermark for a generation.
func (l *Ledger) Watermark(probe ProbeID, gen Gen) (Seq, error) {
	if probe == "" {
		return 0, ErrZeroProbeID
	}
	if gen == 0 {
		return 0, ErrZeroGen
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	epoch, ok := l.epochs[EpochKey{probe, gen}]
	if !ok {
		return 0, ErrUnknownEpoch
	}
	return epoch.high, nil
}

// Generations returns a sorted copy of the known generations of a probe.
func (l *Ledger) Generations(probe ProbeID) ([]Gen, error) {
	if probe == "" {
		return nil, ErrZeroProbeID
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	gens, ok := l.gens[probe]
	if !ok {
		return nil, ErrUnknownEpoch
	}
	out := make([]Gen, len(gens))
	copy(out, gens)
	return out, nil
}

// Corrections returns the append-only correction log for a generation: every
// retransmission that repaired a previously confirmed-missing sequence.
func (l *Ledger) Corrections(probe ProbeID, gen Gen) ([]Correction, error) {
	if probe == "" {
		return nil, ErrZeroProbeID
	}
	if gen == 0 {
		return nil, ErrZeroGen
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	epoch, ok := l.epochs[EpochKey{probe, gen}]
	if !ok {
		return nil, ErrUnknownEpoch
	}
	out := make([]Correction, len(epoch.corrections))
	copy(out, epoch.corrections)
	return out, nil
}

// Audit re-derives the ledger's small-sequence truth from the full-set
// reference and checks duplicate commitments, content conflicts and
// boundaries. It returns ErrLimitRange-equivalent error when no reference is
// configured.
func (l *Ledger) Audit() (AuditReport, error) {
	if l.ref == nil {
		return AuditReport{}, errors.New("netsec: ledger has no full-set reference")
	}
	return l.ref.AuditLedger(l), nil
}

// ensureEpoch returns the ledger for (probe, gen), creating it on first use.
// Caller must hold l.mu.
func (l *Ledger) ensureEpoch(probe ProbeID, gen Gen) *epochLedger {
	key := EpochKey{probe, gen}
	if epoch, ok := l.epochs[key]; ok {
		return epoch
	}

	epoch := newEpochLedger(probe, gen)
	l.epochs[key] = epoch

	gens := l.gens[probe]
	idx := sort.Search(len(gens), func(i int) bool { return gens[i] >= gen })
	if idx == len(gens) {
		l.gens[probe] = append(gens, gen)
	} else {
		gens = append(gens, 0)
		copy(gens[idx+1:], gens[idx:])
		gens[idx] = gen
		l.gens[probe] = gens
	}
	return epoch
}
