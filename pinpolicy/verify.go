package pinpolicy

import (
	"errors"
	"sort"
)

// certView is the indexed certificate schedule.
type certView struct {
	byKey map[KeyID]Cert
}

func indexCerts(in Input) certView {
	view := certView{byKey: make(map[KeyID]Cert, len(in.Certs))}
	for _, cert := range in.Certs {
		view.byKey[cert.Key] = cert
	}
	return view
}

// liveAt implements the open revocation edge: a key is live at real time t
// exactly when it is published and either never revoked or t < RevokedAt.
func (v certView) liveAt(key KeyID, t Time) (Cert, bool) {
	cert, ok := v.byKey[key]
	if !ok || t < cert.PublishAt {
		return Cert{}, false
	}
	if cert.Revoked && t >= cert.RevokedAt {
		return Cert{}, false
	}
	return cert, true
}

// Validate checks structural input requirements. Schedule-level failures
// (revocations, missing authorizations) are reported through Report instead.
func Validate(in Input) error {
	if in.OldRoot == "" || in.NewRoot == "" {
		return errors.New("pinpolicy: OldRoot and NewRoot must both be set")
	}
	if in.OldRoot == in.NewRoot {
		return errors.New("pinpolicy: OldRoot and NewRoot must differ")
	}
	if in.HorizonUntil < in.HorizonFrom {
		return errors.New("pinpolicy: horizon is inverted")
	}
	if in.OfflineMax < 0 {
		return errors.New("pinpolicy: OfflineMax must be non-negative")
	}
	if in.SkewMax < in.SkewMin {
		return errors.New("pinpolicy: skew interval is inverted")
	}
	if len(in.Releases) == 0 {
		return errors.New("pinpolicy: at least one release is required")
	}

	seen := map[HostID]map[Seq]bool{}
	for _, release := range in.Releases {
		body := release.Policy
		if body.ValidUntil < body.ValidFrom {
			return errors.New("pinpolicy: policy validity window is inverted")
		}
		if seen[body.Host] == nil {
			seen[body.Host] = map[Seq]bool{}
		}
		if seen[body.Host][body.Sequence] {
			return errors.New("pinpolicy: duplicate sequence in release schedule")
		}
		seen[body.Host][body.Sequence] = true
	}

	for _, handover := range in.Handovers {
		if handover.OldRoot != in.OldRoot || handover.NewRoot != in.NewRoot {
			return errors.New("pinpolicy: handover roots do not match configured migration")
		}
	}
	return nil
}

type engine struct {
	in   Input
	cert certView
}

// Verify exhaustively evaluates the bounded schedule and returns an
// item-by-item report. Structural input problems return an error; schedule
// rejections are reported inside the Report with SafetyPass/LivenessPass set
// accordingly.
func Verify(in Input) (*Report, error) {
	if err := Validate(in); err != nil {
		return nil, err
	}
	eng := engine{in: in, cert: indexCerts(in)}

	elements := partition(in)
	allSkews := skews(in)
	report := &Report{
		Elements: elements,
		Windows:  make([]OnlineCase, 0, len(elements)),
	}
	report.Stats.ElementCount = len(elements)
	report.Stats.SkewCount = len(allSkews)
	report.Stats.CheckpointCount = len(in.Checkpoints)
	report.Stats.ReleaseCount = len(in.Releases)
	report.Stats.HandoverCount = len(in.Handovers)

	for _, element := range elements {
		if element.Kind == KindOpen && element.Empty {
			report.Stats.EmptyIntervalCount++
			report.Windows = append(report.Windows, OnlineCase{Element: element})
			continue
		}
		report.Windows = append(report.Windows, eng.evaluateElement(element, allSkews))
	}

	eng.finalize(report)
	return report, nil
}

func (e engine) evaluateElement(element Element, allSkews []Time) OnlineCase {
	t := element.Sample
	window := OnlineCase{
		Element:      element,
		RealTime:     t,
		ChainFailure: map[CaseKey]Reason{},
	}

	viable := viableCheckpoints(e.in, t)
	window.Reachable = len(viable) > 0

	// Release-level verdicts are real-time facts (publication, revocation,
	// thresholds, handover, rollback) combined with the closed local-time
	// window. Evaluate every release once per element over all skews and keep
	// the union of reasons; per-skew chain eligibility is derived afterwards.
	baseVerdict := map[Seq]*ReleaseVerdict{}
	for _, skew := range allSkews {
		localTime := t + skew
		for index := range e.in.Releases {
			release := e.in.Releases[index]
			verdict := e.evaluateRelease(release, t, localTime)
			if existing, ok := baseVerdict[release.Policy.Sequence]; ok {
				mergeVerdict(existing, verdict)
			} else {
				baseVerdict[release.Policy.Sequence] = verdict
			}
		}
	}
	for _, release := range e.in.Releases {
		window.ReleaseVerdicts = append(window.ReleaseVerdicts,
			*baseVerdict[release.Policy.Sequence])
	}

	window.MigrationFetchable = e.migrationPublished(t)

	for _, checkpoint := range viable {
		for _, skew := range allSkews {
			key := CaseKey{Checkpoint: checkpoint, Skew: skew}
			window.Cases = append(window.Cases, key)
			eligible := map[Seq]*ReleaseVerdict{}
			for seq, verdict := range baseVerdict {
				if e.eligibleAtSkew(*verdict, t, skew) {
					eligible[seq] = verdict
				}
			}
			if reason := e.chainReason(eligible, t); reason != "" {
				window.ChainFailure[key] = reason
			}
		}
	}

	window.ChainOK = window.Reachable && window.MigrationFetchable &&
		len(window.Cases) > 0 && len(window.ChainFailure) == 0

	return window
}

func mergeVerdict(dst, src *ReleaseVerdict) {
	for _, reason := range src.Reasons {
		found := false
		for _, existing := range dst.Reasons {
			if existing == reason {
				found = true
				break
			}
		}
		if !found {
			dst.Reasons = append(dst.Reasons, reason)
		}
	}
	for _, missing := range src.Missing {
		found := false
		for _, existing := range dst.Missing {
			if existing == missing {
				found = true
				break
			}
		}
		if !found {
			dst.Missing = append(dst.Missing, missing)
		}
	}
	sortReasons(dst.Reasons)
}

// eligibleAtSkew reports whether the release behind a verdict is usable at a
// particular skew: all skew-independent predicates must pass, and the closed
// policy window must contain the skewed local time.
func (e engine) eligibleAtSkew(verdict ReleaseVerdict, t, skew Time) bool {
	for _, reason := range verdict.Reasons {
		if reason != ReasonWindowClosed {
			return false
		}
	}
	for _, release := range e.in.Releases {
		body := release.Policy
		if body.Host != verdict.Host || body.Sequence != verdict.Sequence {
			continue
		}
		localTime := t + skew
		return localTime >= body.ValidFrom && localTime <= body.ValidUntil
	}
	return false
}

func viableCheckpoints(in Input, t Time) []Time {
	out := make([]Time, 0, len(in.Checkpoints))
	for _, checkpoint := range in.Checkpoints {
		if t >= checkpoint && t-checkpoint <= in.OfflineMax {
			out = append(out, checkpoint)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func (e engine) migrationPublished(t Time) bool {
	for _, release := range e.in.Releases {
		body := release.Policy
		if body.RootKey == e.in.NewRoot && release.PublishAt <= t {
			return true
		}
	}
	return false
}
