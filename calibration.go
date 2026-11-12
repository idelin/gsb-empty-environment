// Package taskpkg implements calibration mapping for a time-series ingestion
// host.
//
// Raw sensor samples are immutable and addressed by (device instance, sample
// sequence number). Calibrations are rational affine transforms
// (value*scale + offset) that are active over a half-open effective interval
// [EffectiveFrom, EffectiveUntil). When calibration parameters arrive late,
// only the samples whose sample time intersects the new interval are
// recomputed. Overlapping calibrations resolve to the greatest explicit
// version; identical versions carrying different parameters are rejected.
//
// Recomputation never overwrites the raw reading. Each change appends a
// revision event that carries the original evidence (a snapshot of the raw
// sample and of the calibration that produced the value), so every materialized
// value stays auditable back to its source reading.
package taskpkg

import (
	"errors"
	"fmt"
	"math/big"
	"sort"
	"sync"
	"time"
)

// Reason describes why a revision event was emitted.
type Reason string

const (
	// ReasonInitial is the first materialization of a raw sample.
	ReasonInitial Reason = "initial"
	// ReasonCalibrationArrived means a late calibration changed a value.
	ReasonCalibrationArrived Reason = "calibration_arrived"
	// ReasonCalibrationWithdrawn means a calibration was withdrawn and the
	// sample reverted to another calibration or to the identity mapping.
	ReasonCalibrationWithdrawn Reason = "calibration_withdrawn"
)

var (
	// ErrSampleConflict is returned when an existing (device, sequence) pair
	// is re-inserted with a different reading.
	ErrSampleConflict = errors.New("taskpkg: conflicting raw sample for fixed device/sequence")
	// ErrInvalidCalibration is returned when a sample or calibration is
	// structurally invalid (unknown device, empty interval, nil/non-finite
	// coefficients).
	ErrInvalidCalibration = errors.New("taskpkg: invalid calibration")
	// ErrVersionConflict is returned when an existing version is re-submitted
	// with different parameters, or when the version was already withdrawn.
	ErrVersionConflict = errors.New("taskpkg: calibration version conflict")
	// ErrCalibrationNotFound is returned when a withdrawal targets an unknown
	// calibration.
	ErrCalibrationNotFound = errors.New("taskpkg: calibration not found")
)

// SampleID identifies a raw sample by device instance and sequence number.
type SampleID struct {
	Device   string
	Sequence uint64
}

// Sample is the immutable raw sensor reading. Device and Sequence form its
// fixed identity; TakenAt is the sample time used to intersect calibration
// intervals.
type Sample struct {
	Device     string
	Sequence   uint64
	TakenAt    time.Time
	Raw        *big.Rat
	RolledBack bool
}

// ID returns the fixed identity of the sample.
func (s Sample) ID() SampleID {
	return SampleID{Device: s.Device, Sequence: s.Sequence}
}

// Rat builds a rational value, rejecting a zero denominator instead of
// panicking like big.NewRat.
func Rat(numerator, denominator int64) (*big.Rat, error) {
	if denominator == 0 {
		return nil, errors.New("taskpkg: rational with zero denominator")
	}
	return big.NewRat(numerator, denominator), nil
}

// RatInt builds a rational from a single integer.
func RatInt(value int64) *big.Rat {
	return big.NewRat(value, 1)
}

// Calibration is a rational affine transform active on a half-open interval.
// Version explicitly orders overlapping calibrations. The (Device, Version)
// pair is the calibration identity. A zero EffectiveUntil means the interval
// extends to the open end of time.
type Calibration struct {
	Device         string
	Version        uint64
	EffectiveFrom  time.Time
	EffectiveUntil time.Time
	Scale          *big.Rat
	Offset         *big.Rat
}

func (c Calibration) validate() error {
	if c.Device == "" {
		return fmt.Errorf("%w: empty device", ErrInvalidCalibration)
	}
	if c.Scale == nil || c.Offset == nil {
		return fmt.Errorf("%w: nil scale or offset", ErrInvalidCalibration)
	}
	if !c.EffectiveUntil.IsZero() && !c.EffectiveFrom.Before(c.EffectiveUntil) {
		return fmt.Errorf("%w: effective interval must be a non-empty half-open range", ErrInvalidCalibration)
	}
	return nil
}

// covers reports whether the calibration's half-open interval contains t.
func (c Calibration) covers(t time.Time) bool {
	if t.Before(c.EffectiveFrom) {
		return false
	}
	if c.EffectiveUntil.IsZero() {
		return true
	}
	return t.Before(c.EffectiveUntil)
}

// Apply evaluates value*scale + offset.
func (c Calibration) Apply(value *big.Rat) *big.Rat {
	result := new(big.Rat).Mul(value, c.Scale)
	return result.Add(result, c.Offset)
}

// sameParams reports whether two calibrations carry identical transform
// parameters and effective interval.
func (c Calibration) sameParams(other Calibration) bool {
	sameUntil := c.EffectiveUntil.Equal(other.EffectiveUntil) ||
		(c.EffectiveUntil.IsZero() && other.EffectiveUntil.IsZero())
	return c.Scale.Cmp(other.Scale) == 0 &&
		c.Offset.Cmp(other.Offset) == 0 &&
		c.EffectiveFrom.Equal(other.EffectiveFrom) &&
		sameUntil
}

// Evidence is the immutable provenance attached to every revision: a snapshot
// of the raw sample and of the calibration that produced the materialized
// value. HasCalibration reports whether a calibration (whose Version may
// legitimately be zero) produced the value; false means the identity mapping.
type Evidence struct {
	Sample         Sample
	HasCalibration bool
	CalVersion     uint64
	Calibration    *Calibration
}

// Revision is an immutable event describing one materialized value of a raw
// sample. The previous revision is superseded but never deleted.
type Revision struct {
	Seq        uint64
	Device     string
	Sequence   uint64
	Reason     Reason
	Value      *big.Rat
	RolledBack bool
	Evidence   Evidence
}

// ID returns the sample identity the revision belongs to.
func (r Revision) ID() SampleID {
	return SampleID{Device: r.Device, Sequence: r.Sequence}
}

// Materialized is the current projected value for one raw sample, together
// with the revision that established it and the live clock-rollback flag.
type Materialized struct {
	Device      string
	Sequence    uint64
	Value       *big.Rat
	RolledBack  bool
	RevisionSeq uint64
	Reason      Reason
}

// ID returns the sample identity of the materialized entry.
func (m Materialized) ID() SampleID {
	return SampleID{Device: m.Device, Sequence: m.Sequence}
}

type calState struct {
	cal       Calibration
	withdrawn bool
}

// Engine ingests raw samples and calibrations, maintains the materialized
// time series, and retains every revision with its original evidence. It is
// safe for concurrent use.
type Engine struct {
	mu sync.Mutex

	samples   map[SampleID]Sample
	byDevice  map[string][]SampleID
	cals      map[string]map[uint64]calState
	current   map[SampleID]Materialized
	revisions []Revision
	nextSeq   uint64
}

// NewEngine creates an empty calibration engine.
func NewEngine() *Engine {
	return &Engine{
		samples:  make(map[SampleID]Sample),
		byDevice: make(map[string][]SampleID),
		cals:     make(map[string]map[uint64]calState),
		current:  make(map[SampleID]Materialized),
		nextSeq:  1,
	}
}

func cloneRat(value *big.Rat) *big.Rat {
	if value == nil {
		return nil
	}
	return new(big.Rat).Set(value)
}

func ratEqual(a, b *big.Rat) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Cmp(b) == 0
}

func cloneSample(s Sample) Sample {
	s.Raw = cloneRat(s.Raw)
	return s
}

func cloneCalibration(c Calibration) Calibration {
	c.Scale = cloneRat(c.Scale)
	c.Offset = cloneRat(c.Offset)
	return c
}

// activeCalibration resolves the greatest-version non-withdrawn calibration
// whose half-open interval contains t. It returns nil when none applies, in
// which case the identity mapping is used.
func (e *Engine) activeCalibration(device string, t time.Time) (Calibration, bool) {
	versions := e.cals[device]
	if versions == nil {
		return Calibration{}, false
	}
	var chosen Calibration
	found := false
	for _, state := range versions {
		if state.withdrawn || !state.cal.covers(t) {
			continue
		}
		if !found || state.cal.Version > chosen.Version {
			chosen = state.cal
			found = true
		}
	}
	return chosen, found
}

func buildEvidence(sample Sample, cal *Calibration) Evidence {
	evidence := Evidence{Sample: cloneSample(sample)}
	if cal != nil {
		snapshot := cloneCalibration(*cal)
		evidence.Calibration = &snapshot
		evidence.CalVersion = cal.Version
		evidence.HasCalibration = true
	}
	return evidence
}

// deriveValue applies the given calibration to the raw reading, or returns the
// raw reading unchanged when no calibration applies.
func deriveValue(sample Sample, cal Calibration, found bool) *big.Rat {
	if !found {
		return cloneRat(sample.Raw)
	}
	return cal.Apply(sample.Raw)
}

func buildEvidenceFromValue(sample Sample, cal Calibration, found bool) Evidence {
	if !found {
		return buildEvidence(sample, nil)
	}
	return buildEvidence(sample, &cal)
}

// recomputeRollback recomputes clock-rollback flags for every sample of a
// device in sequence order. A sample is flagged when its sample time is
// strictly earlier than the previous sample's time.
func (e *Engine) recomputeRollback(device string) {
	ids := append([]SampleID(nil), e.byDevice[device]...)
	sort.Slice(ids, func(i, j int) bool { return ids[i].Sequence < ids[j].Sequence })

	var previous time.Time
	for index, id := range ids {
		sample := e.samples[id]
		rolledBack := index > 0 && sample.TakenAt.Before(previous)
		sample.RolledBack = rolledBack
		e.samples[id] = sample
		previous = sample.TakenAt
	}
}

func (e *Engine) appendRevision(sample Sample, cal Calibration, found bool, reason Reason) Revision {
	revision := Revision{
		Seq:        e.nextSeq,
		Device:     sample.Device,
		Sequence:   sample.Sequence,
		Reason:     reason,
		Value:      deriveValue(sample, cal, found),
		RolledBack: sample.RolledBack,
		Evidence:   buildEvidenceFromValue(sample, cal, found),
	}
	e.nextSeq++
	e.revisions = append(e.revisions, revision)

	e.current[sample.ID()] = Materialized{
		Device:      sample.Device,
		Sequence:    sample.Sequence,
		Value:       cloneRat(revision.Value),
		RolledBack:  sample.RolledBack,
		RevisionSeq: revision.Seq,
		Reason:      reason,
	}
	return revision
}

// PutSample ingests an immutable raw sample addressed by (Device, Sequence).
// Re-inserting the identical reading is a no-op; a different reading for the
// same fixed identity is rejected with ErrSampleConflict. A sample arriving
// after calibrations is mapped immediately and the affected rollback flags are
// recomputed in sequence order.
func (e *Engine) PutSample(sample Sample) (Revision, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if sample.Device == "" {
		return Revision{}, fmt.Errorf("%w: empty device", ErrInvalidCalibration)
	}
	if sample.Raw == nil {
		return Revision{}, fmt.Errorf("%w: nil raw reading", ErrInvalidCalibration)
	}

	id := sample.ID()
	if existing, ok := e.samples[id]; ok {
		if !existing.TakenAt.Equal(sample.TakenAt) || existing.Raw.Cmp(sample.Raw) != 0 {
			return Revision{}, fmt.Errorf("%w: device %q sequence %d", ErrSampleConflict, sample.Device, sample.Sequence)
		}
		last := e.lastRevisionLocked(id)
		if last == nil {
			return Revision{}, fmt.Errorf("%w: device %q sequence %d", ErrInvalidCalibration, sample.Device, sample.Sequence)
		}
		return *last, nil
	}

	stored := cloneSample(sample)
	stored.RolledBack = false
	e.samples[id] = stored
	e.byDevice[sample.Device] = append(e.byDevice[sample.Device], id)

	e.recomputeRollback(sample.Device)

	cal, found := e.activeCalibration(sample.Device, sample.TakenAt)
	return e.appendRevision(e.samples[id], cal, found, ReasonInitial), nil
}

func (e *Engine) lastRevisionLocked(id SampleID) *Revision {
	for index := len(e.revisions) - 1; index >= 0; index-- {
		if e.revisions[index].ID() == id {
			return &e.revisions[index]
		}
	}
	return nil
}

// recomputeIntersecting recomputes samples of a device after a calibration
// change. Only samples whose sample time intersects the given half-open
// interval are re-resolved; a revision is emitted only when the effective
// calibration version or the materialized value changes.
func (e *Engine) recomputeIntersecting(device string, from, until time.Time, reason Reason) {
	ids := append([]SampleID(nil), e.byDevice[device]...)
	sort.Slice(ids, func(i, j int) bool { return ids[i].Sequence < ids[j].Sequence })

	for _, id := range ids {
		sample := e.samples[id]
		if sample.TakenAt.Before(from) {
			continue
		}
		if !until.IsZero() && !sample.TakenAt.Before(until) {
			continue
		}

		cal, found := e.activeCalibration(device, sample.TakenAt)
		previousVersion := uint64(0)
		previousMapped := false
		if last := e.lastRevisionLocked(id); last != nil {
			previousMapped = last.Evidence.HasCalibration
			previousVersion = last.Evidence.CalVersion
		}

		newVersion := uint64(0)
		if found {
			newVersion = cal.Version
		}
		value := deriveValue(sample, cal, found)
		previous := e.current[id]
		if found == previousMapped && newVersion == previousVersion && ratEqual(value, previous.Value) {
			continue
		}
		e.appendRevision(sample, cal, found, reason)
	}
}

// PutCalibration registers or re-submits a calibration.
//
// An identical re-submission of an existing (Device, Version) is a no-op. The
// same version carrying different parameters (or re-using a withdrawn
// version) is rejected with ErrVersionConflict. New calibrations recompute
// only the intersecting historical samples; overlapping calibrations resolve
// to the greatest version.
func (e *Engine) PutCalibration(cal Calibration) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if err := cal.validate(); err != nil {
		return err
	}

	versions := e.cals[cal.Device]
	if versions == nil {
		versions = make(map[uint64]calState)
		e.cals[cal.Device] = versions
	}

	if state, ok := versions[cal.Version]; ok {
		if state.withdrawn {
			return fmt.Errorf("%w: device %q version %d is withdrawn", ErrVersionConflict, cal.Device, cal.Version)
		}
		if state.cal.sameParams(cal) {
			return nil
		}
		return fmt.Errorf("%w: device %q version %d already exists with different parameters", ErrVersionConflict, cal.Device, cal.Version)
	}

	versions[cal.Version] = calState{cal: cloneCalibration(cal)}
	e.recomputeIntersecting(cal.Device, cal.EffectiveFrom, cal.EffectiveUntil, ReasonCalibrationArrived)
	return nil
}

// WithdrawCalibration marks an existing calibration as no longer effective and
// recomputes the samples it used to cover, reverting them to the next active
// calibration or to the identity mapping. Withdrawing an unknown or already
// withdrawn calibration returns an error.
func (e *Engine) WithdrawCalibration(device string, version uint64) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	versions := e.cals[device]
	if versions == nil {
		return fmt.Errorf("%w: device %q version %d", ErrCalibrationNotFound, device, version)
	}
	state, ok := versions[version]
	if !ok {
		return fmt.Errorf("%w: device %q version %d", ErrCalibrationNotFound, device, version)
	}
	if state.withdrawn {
		return fmt.Errorf("%w: device %q version %d already withdrawn", ErrVersionConflict, device, version)
	}

	from := state.cal.EffectiveFrom
	until := state.cal.EffectiveUntil
	state.withdrawn = true
	versions[version] = state

	e.recomputeIntersecting(device, from, until, ReasonCalibrationWithdrawn)
	return nil
}

// RecomputeResult is the independently rebuilt projection of the whole
// engine, produced purely from raw samples and active calibrations.
type RecomputeResult struct {
	Materialized []Materialized
	RolledBack   map[SampleID]bool
	RevisionHits map[SampleID]int
}

// Recompute rebuilds the complete materialized series from the immutable raw
// samples and the currently active calibrations without consulting the live
// projection or revision log. It is the authoritative cross-check used by
// Verify and covers clock-rollback marking, half-open boundary equality and
// withdrawn calibrations.
func (e *Engine) Recompute() RecomputeResult {
	e.mu.Lock()
	defer e.mu.Unlock()

	devices := make([]string, 0, len(e.byDevice))
	for device := range e.byDevice {
		devices = append(devices, device)
	}
	sort.Strings(devices)

	result := RecomputeResult{
		RolledBack:   make(map[SampleID]bool),
		RevisionHits: make(map[SampleID]int),
	}

	for _, device := range devices {
		ids := append([]SampleID(nil), e.byDevice[device]...)
		sort.Slice(ids, func(i, j int) bool { return ids[i].Sequence < ids[j].Sequence })

		var previous time.Time
		for index, id := range ids {
			sample := e.samples[id]
			rolledBack := index > 0 && sample.TakenAt.Before(previous)
			previous = sample.TakenAt
			result.RolledBack[id] = rolledBack

			cal, found := e.activeCalibration(device, sample.TakenAt)
			result.Materialized = append(result.Materialized, Materialized{
				Device:     sample.Device,
				Sequence:   sample.Sequence,
				Value:      deriveValue(sample, cal, found),
				RolledBack: rolledBack,
			})
		}
	}

	for _, revision := range e.revisions {
		result.RevisionHits[revision.ID()]++
	}
	return result
}

// Verify rebuilds the series with Recompute and cross-checks it against the
// live projection and the full revision/evidence lineage. It returns an
// error describing the first inconsistency found, or nil when the engine is
// internally consistent.
func (e *Engine) Verify() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	rebuilt := e.recomputeLocked()

	ids := make([]SampleID, 0, len(e.samples))
	for id := range e.samples {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if ids[i].Device != ids[j].Device {
			return ids[i].Device < ids[j].Device
		}
		return ids[i].Sequence < ids[j].Sequence
	})

	if len(rebuilt) != len(ids) {
		return fmt.Errorf("taskpkg: recompute produced %d entries, engine has %d", len(rebuilt), len(ids))
	}
	for index, id := range ids {
		want := rebuilt[index]
		if want.ID() != id {
			return fmt.Errorf("taskpkg: recompute ordering mismatch at %d", index)
		}
		got, ok := e.current[id]
		if !ok {
			return fmt.Errorf("taskpkg: missing materialized value for %q/%d", id.Device, id.Sequence)
		}
		if !ratEqual(want.Value, got.Value) {
			return fmt.Errorf("taskpkg: value mismatch for %q/%d: recompute %v vs live %v",
				id.Device, id.Sequence, want.Value.RatString(), got.Value.RatString())
		}
		if want.RolledBack != got.RolledBack {
			return fmt.Errorf("taskpkg: rollback flag mismatch for %q/%d", id.Device, id.Sequence)
		}
		if got.RevisionSeq == 0 {
			return fmt.Errorf("taskpkg: materialized value for %q/%d has no establishing revision", id.Device, id.Sequence)
		}
	}

	lastBySample := make(map[SampleID]Revision)
	seenSeq := make(map[uint64]bool)
	var expectedSeq uint64 = 1
	for _, revision := range e.revisions {
		if revision.Seq != expectedSeq {
			return fmt.Errorf("taskpkg: revision sequence gap: want %d got %d", expectedSeq, revision.Seq)
		}
		expectedSeq++
		if seenSeq[revision.Seq] {
			return fmt.Errorf("taskpkg: duplicate revision sequence %d", revision.Seq)
		}
		seenSeq[revision.Seq] = true

		sample, ok := e.samples[revision.ID()]
		if !ok {
			return fmt.Errorf("taskpkg: revision %d references unknown sample", revision.Seq)
		}
		if !ratEqual(revision.Evidence.Sample.Raw, sample.Raw) ||
			revision.Evidence.Sample.TakenAt != sample.TakenAt ||
			revision.Evidence.Sample.Sequence != sample.Sequence ||
			revision.Evidence.Sample.Device != sample.Device {
			return fmt.Errorf("taskpkg: revision %d evidence does not match the immutable raw sample", revision.Seq)
		}

		// Each revision must be reproducible from its own frozen evidence,
		// independent of calibrations that were withdrawn or superseded later.
		if revision.Evidence.HasCalibration {
			snapshot := revision.Evidence.Calibration
			if snapshot == nil || snapshot.Version != revision.Evidence.CalVersion {
				return fmt.Errorf("taskpkg: revision %d has calibration version without matching snapshot", revision.Seq)
			}
			if !ratEqual(revision.Value, snapshot.Apply(sample.Raw)) {
				return fmt.Errorf("taskpkg: revision %d value does not follow its evidence snapshot", revision.Seq)
			}
		} else if revision.Evidence.Calibration != nil || revision.Evidence.CalVersion != 0 {
			return fmt.Errorf("taskpkg: revision %d carries a snapshot without a calibration version", revision.Seq)
		} else if !ratEqual(revision.Value, sample.Raw) {
			return fmt.Errorf("taskpkg: revision %d identity value differs from raw reading", revision.Seq)
		}
		lastBySample[revision.ID()] = revision
	}

	if len(lastBySample) != len(ids) {
		return fmt.Errorf("taskpkg: %d samples but only %d have revisions", len(ids), len(lastBySample))
	}
	for _, id := range ids {
		last := lastBySample[id]
		current := e.current[id]
		if last.Seq != current.RevisionSeq || !ratEqual(last.Value, current.Value) {
			return fmt.Errorf("taskpkg: live projection for %q/%d is not the latest revision", id.Device, id.Sequence)
		}
		if last.RolledBack != current.RolledBack {
			return fmt.Errorf("taskpkg: rollback flag on latest revision for %q/%d is stale", id.Device, id.Sequence)
		}
		// The latest revision must reflect the calibration active right now.
		active, found := e.activeCalibration(id.Device, e.samples[id].TakenAt)
		activeMapped := found
		activeVersion := uint64(0)
		if found {
			activeVersion = active.Version
		}
		if last.Evidence.HasCalibration != activeMapped || last.Evidence.CalVersion != activeVersion {
			return fmt.Errorf("taskpkg: latest revision for %q/%d does not reference the currently active calibration", id.Device, id.Sequence)
		}
	}
	return nil
}

// recomputeLocked rebuilds the ordered materialized projection assuming the
// caller holds the lock.
func (e *Engine) recomputeLocked() []Materialized {
	devices := make([]string, 0, len(e.byDevice))
	for device := range e.byDevice {
		devices = append(devices, device)
	}
	sort.Strings(devices)

	var materialized []Materialized
	for _, device := range devices {
		ids := append([]SampleID(nil), e.byDevice[device]...)
		sort.Slice(ids, func(i, j int) bool { return ids[i].Sequence < ids[j].Sequence })

		var previous time.Time
		for index, id := range ids {
			sample := e.samples[id]
			rolledBack := index > 0 && sample.TakenAt.Before(previous)
			previous = sample.TakenAt

			cal, found := e.activeCalibration(device, sample.TakenAt)
			materialized = append(materialized, Materialized{
				Device:     sample.Device,
				Sequence:   sample.Sequence,
				Value:      deriveValue(sample, cal, found),
				RolledBack: rolledBack,
			})
		}
	}
	return materialized
}

// RawSample returns a defensive copy of the immutable raw sample. The second
// return value reports whether it exists.
func (e *Engine) RawSample(id SampleID) (Sample, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	sample, ok := e.samples[id]
	if !ok {
		return Sample{}, false
	}
	return cloneSample(sample), true
}

// MaterializedValue returns the current materialized value for a sample.
func (e *Engine) MaterializedValue(id SampleID) (Materialized, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	materialized, ok := e.current[id]
	if !ok {
		return Materialized{}, false
	}
	materialized.Value = cloneRat(materialized.Value)
	return materialized, true
}

// Revisions returns the full ordered revision history. Each revision and its
// evidence are copied so callers cannot mutate engine state.
func (e *Engine) Revisions() []Revision {
	e.mu.Lock()
	defer e.mu.Unlock()

	out := make([]Revision, len(e.revisions))
	for index, revision := range e.revisions {
		copied := revision
		copied.Value = cloneRat(revision.Value)
		copied.Evidence.Sample = cloneSample(revision.Evidence.Sample)
		if revision.Evidence.Calibration != nil {
			snapshot := cloneCalibration(*revision.Evidence.Calibration)
			copied.Evidence.Calibration = &snapshot
		}
		out[index] = copied
	}
	return out
}

// RevisionsFor returns the ordered revisions of one sample.
func (e *Engine) RevisionsFor(id SampleID) []Revision {
	e.mu.Lock()
	defer e.mu.Unlock()

	var out []Revision
	for _, revision := range e.revisions {
		if revision.ID() != id {
			continue
		}
		copied := revision
		copied.Value = cloneRat(revision.Value)
		copied.Evidence.Sample = cloneSample(revision.Evidence.Sample)
		if revision.Evidence.Calibration != nil {
			snapshot := cloneCalibration(*revision.Evidence.Calibration)
			copied.Evidence.Calibration = &snapshot
		}
		out = append(out, copied)
	}
	return out
}
