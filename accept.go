package proofaccept

import (
	"context"
	"errors"
	"fmt"
)

// Check verifies a measurement set using a background context.
func Check(in Input) Result {
	return CheckContext(context.Background(), in)
}

// CheckContext verifies whether all locally consistent measurements can be
// explained by one legal causal cut.  It does not enumerate topological sorts:
// it searches only the finite set of page-state intervals and represents the
// result as a partial-order frontier.
func CheckContext(ctx context.Context, in Input) Result {
	if err := ctx.Err(); err != nil {
		return canceledResult(err)
	}
	c, err := compile(in)
	if err != nil {
		return Result{Status: StatusInvalidInput, Reason: err.Error()}
	}
	return c.check(ctx)
}

func (c *compiled) check(ctx context.Context) Result {
	all := c.sampleIDs()
	analysis := c.analyze(ctx, all)
	if err := ctx.Err(); err != nil {
		return canceledResult(err)
	}
	if !analysis.compatible {
		conflict := c.minimalConflict(ctx, all)
		if err := ctx.Err(); err != nil {
			return canceledResult(err)
		}
		if conflict == nil {
			return Result{
				Status: StatusInvalidInput,
				Reason: "measurements are incompatible but no conflict certificate could be built",
			}
		}
		return Result{
			Status:   StatusIncompatible,
			Conflict: conflict,
			Reason:   "no single causal cut satisfies the conflicting samples",
		}
	}

	result := c.resultForAnalysis(analysis)
	missing := c.missingPages(all)
	if len(missing) > 0 {
		return Result{
			Status:       StatusEvidenceInsufficient,
			CommonPrefix: true,
			Safe:         false,
			Frontier:     result.Frontier,
			Incomplete:   missing,
			Reason:       "a complete change list is missing for at least one observed page",
		}
	}
	return result
}

// Accept independently validates an explicit frontier or conflict
// certificate.  A frontier certificate is accepted only when the supplied cut
// is compact, is a legal common prefix of every complete observation, and all
// executable pages at that cut are trusted.  A conflict certificate is
// accepted only when it is the canonical minimum-cardinality incompatible
// sample set.
func Accept(in Input, certificate Certificate) Result {
	return AcceptContext(context.Background(), in, certificate)
}

// AcceptContext is the context-aware form of Accept.
func AcceptContext(ctx context.Context, in Input, certificate Certificate) Result {
	if err := ctx.Err(); err != nil {
		return canceledResult(err)
	}
	c, err := compile(in)
	if err != nil {
		return Result{Status: StatusInvalidInput, Reason: err.Error()}
	}
	switch certificate.Kind {
	case FrontierCertificate:
		return c.acceptFrontier(ctx, certificate.Frontier)
	case ConflictCertificate:
		return c.acceptConflict(ctx, certificate.Samples)
	default:
		return Result{
			Status: StatusInvalidInput,
			Reason: fmt.Sprintf("unknown certificate kind %q", certificate.Kind),
		}
	}
}

// AcceptFrontier is a convenience wrapper for a frontier certificate.
func AcceptFrontier(in Input, frontier []EventID) Result {
	return Accept(in, Certificate{Kind: FrontierCertificate, Frontier: frontier})
}

// AcceptConflict is a convenience wrapper for a conflict certificate.
func AcceptConflict(in Input, samples []SampleID) Result {
	return Accept(in, Certificate{Kind: ConflictCertificate, Samples: samples})
}

func (c *compiled) acceptFrontier(ctx context.Context, given []EventID) Result {
	frontier, err := c.canonicalFrontier(given)
	if err != nil {
		return Result{
			Status: StatusInvalidInput,
			Reason: "invalid frontier certificate: " + err.Error(),
		}
	}
	required := make(map[EventID]struct{}, len(frontier))
	for _, id := range frontier {
		required[id] = struct{}{}
	}
	closure := c.closure(required)

	all := c.sampleIDs()
	ok, missing, err := c.cutSatisfies(ctx, all, closure, false)
	if err != nil {
		if ctx.Err() != nil {
			return canceledResult(err)
		}
		return Result{Status: StatusInvalidInput, Reason: err.Error()}
	}
	if !ok {
		// The supplied cut may satisfy complete evidence while a different
		// cut is needed for all samples, but a certificate must prove the
		// requested set, not a weaker one.
		return Result{
			Status:              StatusInvalidInput,
			CertificateAccepted: false,
			Frontier:            frontier,
			Reason:              "frontier is not a common prefix of every complete observation",
		}
	}
	if missing {
		return Result{
			Status:              StatusEvidenceInsufficient,
			CommonPrefix:        true,
			CertificateAccepted: false,
			Frontier:            frontier,
			Incomplete:          c.missingPages(all),
			Reason:              "frontier proves only the complete portion of the evidence",
		}
	}
	if !c.cutSafe(closure) {
		return Result{
			Status:              StatusUnsafe,
			CommonPrefix:        true,
			Safe:                false,
			CertificateAccepted: false,
			Frontier:            frontier,
			Reason:              "the supplied cut has an executable page from an untrusted object",
		}
	}
	return Result{
		Status:              StatusCompatible,
		CommonPrefix:        true,
		Safe:                true,
		CertificateAccepted: true,
		Frontier:            frontier,
		Reason:              "frontier witness accepted",
	}
}

func (c *compiled) acceptConflict(ctx context.Context, given []SampleID) Result {
	seen := make(map[SampleID]struct{}, len(given))
	for _, id := range given {
		if _, ok := c.samples[id]; !ok {
			return Result{
				Status: StatusInvalidInput,
				Reason: fmt.Sprintf("invalid conflict certificate: unknown sample %q", id),
			}
		}
		if _, ok := seen[id]; ok {
			return Result{
				Status: StatusInvalidInput,
				Reason: fmt.Sprintf("invalid conflict certificate: duplicate sample %q", id),
			}
		}
		seen[id] = struct{}{}
	}

	canonical := c.minimalConflict(ctx, c.sampleIDs())
	if err := ctx.Err(); err != nil {
		return canceledResult(err)
	}
	if canonical == nil {
		return Result{
			Status:              StatusCompatible,
			CommonPrefix:        true,
			CertificateAccepted: false,
			Reason:              "conflict certificate rejected: the measurements have a common prefix",
		}
	}
	if !sameSampleSet(canonical, given) {
		return Result{
			Status:              StatusIncompatible,
			CertificateAccepted: false,
			Conflict:            canonical,
			Reason:              "conflict certificate is not the canonical minimum incompatible set",
		}
	}
	return Result{
		Status:              StatusIncompatible,
		CertificateAccepted: true,
		Conflict:            canonical,
		Reason:              "conflict certificate accepted",
	}
}

func canceledResult(err error) Result {
	return Result{Status: StatusCanceled, Reason: err.Error()}
}

func sameSampleSet(a, b []SampleID) bool {
	if len(a) != len(b) {
		return false
	}
	as := append([]SampleID(nil), a...)
	bs := append([]SampleID(nil), b...)
	sortSampleIDs(as)
	sortSampleIDs(bs)
	for i := range as {
		if as[i] != bs[i] {
			return false
		}
	}
	return true
}

func (c *compiled) cutSatisfies(ctx context.Context, ids []SampleID, closure map[EventID]struct{}, includeIncomplete bool) (bool, bool, error) {
	missing := false
	for _, id := range ids {
		if err := c.checkContext(ctx); err != nil {
			return false, false, err
		}
		sample, ok := c.samples[id]
		if !ok {
			return false, false, errors.New("unknown sample " + string(id))
		}
		for _, event := range sample.After {
			if _, ok := closure[event]; !ok {
				return false, missing, nil
			}
		}
		for _, event := range sample.Before {
			if _, ok := closure[event]; ok {
				return false, missing, nil
			}
		}
		incomplete := c.incompleteFrames(sample)
		for _, state := range sample.States {
			frame, err := c.frameOf(state.Page, state.Frame)
			if err != nil {
				return false, missing, err
			}
			state.Frame = frame
			if _, unknown := incomplete[frame]; unknown && !includeIncomplete {
				missing = true
				continue
			}
			actual, mapped := c.stateAtFrame(frame, closure)
			if !mapped || !sameMapping(actual, state) {
				return false, missing, nil
			}
		}
	}
	return true, missing, nil
}

func (c *compiled) stateAtFrame(frame FrameID, closure map[EventID]struct{}) (PageState, bool) {
	line := c.lines[frame]
	if line == nil {
		state, ok := c.initial[frame]
		return state, ok
	}
	count := 0
	for _, id := range line.events {
		if _, ok := closure[id]; ok {
			count++
		}
	}
	if count == 0 {
		if line.initial == nil {
			return PageState{}, false
		}
		return *line.initial, true
	}
	state := line.after[count-1]
	if state == nil {
		return PageState{}, false
	}
	return *state, true
}
