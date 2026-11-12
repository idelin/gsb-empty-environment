package taskpkg

import (
	"fmt"
	"sort"
)

type coverage struct {
	resp   *Response
	header bool
	ok     bool
}

func coverageAt(responses []Response, off int) coverage {
	var best *Response
	for i := range responses {
		r := &responses[i]
		if r.Start <= off && off < r.End {
			if best == nil || r.Start > best.Start {
				best = r
			}
		}
	}
	if best == nil {
		return coverage{}
	}
	return coverage{resp: best, header: off < best.HeaderEnd, ok: true}
}

func nearestResponse(responses []Response, off int) *Response {
	var best *Response
	for i := range responses {
		r := &responses[i]
		if r.Start <= off && (best == nil || r.Start >= best.Start) {
			best = r
		}
	}
	if best == nil && len(responses) > 0 {
		best = &responses[0]
	}
	return best
}

func addOffset(set map[int]struct{}, off, rawLen int) {
	if off >= 0 && off <= rawLen {
		set[off] = struct{}{}
	}
}

func collectOffsets(a, b ParseResult, rawLen int) []int {
	set := make(map[int]struct{})
	addOffset(set, 0, rawLen)
	addOffset(set, rawLen, rawLen)
	for _, r := range a.Responses {
		addOffset(set, r.Start, rawLen)
		addOffset(set, r.HeaderEnd, rawLen)
		addOffset(set, r.BodyStart, rawLen)
		addOffset(set, r.BodyEnd, rawLen)
		addOffset(set, r.End, rawLen)
	}
	for _, r := range b.Responses {
		addOffset(set, r.Start, rawLen)
		addOffset(set, r.HeaderEnd, rawLen)
		addOffset(set, r.BodyStart, rawLen)
		addOffset(set, r.BodyEnd, rawLen)
		addOffset(set, r.End, rawLen)
	}
	if a.Err != nil {
		addOffset(set, a.Err.Offset, rawLen)
		addOffset(set, a.Err.DetailOffset, rawLen)
	}
	if b.Err != nil {
		addOffset(set, b.Err.Offset, rawLen)
		addOffset(set, b.Err.DetailOffset, rawLen)
	}
	offsets := make([]int, 0, len(set))
	for off := range set {
		offsets = append(offsets, off)
	}
	sort.Ints(offsets)
	return offsets
}

func rangeOf(resp *Response) Interval {
	if resp == nil {
		return Interval{Start: -1, End: -1}
	}
	return resp.Interval()
}

func makeDifference(off int, kind DifferenceKind, message string, cross bool, a, b ParseResult, ar, br *Response) Difference {
	d := Difference{
		Offset:                 off,
		Kind:                   kind,
		Message:                message,
		CrossRequest:           cross,
		StrictRequestIndex:     -1,
		AlternateRequestIndex:  -1,
		StrictResponseIndex:    -1,
		AlternateResponseIndex: -1,
		StrictRange:            rangeOf(ar),
		AlternateRange:         rangeOf(br),
		StrictResponse:         ar,
		AlternateResponse:      br,
	}
	if ar != nil {
		d.StrictRequestIndex = ar.RequestIndex
		d.StrictResponseIndex = ar.Index
		d.StrictBytes = ar.Raw
	}
	if br != nil {
		d.AlternateRequestIndex = br.RequestIndex
		d.AlternateResponseIndex = br.Index
		d.AlternateBytes = br.Raw
	}
	d.StrictError = a.Err
	d.AlternateError = b.Err
	return d
}

func compareResults(a, b ParseResult, rawLen int) []Difference {
	var diffs []Difference

	offsets := collectOffsets(a, b, rawLen)
	for _, off := range offsets {
		ca := coverageAt(a.Responses, off)
		cb := coverageAt(b.Responses, off)

		if ca.ok && cb.ok {
			switch {
			case ca.resp.RequestIndex != cb.resp.RequestIndex:
				diffs = append(diffs, makeDifference(
					off,
					DiffOwnership,
					fmt.Sprintf("byte %d belongs to request %d on strict side and request %d on alternate side", off, ca.resp.RequestIndex, cb.resp.RequestIndex),
					true, a, b, ca.resp, cb.resp,
				))
			case ca.resp.Index != cb.resp.Index:
				diffs = append(diffs, makeDifference(
					off,
					DiffResponseIndex,
					fmt.Sprintf("byte %d is response %d on strict side and response %d on alternate side", off, ca.resp.Index, cb.resp.Index),
					false, a, b, ca.resp, cb.resp,
				))
			case ca.header != cb.header && !a.Minimal && !b.Minimal:
				diffs = append(diffs, makeDifference(
					off,
					DiffHeaderBoundary,
					fmt.Sprintf("strict side calls byte %d %s; alternate side calls it %s", off, regionName(ca.header), regionName(cb.header)),
					false, a, b, ca.resp, cb.resp,
				))
			}
			continue
		}

		if !ca.ok && !cb.ok {
			continue
		}

		var kind DifferenceKind
		var message string
		var ar, br *Response
		var cross bool
		if ca.ok {
			kind = DiffMissingOnAlternate
			message = fmt.Sprintf("strict strategy assigns byte %d to response %d; alternate strategy has no response there", off, ca.resp.Index)
			ar = ca.resp
			br = nearestResponse(b.Responses, off)
			if b.Err != nil && b.Err.Offset == off {
				kind = DiffParseError
				message = fmt.Sprintf("alternate strategy stops at byte %d inside strict response %d", off, ca.resp.Index)
			}
			if br != nil && ar.RequestIndex >= 0 && br.RequestIndex >= 0 && ar.RequestIndex != br.RequestIndex {
				cross = true
			}
		} else {
			kind = DiffMissingOnStrict
			message = fmt.Sprintf("alternate strategy assigns byte %d to response %d; strict strategy has no response there", off, cb.resp.Index)
			ar = nearestResponse(a.Responses, off)
			br = cb.resp
			if a.Err != nil && a.Err.Offset == off {
				kind = DiffParseError
				message = fmt.Sprintf("strict strategy stops at byte %d inside alternate response %d", off, cb.resp.Index)
			}
			if ar != nil && ar.RequestIndex >= 0 && br.RequestIndex >= 0 && ar.RequestIndex != br.RequestIndex {
				cross = true
			}
		}
		diffs = append(diffs, makeDifference(off, kind, message, cross, a, b, ar, br))
	}

	diffs = append(diffs, compareMetadata(a, b)...)
	diffs = append(diffs, compareErrors(a, b)...)
	return sortDifferences(diffs)
}

func regionName(header bool) string {
	if header {
		return "header bytes"
	}
	return "body bytes"
}

func compareMetadata(a, b ParseResult) []Difference {
	if len(a.Responses) == 0 || len(b.Responses) == 0 {
		return nil
	}
	byStart := make(map[int]int, len(b.Responses))
	for i := range b.Responses {
		if _, exists := byStart[b.Responses[i].Start]; !exists {
			byStart[b.Responses[i].Start] = i
		}
	}
	var diffs []Difference
	for i := range a.Responses {
		ar := &a.Responses[i]
		j, found := byStart[ar.Start]
		if !found {
			continue
		}
		br := &b.Responses[j]
		cross := ar.RequestIndex != br.RequestIndex

		if a.Minimal || b.Minimal {
			if ar.Truncated != br.Truncated {
				diffs = append(diffs, makeDifference(
					minInt(ar.End, br.End), DiffTruncation,
					fmt.Sprintf("truncation is %v on strict side and %v on alternate side", ar.Truncated, br.Truncated),
					cross, a, b, ar, br,
				))
			}
			continue
		}
		if ar.StatusCode != 0 && br.StatusCode != 0 && ar.StatusCode != br.StatusCode {
			diffs = append(diffs, makeDifference(
				ar.Start, DiffStatus,
				fmt.Sprintf("status is %d on strict side and %d on alternate side", ar.StatusCode, br.StatusCode),
				cross, a, b, ar, br,
			))
		}
		if ar.HeaderEnd != br.HeaderEnd {
			diffs = append(diffs, makeDifference(
				minInt(ar.HeaderEnd, br.HeaderEnd), DiffHeaderBoundary,
				fmt.Sprintf("header ends at byte %d on strict side and byte %d on alternate side", ar.HeaderEnd, br.HeaderEnd),
				cross, a, b, ar, br,
			))
		}
		if ar.BodyStart != br.BodyStart || ar.BodyEnd != br.BodyEnd || ar.End != br.End {
			off := minInt(ar.End, br.End)
			diffs = append(diffs, makeDifference(
				off, DiffBodyBoundary,
				fmt.Sprintf("body/response interval is [%d,%d) on strict side and [%d,%d) on alternate side", ar.BodyStart, ar.End, br.BodyStart, br.End),
				cross, a, b, ar, br,
			))
		}
		if ar.Boundary != br.Boundary {
			diffs = append(diffs, makeDifference(
				minInt(ar.HeaderEnd, br.HeaderEnd), DiffBoundaryPolicy,
				fmt.Sprintf("strict boundary is %q and alternate boundary is %q", ar.Boundary, br.Boundary),
				cross, a, b, ar, br,
			))
		}
		if ar.DeclaredLength != br.DeclaredLength {
			diffs = append(diffs, makeDifference(
				minInt(ar.HeaderEnd, br.HeaderEnd), DiffBoundaryPolicy,
				fmt.Sprintf("declared Content-Length is %d on strict side and %d on alternate side", ar.DeclaredLength, br.DeclaredLength),
				cross, a, b, ar, br,
			))
		}
		if ar.NoBody != br.NoBody {
			diffs = append(diffs, makeDifference(
				minInt(ar.HeaderEnd, br.HeaderEnd), DiffBoundaryPolicy,
				"strict and alternate strategies disagree about whether the response has a body",
				cross, a, b, ar, br,
			))
		}
		if ar.Informational != br.Informational {
			diffs = append(diffs, makeDifference(
				ar.Start, DiffInformational,
				"strict and alternate strategies disagree about the informational status",
				cross, a, b, ar, br,
			))
		}
		if ar.Truncated != br.Truncated ||
			(ar.ExpectedEnd >= 0 && br.ExpectedEnd >= 0 && ar.ExpectedEnd != br.ExpectedEnd) {
			off := minInt(ar.End, br.End)
			if ar.Truncated || br.Truncated {
				off = maxIntValue(off, len(a.Raw))
			}
			diffs = append(diffs, makeDifference(
				off, DiffTruncation,
				fmt.Sprintf("truncation is %v/%v with expected ends %d/%d", ar.Truncated, br.Truncated, ar.ExpectedEnd, br.ExpectedEnd),
				cross, a, b, ar, br,
			))
		}
	}
	return diffs
}

func compareErrors(a, b ParseResult) []Difference {
	var diffs []Difference
	switch {
	case a.Err == nil && b.Err == nil:
		return nil
	case a.Err != nil && b.Err != nil:
		if a.Err.Kind == b.Err.Kind && a.Err.Offset == b.Err.Offset {
			return nil
		}
		aOff := clampOffset(a.Err.Offset, len(a.Raw))
		bOff := clampOffset(b.Err.Offset, len(b.Raw))
		off := minInt(aOff, bOff)
		diffs = append(diffs, makeDifference(
			off, DiffParseError,
			fmt.Sprintf("strict stops with %s at byte %d; alternate stops with %s at byte %d", a.Err.Kind, aOff, b.Err.Kind, bOff),
			false, a, b, nearestResponse(a.Responses, off), nearestResponse(b.Responses, off),
		))
	case a.Err != nil:
		off := clampOffset(a.Err.Offset, len(a.Raw))
		br := nearestResponse(b.Responses, off)
		ar := nearestResponse(a.Responses, off)
		cross := ar != nil && br != nil && ar.RequestIndex >= 0 && br.RequestIndex >= 0 && ar.RequestIndex != br.RequestIndex
		diffs = append(diffs, makeDifference(
			off, DiffParseError,
			fmt.Sprintf("strict stops with %s at byte %d; alternate accepts the stream", a.Err.Kind, a.Err.Offset),
			cross, a, b, ar, br,
		))
	case b.Err != nil:
		off := clampOffset(b.Err.Offset, len(b.Raw))
		ar := nearestResponse(a.Responses, off)
		br := nearestResponse(b.Responses, off)
		cross := ar != nil && br != nil && ar.RequestIndex >= 0 && br.RequestIndex >= 0 && ar.RequestIndex != br.RequestIndex
		diffs = append(diffs, makeDifference(
			off, DiffParseError,
			fmt.Sprintf("alternate stops with %s at byte %d; strict accepts the stream", b.Err.Kind, b.Err.Offset),
			cross, a, b, ar, br,
		))
	}
	return diffs
}

func clampOffset(off, rawLen int) int {
	if off < 0 {
		return 0
	}
	if off > rawLen {
		return rawLen
	}
	return off
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxIntValue(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func differencePriority(kind DifferenceKind) int {
	switch kind {
	case DiffOwnership:
		return 0
	case DiffMissingOnStrict, DiffMissingOnAlternate:
		return 1
	case DiffParseError:
		return 2
	case DiffResponseIndex, DiffStatus, DiffInformational:
		return 3
	case DiffHeaderBoundary, DiffBodyBoundary:
		return 4
	case DiffBoundaryPolicy, DiffTruncation:
		return 5
	default:
		return 6
	}
}

func sortDifferences(diffs []Difference) []Difference {
	sort.SliceStable(diffs, func(i, j int) bool {
		if diffs[i].Offset != diffs[j].Offset {
			return diffs[i].Offset < diffs[j].Offset
		}
		pi, pj := differencePriority(diffs[i].Kind), differencePriority(diffs[j].Kind)
		if pi != pj {
			return pi < pj
		}
		if diffs[i].StrictResponseIndex != diffs[j].StrictResponseIndex {
			return diffs[i].StrictResponseIndex < diffs[j].StrictResponseIndex
		}
		return diffs[i].AlternateResponseIndex < diffs[j].AlternateResponseIndex
	})

	seen := make(map[string]struct{}, len(diffs))
	out := make([]Difference, 0, len(diffs))
	for _, d := range diffs {
		key := fmt.Sprintf("%s|%d|%d|%d|%d", d.Kind, d.StrictResponseIndex, d.AlternateResponseIndex, d.StrictRequestIndex, d.AlternateRequestIndex)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, d)
	}
	return out
}

// Verify parses the raw response stream with ParseStrict and an alternate
// strategy, then compares request ownership and byte intervals. If alternate
// is omitted or nil, ParseLenient is used.
func Verify(methods []string, raw []byte, alternate ...any) Report {
	rawCopy := append([]byte(nil), raw...)
	methodsCopy := append([]string(nil), methods...)

	strict := parseResponses(methodsCopy, rawCopy, "strict", false)
	var alt ParseResult
	if len(alternate) > 0 && !isNilParser(alternate[0]) {
		alt = invokeAlternate(alternate[0], methodsCopy, rawCopy)
	} else {
		if DefaultAlternate != nil {
			alt = DefaultAlternate(methodsCopy, rawCopy)
		} else {
			alt = parseResponses(methodsCopy, rawCopy, "lenient", true)
		}
	}
	normalizeParseResult(&alt, methodsCopy, rawCopy)
	normalizeParseResult(&strict, methodsCopy, rawCopy)

	report := Report{
		Methods:   methodsCopy,
		Raw:       rawCopy,
		Strict:    strict,
		Alternate: alt,
	}
	report.Differences = compareResults(report.Strict, report.Alternate, len(rawCopy))
	if len(report.Differences) > 0 {
		report.Earliest = &report.Differences[0]
	}
	report.Findings = mergeFindings(report.Strict, report.Alternate)

	for _, d := range report.Differences {
		if d.CrossRequest {
			report.CrossRequest = true
			break
		}
	}
	unattributed := false
	for _, f := range report.Findings {
		if f.Kind == FindingUnattributedResponse {
			unattributed = true
			break
		}
	}

	var reasons []string
	if report.CrossRequest {
		reasons = append(reasons, "cross-request ownership difference")
	}
	if report.Strict.Err != nil {
		reasons = append(reasons, "strict: "+report.Strict.Err.Error())
	}
	if report.Alternate.Err != nil {
		reasons = append(reasons, "alternate: "+report.Alternate.Err.Error())
	}
	if unattributed {
		reasons = append(reasons, "response has no request in the method queue")
	}
	if len(reasons) == 0 && len(report.Differences) > 0 {
		reasons = append(reasons, fmt.Sprintf("strategies disagree at byte %d", report.Differences[0].Offset))
	}

	report.Blocked = report.CrossRequest || report.Strict.Err != nil || report.Alternate.Err != nil || unattributed
	if report.Blocked {
		report.Verdict = VerdictBlocked
	} else if len(report.Differences) > 0 {
		report.Verdict = VerdictDiverged
	} else {
		report.Verdict = VerdictConsistent
	}
	if report.Blocked {
		report.BlockReason = joinReasons(reasons)
	}
	return report
}

func mergeFindings(results ...ParseResult) []Finding {
	var out []Finding
	for _, result := range results {
		for _, f := range result.Findings {
			key := fmt.Sprintf("%s|%s|%d|%d|%d", f.Kind, f.Strategy, f.ResponseIndex, f.RequestIndex, f.Offset)
			duplicate := false
			for _, existing := range out {
				ekey := fmt.Sprintf("%s|%s|%d|%d|%d", existing.Kind, existing.Strategy, existing.ResponseIndex, existing.RequestIndex, existing.Offset)
				if key == ekey {
					duplicate = true
					break
				}
			}
			if !duplicate {
				out = append(out, f)
			}
		}
	}
	return out
}

func joinReasons(reasons []string) string {
	if len(reasons) == 0 {
		return ""
	}
	joined := reasons[0]
	for _, reason := range reasons[1:] {
		joined += "; " + reason
	}
	return joined
}

// VerifyRaw is Verify with the raw bytes first, for callers that prefer that
// argument order.
func VerifyRaw(raw []byte, methods []string, alternate ...any) Report {
	return Verify(methods, raw, alternate...)
}
