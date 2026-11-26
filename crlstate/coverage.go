package crlstate

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

type timeInterval struct {
	lo time.Time
	hi time.Time
}

type genKey struct {
	epoch uint64
	root  RootID
}

type channelKey struct {
	partitionID string
	lo          string
	hi          string
	reasons     ReasonMask
}

type channelGroupKey struct {
	gen genKey
	ch  channelKey
}

type run struct {
	manifests []*Manifest
	start     time.Time
	end       time.Time
}

func (r *run) covers(t time.Time) bool {
	return !t.Before(r.start) && t.Before(r.end)
}

type channel struct {
	key     channelKey
	gen     genKey
	lo      Serial
	hi      Serial
	reasons ReasonMask
	runs    []*run
}

func (c *channel) coversAt(t time.Time) bool {
	for _, r := range c.runs {
		if r.covers(t) {
			return true
		}
	}
	return false
}

func (c *channel) spanKey() string {
	return strings.Join([]string{
		c.key.partitionID,
		c.lo.String(),
		c.hi.String(),
		fmt.Sprintf("%#x", uint64(c.reasons)),
	}, "|")
}

type coverageSeg struct {
	gen    genKey
	lo     time.Time
	hi     time.Time
	layout string
}

type evaluation struct {
	issuer     IssuerID
	serial     Serial
	start      time.Time
	end        time.Time
	required   ReasonMask
	all        []*Manifest
	relevant   []*Manifest
	migrations []*Migration

	channels  map[genKey][]*channel
	authority map[uint64][]timeInterval
	epochs    []uint64
	usable    map[*Manifest]bool
}

func (a *Aggregator) snapshot(issuer IssuerID) ([]*Manifest, []*Migration, *ForkEvidence) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	ms := append([]*Manifest(nil), a.manifests[issuer]...)
	migs := append([]*Migration(nil), a.migrations[issuer]...)
	var ev *ForkEvidence
	if f, ok := a.locked[issuer]; ok {
		c := cloneFork(*f)
		ev = &c
	}
	return ms, migs, ev
}

func newEvaluation(issuer IssuerID, serial Serial, start, end time.Time, required ReasonMask,
	all []*Manifest, migrations []*Migration) *evaluation {
	e := &evaluation{
		issuer:     issuer,
		serial:     serial,
		start:      start,
		end:        end,
		required:   required,
		all:        all,
		migrations: migrations,
		channels:   make(map[genKey][]*channel),
		authority:  make(map[uint64][]timeInterval),
		usable:     make(map[*Manifest]bool),
	}
	for _, m := range all {
		if serialInManifest(serial, m) && m.Reasons.Intersect(required) != 0 &&
			m.ValidFrom.Before(end) {
			// Include already-expired manifests that started before the
			// lifetime: a higher epoch still supersedes lower epochs after
			// its own window ends. Dropping it here would resurrect stale
			// lower-epoch coverage.
			e.relevant = append(e.relevant, m)
		}
	}
	e.buildChannels()
	e.buildUsableSet()
	e.buildAuthority()
	return e
}

func serialInManifest(s Serial, m *Manifest) bool {
	return s.Cmp(m.SerialLo) >= 0 && s.Cmp(m.SerialHi) < 0
}

func (e *evaluation) buildChannels() {
	groups := make(map[channelGroupKey][]*Manifest)
	for _, m := range e.relevant {
		k := channelGroupKey{
			gen: genKey{epoch: m.Epoch, root: m.RootID},
			ch: channelKey{
				partitionID: m.PartitionID,
				lo:          m.SerialLo.String(),
				hi:          m.SerialHi.String(),
				reasons:     m.Reasons,
			},
		}
		groups[k] = append(groups[k], m)
	}
	for k, ms := range groups {
		ch := &channel{
			key:     k.ch,
			gen:     k.gen,
			lo:      ms[0].SerialLo,
			hi:      ms[0].SerialHi,
			reasons: ms[0].Reasons,
			runs:    buildRuns(ms),
		}
		e.channels[k.gen] = append(e.channels[k.gen], ch)
	}
	for gen := range e.channels {
		sort.Slice(e.channels[gen], func(i, j int) bool {
			return e.channels[gen][i].spanKey() < e.channels[gen][j].spanKey()
		})
	}
}

// buildUsableSet marks manifests that participate in a complete contiguous
// full-base/delta run. A wrong-baseline, post-gap or branching delta is not
// usable state and therefore cannot release a certificateHold.
func (e *evaluation) buildUsableSet() {
	groups := make(map[channelGroupKey][]*Manifest)
	for _, m := range e.all {
		k := channelGroupKey{
			gen: genKey{epoch: m.Epoch, root: m.RootID},
			ch: channelKey{
				partitionID: m.PartitionID,
				lo:          m.SerialLo.String(),
				hi:          m.SerialHi.String(),
				reasons:     m.Reasons,
			},
		}
		groups[k] = append(groups[k], m)
	}
	for _, ms := range groups {
		for _, r := range buildRuns(ms) {
			for _, m := range r.manifests {
				e.usable[m] = true
			}
		}
	}
}

func buildRuns(manifests []*Manifest) []*run {
	ms := append([]*Manifest(nil), manifests...)
	sort.Slice(ms, func(i, j int) bool {
		if ms[i].CRLNumber != ms[j].CRLNumber {
			return ms[i].CRLNumber < ms[j].CRLNumber
		}
		return ms[i].ValidFrom.Before(ms[j].ValidFrom)
	})
	var fulls []*Manifest
	for _, m := range ms {
		if !m.IsDelta {
			fulls = append(fulls, m)
		}
	}
	var runs []*run
	for _, base := range fulls {
		r := &run{manifests: []*Manifest{base}, start: base.ValidFrom, end: base.ValidUntil}
		current := base.CRLNumber
		used := make(map[uint64]bool)
		reachable := map[uint64]bool{base.CRLNumber: true}
		for {
			var candidate *Manifest
			ambiguous := false
			for _, d := range ms {
				if !d.IsDelta || d.CRLNumber <= current || used[d.CRLNumber] || !reachable[d.BaseDelta] {
					continue
				}
				if candidate == nil || d.CRLNumber < candidate.CRLNumber {
					candidate = d
					ambiguous = false
				} else if d.CRLNumber == candidate.CRLNumber {
					ambiguous = true
				}
			}
			if candidate == nil {
				break
			}
			if ambiguous {
				// A branching delta graph is ambiguous. Coverage stops at
				// the last unambiguous head.
				break
			}
			d := candidate
			if d.ValidFrom.Before(base.ValidFrom) {
				break
			}
			if prev := r.manifests[len(r.manifests)-1]; d.ValidFrom.Before(prev.ValidFrom) {
				// CRL generation order and publication order must agree.
				break
			}
			if d.ValidFrom.After(r.end) || !d.ValidUntil.After(r.start) {
				// A hole in the base/delta sequence permanently ends usable
				// coverage for this run.
				break
			}
			r.manifests = append(r.manifests, d)
			r.end = d.ValidUntil
			used[d.CRLNumber] = true
			reachable[d.CRLNumber] = true
			current = d.CRLNumber
		}
		runs = append(runs, r)
	}
	return runs
}

func (e *evaluation) buildAuthority() {
	if len(e.relevant) == 0 {
		return
	}
	var events []time.Time
	events = append(events, e.start, e.end)
	for _, m := range e.relevant {
		lo := m.ValidFrom
		hi := m.ValidUntil
		if lo.Before(e.start) {
			lo = e.start
		}
		if hi.After(e.end) {
			hi = e.end
		}
		if lo.Before(hi) {
			events = append(events, lo, hi)
		}
	}
	events = uniqueTimes(events)
	for i := 0; i+1 < len(events); i++ {
		lo, hi := events[i], events[i+1]
		if !lo.Before(hi) {
			continue
		}
		started := false
		var maxEpoch uint64
		for _, m := range e.relevant {
			if !lo.Before(m.ValidFrom) {
				if !started || m.Epoch > maxEpoch {
					maxEpoch = m.Epoch
					started = true
				}
			}
		}
		if !started {
			continue
		}
		active := false
		for _, m := range e.relevant {
			if m.Epoch == maxEpoch && !lo.Before(m.ValidFrom) && lo.Before(m.ValidUntil) {
				active = true
				break
			}
		}
		if !active {
			// A started higher epoch that has expired supersedes lower
			// epochs; it must not resurrect them.
			continue
		}
		e.authority[maxEpoch] = appendInterval(e.authority[maxEpoch], timeInterval{lo: lo, hi: hi})
	}
	for epoch := range e.authority {
		e.epochs = append(e.epochs, epoch)
	}
	sort.Slice(e.epochs, func(i, j int) bool { return e.epochs[i] < e.epochs[j] })
}

func appendInterval(list []timeInterval, add timeInterval) []timeInterval {
	if !add.lo.Before(add.hi) {
		return list
	}
	if len(list) == 0 {
		return append(list, add)
	}
	last := &list[len(list)-1]
	if !add.lo.After(last.hi) {
		if add.hi.After(last.hi) {
			last.hi = add.hi
		}
		return list
	}
	return append(list, add)
}

func uniqueTimes(ts []time.Time) []time.Time {
	sort.Slice(ts, func(i, j int) bool { return ts[i].Before(ts[j]) })
	out := ts[:0]
	for _, t := range ts {
		if len(out) == 0 || !out[len(out)-1].Equal(t) {
			out = append(out, t)
		}
	}
	return out
}

func (e *evaluation) rootsFor(epoch uint64) []RootID {
	set := make(map[RootID]struct{})
	for gen := range e.channels {
		if gen.epoch == epoch {
			set[gen.root] = struct{}{}
		}
	}
	var roots []RootID
	for r := range set {
		roots = append(roots, r)
	}
	sort.Slice(roots, func(i, j int) bool { return roots[i] < roots[j] })
	return roots
}

// layoutAt returns the active partition layout for the target serial at t.
// Exactly one partition must own each required reason bit; overlapping
// ownership is ambiguous and therefore uncovered.
func (e *evaluation) layoutAt(gen genKey, t time.Time) (string, bool) {
	channels := e.channels[gen]
	var parts []string
	for _, reason := range e.required.Bits() {
		var owners []string
		for _, ch := range channels {
			if !ch.reasons.Has(reason) {
				continue
			}
			if ch.coversAt(t) {
				owners = append(owners, ch.spanKey())
			}
		}
		if len(owners) != 1 {
			return "", false
		}
		parts = append(parts, fmt.Sprintf("%d=%s", reason, owners[0]))
	}
	return strings.Join(parts, ";"), true
}

func (e *evaluation) coverageSegments() []coverageSeg {
	var out []coverageSeg
	for _, epoch := range e.epochs {
		auth := e.authority[epoch]
		for _, root := range e.rootsFor(epoch) {
			gen := genKey{epoch: epoch, root: root}
			for _, ai := range auth {
				var events []time.Time
				events = append(events, ai.lo, ai.hi)
				for _, ch := range e.channels[gen] {
					for _, r := range ch.runs {
						lo, hi := clipInterval(r.start, r.end, ai.lo, ai.hi)
						if lo.Before(hi) {
							events = append(events, lo, hi)
						}
					}
				}
				events = uniqueTimes(events)
				for i := 0; i+1 < len(events); i++ {
					lo, hi := events[i], events[i+1]
					if !lo.Before(hi) {
						continue
					}
					layout, ok := e.layoutAt(gen, lo)
					if !ok {
						continue
					}
					if n := len(out); n > 0 {
						prev := &out[n-1]
						if prev.gen == gen && prev.layout == layout && prev.hi.Equal(lo) {
							prev.hi = hi
							continue
						}
					}
					out = append(out, coverageSeg{gen: gen, lo: lo, hi: hi, layout: layout})
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].lo.Equal(out[j].lo) {
			return out[i].lo.Before(out[j].lo)
		}
		if !out[i].hi.Equal(out[j].hi) {
			return out[i].hi.Before(out[j].hi)
		}
		if out[i].gen != out[j].gen {
			return out[i].gen.epoch < out[j].gen.epoch ||
				(out[i].gen.epoch == out[j].gen.epoch && out[i].gen.root < out[j].gen.root)
		}
		return out[i].layout < out[j].layout
	})
	return out
}

func clipInterval(lo, hi, aLo, aHi time.Time) (time.Time, time.Time) {
	if lo.Before(aLo) {
		lo = aLo
	}
	if hi.After(aHi) {
		hi = aHi
	}
	return lo, hi
}

type pathResult struct {
	complete bool
	paths    int
	reach    time.Time
}

func (e *evaluation) coveragePath() pathResult {
	segments := e.coverageSegments()
	res := pathResult{reach: e.start}
	if len(segments) == 0 {
		return res
	}
	visited := make(map[string]bool)
	var dfs func(idx int, pos time.Time)
	dfs = func(idx int, pos time.Time) {
		if res.paths > 0 {
			return
		}
		seg := segments[idx]
		end := seg.hi
		if end.After(res.reach) {
			res.reach = end
		}
		if !end.Before(e.end) {
			res.complete = true
			res.paths++
			return
		}
		key := fmt.Sprintf("%d|%s|%s|%s|%s", seg.gen.epoch, seg.gen.root,
			pos.UTC().Format(time.RFC3339Nano), seg.hi.UTC().Format(time.RFC3339Nano), seg.layout)
		if visited[key] {
			return
		}
		visited[key] = true
		for j, next := range segments {
			if j == idx || !next.hi.After(end) {
				continue
			}
			if next.lo.After(end) || !end.Before(next.hi) {
				continue
			}
			if !e.transitionAllowed(seg, next, end) {
				continue
			}
			dfs(j, end)
			if res.paths > 0 {
				return
			}
		}
	}
	for i, seg := range segments {
		if seg.lo.After(e.start) || !e.start.Before(seg.hi) {
			continue
		}
		dfs(i, e.start)
		if res.paths > 0 {
			break
		}
	}
	return res
}

func (e *evaluation) transitionAllowed(cur, next coverageSeg, boundary time.Time) bool {
	if next.gen == cur.gen {
		if !next.lo.Equal(boundary) {
			return false
		}
		if cur.layout == next.layout {
			return true
		}
		// A layout change inside one generation is a split/merge and
		// therefore requires a migration.
		return e.hasMigration(cur.gen, next.gen, boundary)
	}
	if next.gen.epoch < cur.gen.epoch {
		return false
	}
	if next.gen.root == cur.gen.root && next.gen.epoch > cur.gen.epoch &&
		cur.layout == next.layout && next.lo.Equal(boundary) {
		// A normal CRL reissue with unchanged partition boundaries does not
		// need a split/merge declaration.
		return true
	}
	return e.hasMigration(cur.gen, next.gen, boundary)
}

func (e *evaluation) hasMigration(from, to genKey, boundary time.Time) bool {
	for _, mig := range e.migrations {
		if mig.IssuerID != e.issuer ||
			mig.FromEpoch != from.epoch || mig.ToEpoch != to.epoch ||
			mig.OldRoot != from.root || mig.NewRoot != to.root ||
			!mig.EffectiveAt.Equal(boundary) {
			continue
		}
		if spansCover(mig.Old, e.serial, e.required) &&
			spansCover(mig.New, e.serial, e.required) &&
			e.migrationBindsTarget(mig, from, to, boundary) {
			return true
		}
	}
	return false
}

// migrationBindsTarget rejects a declaration whose named partitions do not
// actually own the queried serial/reason region in the claimed generations.
func (e *evaluation) migrationBindsTarget(mig *Migration, from, to genKey, boundary time.Time) bool {
	oldAt := boundary.Add(-time.Nanosecond)
	if !e.sideBindsTarget(mig.Old, from, oldAt) {
		return false
	}
	return e.sideBindsTarget(mig.New, to, boundary)
}

func (e *evaluation) sideBindsTarget(spans []PartitionSpan, gen genKey, at time.Time) bool {
	for _, reason := range e.required.Bits() {
		var span *PartitionSpan
		for i := range spans {
			s := &spans[i]
			if s.Reasons.Has(reason) &&
				e.serial.Cmp(s.SerialLo) >= 0 && e.serial.Cmp(s.SerialHi) < 0 {
				span = s
				break
			}
		}
		if span == nil || span.PartitionID == "" {
			continue
		}
		bound := false
		for _, ch := range e.channels[gen] {
			if ch.key.partitionID != span.PartitionID || !ch.reasons.Has(reason) || !ch.coversAt(at) {
				continue
			}
			if e.serial.Cmp(ch.lo) >= 0 && e.serial.Cmp(ch.hi) < 0 &&
				span.SerialLo.Cmp(ch.lo) >= 0 && span.SerialHi.Cmp(ch.hi) <= 0 {
				bound = true
				break
			}
		}
		if !bound {
			return false
		}
	}
	return true
}

func spansCover(spans []PartitionSpan, serial Serial, reasons ReasonMask) bool {
	for _, reason := range reasons.Bits() {
		found := false
		for _, s := range spans {
			if s.Reasons.Has(reason) &&
				serial.Cmp(s.SerialLo) >= 0 && serial.Cmp(s.SerialHi) < 0 {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

type revocationProof struct {
	entry    Entry
	manifest *Manifest
}

func (e *evaluation) findRevocation(start, end time.Time) *revocationProof {
	var best *revocationProof
	for _, m := range e.all {
		for _, entry := range m.Entries {
			if entry.Serial.Cmp(e.serial) != 0 {
				continue
			}
			if entry.Reason == ReasonRemoveFromCRL {
				continue
			}
			if e.required&entry.Reason.Bit() == 0 {
				continue
			}
			if !entry.RevokedAt.Before(end) {
				continue
			}
			if entry.Reason == ReasonCertificateHold && e.holdReleased(entry) {
				continue
			}
			proof := &revocationProof{entry: entry, manifest: m}
			if best == nil || proof.entry.RevokedAt.Before(best.entry.RevokedAt) {
				best = proof
			}
		}
	}
	return best
}

func (e *evaluation) findRevocationAt(at time.Time) *revocationProof {
	if at.Before(e.start) || !at.Before(e.end) {
		return nil
	}
	var best *revocationProof
	for _, m := range e.all {
		for _, entry := range m.Entries {
			if entry.Serial.Cmp(e.serial) != 0 || entry.Reason == ReasonRemoveFromCRL {
				continue
			}
			if e.required&entry.Reason.Bit() == 0 {
				continue
			}
			if entry.RevokedAt.After(at) {
				continue
			}
			if entry.Reason == ReasonCertificateHold && e.holdReleased(entry) {
				continue
			}
			proof := &revocationProof{entry: entry, manifest: m}
			if best == nil || proof.entry.RevokedAt.Before(best.entry.RevokedAt) {
				best = proof
			}
		}
	}
	return best
}

func (e *evaluation) holdReleased(hold Entry) bool {
	for _, m := range e.all {
		if !e.usable[m] {
			continue
		}
		for _, release := range m.Entries {
			if release.Serial.Cmp(hold.Serial) != 0 || release.Reason != ReasonRemoveFromCRL {
				continue
			}
			if release.effectiveCredential() != hold.effectiveCredential() {
				continue
			}
			if !release.RevokedAt.Before(hold.RevokedAt) {
				return true
			}
		}
	}
	return false
}

func (e *evaluation) coversAt(at time.Time) (bool, uint64, RootID) {
	started := false
	var maxEpoch uint64
	for _, m := range e.relevant {
		if !at.Before(m.ValidFrom) {
			if !started || m.Epoch > maxEpoch {
				maxEpoch = m.Epoch
				started = true
			}
		}
	}
	if !started {
		return false, 0, ""
	}
	active := false
	for _, m := range e.relevant {
		if m.Epoch == maxEpoch && !at.Before(m.ValidFrom) && at.Before(m.ValidUntil) {
			active = true
			break
		}
	}
	if !active {
		return false, maxEpoch, ""
	}
	for _, root := range e.rootsFor(maxEpoch) {
		if _, ok := e.layoutAt(genKey{epoch: maxEpoch, root: root}, at); ok {
			return true, maxEpoch, root
		}
	}
	return false, maxEpoch, ""
}

// Check validates the full lifetime of c. A proven revocation on any valid
// manifest wins over coverage gaps. Otherwise, at least one root-consistent
// full base/delta partition path must cover every required reason bit for the
// entire lifetime; otherwise the result is Unknown.
func (a *Aggregator) Check(c Certificate) Result {
	required := c.Reasons
	if required == 0 {
		required = AllStatusReasons
	}
	if c.IssuerID == "" {
		return Result{Status: StatusUnknown, Err: fmt.Errorf("%w: empty issuer", ErrInvalidManifest)}
	}
	if !c.Start.Before(c.End) {
		return Result{Status: StatusUnknown, Err: fmt.Errorf("%w: certificate lifetime [%s,%s) is empty or inverted",
			ErrInvalidManifest, c.Start, c.End)}
	}
	if required&^AllStatusReasons != 0 {
		return Result{Status: StatusUnknown, Err: fmt.Errorf("%w: unsupported reason mask %s", ErrInvalidManifest, required)}
	}

	all, migrations, fork := a.snapshot(c.IssuerID)
	if fork != nil {
		return Result{Status: StatusLocked, Fork: fork}
	}
	e := newEvaluation(c.IssuerID, c.Serial, c.Start, c.End, required, all, migrations)
	if rev := e.findRevocation(c.Start, c.End); rev != nil {
		return Result{
			Status:         StatusRevoked,
			Reason:         rev.entry.Reason,
			RevokedAt:      rev.entry.RevokedAt,
			ManifestDigest: rev.manifest.SigningDigest(),
			PartitionID:    rev.manifest.PartitionID,
			Epoch:          rev.manifest.Epoch,
			Root:           rev.manifest.RootID,
		}
	}
	path := e.coveragePath()
	if path.complete {
		return Result{Status: StatusGood, Paths: path.paths}
	}
	reach := path.reach
	if reach.Before(c.Start) {
		reach = c.Start
	}
	return Result{
		Status: StatusUnknown,
		Paths:  path.paths,
		Missing: []Gap{{
			Start:    reach,
			End:      c.End,
			Reasons:  required,
			SerialLo: c.Serial,
			SerialHi: c.Serial.Add(1),
		}},
	}
}

// CheckAt evaluates an instantaneous status at at, which must lie inside the
// certificate lifetime. It is intended for timeline audits.
func (a *Aggregator) CheckAt(c Certificate, at time.Time) Result {
	required := c.Reasons
	if required == 0 {
		required = AllStatusReasons
	}
	if c.IssuerID == "" {
		return Result{Status: StatusUnknown, Err: fmt.Errorf("%w: empty issuer", ErrInvalidManifest)}
	}
	if !c.Start.Before(c.End) {
		return Result{Status: StatusUnknown, Err: fmt.Errorf("%w: invalid certificate lifetime", ErrInvalidManifest)}
	}
	if at.Before(c.Start) || !at.Before(c.End) {
		return Result{Status: StatusUnknown, Err: fmt.Errorf("crlstate: point %s outside lifetime [%s,%s)", at, c.Start, c.End)}
	}
	all, migrations, fork := a.snapshot(c.IssuerID)
	if fork != nil {
		return Result{Status: StatusLocked, Fork: fork}
	}
	e := newEvaluation(c.IssuerID, c.Serial, c.Start, c.End, required, all, migrations)
	if rev := e.findRevocationAt(at); rev != nil {
		return Result{
			Status:         StatusRevoked,
			Reason:         rev.entry.Reason,
			RevokedAt:      rev.entry.RevokedAt,
			ManifestDigest: rev.manifest.SigningDigest(),
			PartitionID:    rev.manifest.PartitionID,
			Epoch:          rev.manifest.Epoch,
			Root:           rev.manifest.RootID,
		}
	}
	ok, epoch, root := e.coversAt(at)
	if ok {
		return Result{Status: StatusGood, Epoch: epoch, Root: root, Paths: 1}
	}
	return Result{
		Status: StatusUnknown,
		Epoch:  epoch,
		Root:   root,
		Missing: []Gap{{
			Start:    at,
			End:      at.Add(time.Nanosecond),
			Reasons:  required,
			SerialLo: c.Serial,
			SerialHi: c.Serial.Add(1),
			Epoch:    epoch,
			Root:     root,
		}},
	}
}
