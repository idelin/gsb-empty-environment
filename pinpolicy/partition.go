package pinpolicy

import "sort"

// partition builds the endpoint partition of the real timeline inside
// [horizonFrom, horizonUntil]. Every cert publication/revocation, policy
// publication and window edge contributes an endpoint. The result alternates
// singleton points with open intervals and preserves equal-point versus
// open-interior distinctions.
func partition(in Input) []Element {
	eventSet := map[Time]struct{}{
		in.HorizonFrom:  {},
		in.HorizonUntil: {},
	}
	add := func(t Time) {
		if t < in.HorizonFrom || t > in.HorizonUntil {
			return
		}
		eventSet[t] = struct{}{}
	}

	for _, cert := range in.Certs {
		add(cert.PublishAt)
		if cert.Revoked {
			add(cert.RevokedAt)
		}
	}
	for _, release := range in.Releases {
		add(release.PublishAt)
		add(release.Policy.ValidFrom)
		add(release.Policy.ValidUntil)
	}
	for _, handover := range in.Handovers {
		add(handover.PublishAt)
	}

	events := make([]Time, 0, len(eventSet))
	for event := range eventSet {
		events = append(events, event)
	}
	sort.Slice(events, func(i, j int) bool { return events[i] < events[j] })

	elements := make([]Element, 0, len(events)*2-1)
	for index, event := range events {
		elements = append(elements, Element{
			Kind:   KindPoint,
			At:     event,
			From:   event,
			Until:  event,
			Sample: event,
		})
		if index+1 >= len(events) {
			continue
		}
		next := events[index+1]
		open := Element{
			Kind:  KindOpen,
			From:  event,
			Until: next,
		}
		// The open interval (event, next). With integer time it is non-empty
		// only when next-event >= 2; sample the smallest interior integer so
		// choices stay reproducible and small.
		if next-event >= 2 {
			open.Sample = event + 1
		} else {
			open.Empty = true
		}
		elements = append(elements, open)
	}
	return elements
}

// skews enumerates the inclusive local-clock error interval.
func skews(in Input) []Time {
	if in.SkewMax < in.SkewMin {
		return nil
	}
	out := make([]Time, 0, in.SkewMax-in.SkewMin+1)
	for skew := in.SkewMin; skew <= in.SkewMax; skew++ {
		out = append(out, skew)
	}
	return out
}
