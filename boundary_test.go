package taskpkg

import (
	"bytes"
	"testing"
)

func response(status, headers, body string) string {
	return "HTTP/1.1 " + status + "\r\n" + headers + "\r\n" + body
}

func hasFinding(findings []Finding, kind FindingKind) bool {
	for _, f := range findings {
		if f.Kind == kind {
			return true
		}
	}
	return false
}

func TestConsistentContentLength(t *testing.T) {
	raw := []byte(response("200 OK", "Content-Length: 5\r\n", "hello") +
		response("200 OK", "Content-Length: 5\r\n", "world"))
	report := Verify([]string{"GET", "GET"}, raw)

	if !report.OK() || report.Blocked {
		t.Fatalf("expected a clean report, got verdict=%s blocked=%v reason=%q", report.Verdict, report.Blocked, report.BlockReason)
	}
	if report.Earliest != nil {
		t.Fatalf("unexpected earliest difference: %+v", report.Earliest)
	}
	if len(report.Strict.Responses) != 2 {
		t.Fatalf("strict responses = %d", len(report.Strict.Responses))
	}
	if report.Strict.Responses[0].RequestIndex != 0 || report.Strict.Responses[1].RequestIndex != 1 {
		t.Fatalf("unexpected ownership: %+v", report.Strict.Responses)
	}
	if report.Strict.Responses[0].Interval() != (Interval{0, len(response("200 OK", "Content-Length: 5\r\n", "hello"))}) {
		t.Fatalf("unexpected first interval: %+v", report.Strict.Responses[0].Interval())
	}
}

func TestInformationalDoesNotConsumeRequest(t *testing.T) {
	raw := []byte(response("100 Continue", "", "") +
		response("200 OK", "Content-Length: 5\r\n", "hello") +
		response("200 OK", "Content-Length: 5\r\n", "world"))
	report := Verify([]string{"GET", "GET"}, raw)

	if report.Blocked {
		t.Fatalf("informational responses must not block: %s", report.BlockReason)
	}
	if len(report.Strict.Responses) != 3 {
		t.Fatalf("strict responses = %d", len(report.Strict.Responses))
	}
	owners := []int{
		report.Strict.Responses[0].RequestIndex,
		report.Strict.Responses[1].RequestIndex,
		report.Strict.Responses[2].RequestIndex,
	}
	if owners[0] != 0 || owners[1] != 0 || owners[2] != 1 {
		t.Fatalf("1xx consumed a request: owners=%v", owners)
	}
	if !report.Strict.Responses[0].Informational {
		t.Fatal("100 response was not marked informational")
	}
	if !hasFinding(report.Findings, FindingInformational) {
		t.Fatal("missing informational finding")
	}
}

func TestHeadAnd204HaveNoBody(t *testing.T) {
	t.Run("HEAD", func(t *testing.T) {
		raw := []byte(response("200 OK", "Content-Length: 6\r\n", "") + response("200 OK", "Content-Length: 6\r\n", "second"))
		report := Verify([]string{"HEAD", "GET"}, raw)
		if report.Blocked {
			t.Fatalf("HEAD response was treated as having a body: %s", report.BlockReason)
		}
		if len(report.Strict.Responses) != 2 {
			t.Fatalf("strict responses = %d", len(report.Strict.Responses))
		}
		if report.Strict.Responses[0].BodyInterval().Len() != 0 || report.Strict.Responses[1].Body != nil && string(report.Strict.Responses[1].Body) != "second" {
			t.Fatalf("unexpected HEAD/second body evidence: %+v", report.Strict.Responses)
		}
	})

	t.Run("204", func(t *testing.T) {
		raw := []byte(response("204 No Content", "Content-Length: 5\r\n", "") + response("200 OK", "Content-Length: 5\r\n", "world"))
		report := Verify([]string{"GET", "GET"}, raw)
		if report.Blocked {
			t.Fatalf("204 response was treated as having a body: %s", report.BlockReason)
		}
		if len(report.Strict.Responses) != 2 {
			t.Fatalf("strict responses = %d", len(report.Strict.Responses))
		}
		if report.Strict.Responses[0].BodyInterval().Len() != 0 {
			t.Fatalf("204 body interval = %+v", report.Strict.Responses[0].BodyInterval())
		}
	})
}

func TestChunkedIsRejectedAndCompared(t *testing.T) {
	raw := []byte(response("200 OK", "Transfer-Encoding: chunked\r\n", "5\r\nhello\r\n0\r\n\r\n") +
		response("200 OK", "Content-Length: 5\r\n", "world"))
	report := Verify([]string{"GET", "GET"}, raw)

	if !report.Blocked {
		t.Fatal("chunked response should block")
	}
	if report.Strict.Err == nil || report.Strict.Err.Kind != ErrChunked {
		t.Fatalf("strict error = %+v", report.Strict.Err)
	}
	if len(report.Alternate.Responses) != 2 || !report.Alternate.Responses[0].Chunked {
		t.Fatalf("alternate did not parse chunked response: %+v", report.Alternate.Responses)
	}
	if report.Earliest == nil || report.Earliest.Kind != DiffParseError {
		t.Fatalf("earliest difference = %+v", report.Earliest)
	}
	if !hasFinding(report.Findings, FindingChunked) {
		t.Fatal("missing chunked finding")
	}
}

func TestRepeatedContentLength(t *testing.T) {
	raw := []byte(response("200 OK", "Content-Length: 5\r\nContent-Length: 5\r\n", "hello") +
		response("200 OK", "Content-Length: 5\r\n", "world"))
	report := Verify([]string{"GET", "GET"}, raw)

	if !report.Blocked {
		t.Fatal("repeated Content-Length should block")
	}
	if report.Strict.Err == nil || report.Strict.Err.Kind != ErrRepeatedContentLength {
		t.Fatalf("strict error = %+v", report.Strict.Err)
	}
	if report.Earliest == nil || report.Earliest.Kind != DiffParseError {
		t.Fatalf("earliest difference = %+v", report.Earliest)
	}
	if !hasFinding(report.Findings, FindingRepeatedLength) {
		t.Fatal("missing repeated-length finding")
	}
	if len(report.Alternate.Responses) != 2 {
		t.Fatalf("alternate responses = %d", len(report.Alternate.Responses))
	}
}

func TestEarlyClose(t *testing.T) {
	raw := []byte(response("200 OK", "Content-Length: 10\r\n", "hello"))
	report := Verify([]string{"GET"}, raw)

	if !report.Blocked {
		t.Fatal("early close should block")
	}
	if report.Strict.Err == nil || report.Strict.Err.Kind != ErrEarlyClose {
		t.Fatalf("strict error = %+v", report.Strict.Err)
	}
	if !report.Strict.Responses[0].Truncated {
		t.Fatal("strict response was not marked truncated")
	}
	if report.Strict.Responses[0].ExpectedEnd <= len(raw) {
		t.Fatalf("expected end = %d, raw length = %d", report.Strict.Responses[0].ExpectedEnd, len(raw))
	}
	if report.Earliest == nil || report.Earliest.Kind != DiffParseError || report.Earliest.Offset != len(raw) {
		t.Fatalf("earliest difference = %+v", report.Earliest)
	}
	if !hasFinding(report.Findings, FindingEarlyClose) {
		t.Fatal("missing early-close finding")
	}
}

func TestCloseDelimitedBodyIsAllowed(t *testing.T) {
	raw := []byte(response("200 OK", "", "hello"))
	report := Verify([]string{"GET"}, raw)
	if report.Blocked {
		t.Fatalf("single close-delimited response should be accepted: %s", report.BlockReason)
	}
	if report.Strict.Responses[0].Boundary != BoundaryClose {
		t.Fatalf("boundary = %q", report.Strict.Responses[0].Boundary)
	}
	if !hasFinding(report.Findings, FindingCloseDelimited) {
		t.Fatal("missing close-delimited finding")
	}
}

func TestFirstByteOfCrossRequestOwnershipIsReported(t *testing.T) {
	first := response("200 OK", "", "")
	second := response("200 OK", "Content-Length: 5\r\n", "world")
	raw := []byte(first + second)
	secondStart := len(first)

	report := Verify([]string{"GET", "GET"}, raw, scanStatusLines)
	if !report.Blocked || !report.CrossRequest {
		t.Fatalf("expected cross-request block, verdict=%s blocked=%v cross=%v reason=%s", report.Verdict, report.Blocked, report.CrossRequest, report.BlockReason)
	}
	if report.Earliest == nil || report.Earliest.Kind != DiffOwnership {
		t.Fatalf("earliest difference = %+v", report.Earliest)
	}
	if report.Earliest.Offset != secondStart {
		t.Fatalf("earliest offset = %d, want %d", report.Earliest.Offset, secondStart)
	}
	if report.Earliest.StrictRequestIndex != 0 || report.Earliest.AlternateRequestIndex != 1 {
		t.Fatalf("unexpected earliest ownership: %+v", report.Earliest)
	}
	if len(report.Strict.Responses) != 1 || len(report.Alternate.Responses) != 2 {
		t.Fatalf("evidence counts: strict=%d alternate=%d", len(report.Strict.Responses), len(report.Alternate.Responses))
	}
}

func TestInformationalOwnershipDifference(t *testing.T) {
	raw := []byte(response("100 Continue", "", "") +
		response("200 OK", "Content-Length: 5\r\n", "hello") +
		response("200 OK", "Content-Length: 5\r\n", "world"))
	report := Verify([]string{"GET", "GET"}, raw, consumesEveryResponse)

	if !report.Blocked || !report.CrossRequest {
		t.Fatalf("consumed informational response was not blocked: %+v", report.Earliest)
	}
	if report.Earliest == nil || report.Earliest.Kind != DiffOwnership {
		t.Fatalf("earliest difference = %+v", report.Earliest)
	}
	if report.Earliest.StrictRequestIndex != 0 || report.Earliest.AlternateRequestIndex != 1 {
		t.Fatalf("unexpected ownership: %+v", report.Earliest)
	}
}

func TestUnattributedTrailingResponseBlocks(t *testing.T) {
	raw := []byte(response("200 OK", "Content-Length: 5\r\n", "hello") +
		response("200 OK", "Content-Length: 5\r\n", "world"))
	report := Verify([]string{"GET"}, raw)

	if !report.Blocked {
		t.Fatal("trailing response should block")
	}
	if report.CrossRequest {
		t.Fatal("identical strategies should not report a cross-request difference")
	}
	if !hasFinding(report.Findings, FindingUnattributedResponse) {
		t.Fatal("missing unattributed-response finding")
	}
}

func TestSegmentCallback(t *testing.T) {
	first := response("200 OK", "", "")
	second := response("200 OK", "Content-Length: 5\r\n", "world")
	raw := []byte(first + second)
	callback := func(methods []string, raw []byte) ([]Segment, error) {
		return []Segment{
			{RequestIndex: 0, Start: 0, End: len(first), StatusCode: 200, Boundary: BoundaryClose},
			{RequestIndex: 1, Start: len(first), End: len(raw), StatusCode: 200, Boundary: BoundaryContentLength},
		}, nil
	}
	report := Verify([]string{"GET", "GET"}, raw, callback)
	if !report.CrossRequest || !report.Blocked {
		t.Fatalf("segment callback difference was not blocked: %+v", report.Earliest)
	}
	if report.Earliest == nil || report.Earliest.Offset != len(first) {
		t.Fatalf("earliest = %+v", report.Earliest)
	}
}

func TestRunAcceptsEitherArgumentOrder(t *testing.T) {
	raw := []byte(response("200 OK", "Content-Length: 5\r\n", "hello"))
	first := Run([]string{"GET"}, raw)
	second := Run(raw, []string{"GET"})
	if first.Verdict != second.Verdict || first.Blocked != second.Blocked {
		t.Fatalf("argument order changed verdict: %+v vs %+v", first.Verdict, second.Verdict)
	}
}

func TestConflictingLengthsAlsoShowCrossRequest(t *testing.T) {
	raw := []byte(response("200 OK", "Content-Length: 5\r\nContent-Length: 6\r\n", "hello") +
		response("200 OK", "Content-Length: 5\r\n", "world"))
	report := Verify([]string{"GET", "GET"}, raw)
	if !report.Blocked || !report.CrossRequest {
		t.Fatalf("conflicting lengths did not expose cross-request ownership: %+v", report.Earliest)
	}
	if report.Strict.Err == nil || report.Strict.Err.Kind != ErrConflictingContentLength {
		t.Fatalf("strict error = %+v", report.Strict.Err)
	}
}

func TestZeroLengthResponseIsAdjacentToNextResponse(t *testing.T) {
	first := response("200 OK", "Content-Length: 0\r\n", "")
	raw := []byte(first + response("200 OK", "Content-Length: 5\r\n", "world"))
	report := Verify([]string{"GET", "GET"}, raw)
	if report.Blocked {
		t.Fatalf("zero-length response blocked: %s", report.BlockReason)
	}
	if len(report.Strict.Responses) != 2 {
		t.Fatalf("strict responses = %d", len(report.Strict.Responses))
	}
	if report.Strict.Responses[0].End != len(first) || report.Strict.Responses[1].Start != len(first) {
		t.Fatalf("responses are not adjacent: %+v", report.Strict.Responses)
	}
	if report.Strict.Responses[0].BodyInterval().Len() != 0 {
		t.Fatalf("zero-length body interval = %+v", report.Strict.Responses[0].BodyInterval())
	}
}

func TestReversedCallbackArguments(t *testing.T) {
	raw := []byte(response("200 OK", "Content-Length: 5\r\n", "world"))
	callback := func(raw []byte, methods []string) []Response {
		return ParseLenient(methods, raw).Responses
	}
	report := Verify([]string{"GET"}, raw, callback)
	if report.Blocked {
		t.Fatalf("reversed callback blocked: %s", report.BlockReason)
	}
}

func TestNonCrossBoundaryDifferenceDivergesWithoutBlocking(t *testing.T) {
	headers := response("200 OK", "Content-Length: 5\r\n", "")
	raw := []byte(headers + "hello")
	callback := func(methods []string, raw []byte) []Response {
		return []Response{{
			RequestIndex:   0,
			StatusCode:     200,
			Start:          0,
			HeaderEnd:      len(headers),
			BodyStart:      len(headers),
			BodyEnd:        len(headers),
			End:            len(headers),
			Boundary:       BoundaryContentLength,
			DeclaredLength: 5,
			ExpectedEnd:    -1,
		}}
	}
	report := Verify([]string{"GET"}, raw, callback)
	if report.Blocked {
		t.Fatalf("same-request boundary difference should not block: %s", report.BlockReason)
	}
	if report.Verdict != VerdictDiverged {
		t.Fatalf("verdict = %s", report.Verdict)
	}
	if report.Earliest == nil || report.Earliest.CrossRequest {
		t.Fatalf("earliest = %+v", report.Earliest)
	}
	if report.Earliest.Offset != len(headers) {
		t.Fatalf("earliest offset = %d, want %d", report.Earliest.Offset, len(headers))
	}
}

func TestForeignStructSliceCallback(t *testing.T) {
	type foreignSegment struct {
		Request int
		Start   int
		End     int
		Status  int
	}
	first := response("200 OK", "", "")
	raw := []byte(first + response("200 OK", "Content-Length: 5\r\n", "world"))
	callback := func(methods []string, raw []byte) []foreignSegment {
		return []foreignSegment{
			{Request: 0, Start: 0, End: len(first), Status: 200},
			{Request: 1, Start: len(first), End: len(raw), Status: 200},
		}
	}
	report := Verify([]string{"GET", "GET"}, raw, callback)
	if !report.CrossRequest || !report.Blocked {
		t.Fatalf("foreign callback was not adapted: %+v", report.Earliest)
	}
	if report.Earliest == nil || report.Earliest.Offset != len(first) {
		t.Fatalf("earliest = %+v", report.Earliest)
	}
}

// scanStatusLines is a deliberately different strategy: it starts a new
// response at every HTTP status line, even one embedded in a close-delimited
// body.
func scanStatusLines(methods []string, raw []byte) ParseResult {
	prefix := []byte("HTTP/1.1 ")
	var starts []int
	for pos := 0; ; {
		idx := bytes.Index(raw[pos:], prefix)
		if idx < 0 {
			break
		}
		starts = append(starts, pos+idx)
		pos += idx + len(prefix)
	}
	responses := make([]Response, 0, len(starts))
	for i, start := range starts {
		end := len(raw)
		if i+1 < len(starts) {
			end = starts[i+1]
		}
		responses = append(responses, Response{
			Index:          i,
			RequestIndex:   i,
			StatusCode:     200,
			Start:          start,
			HeaderEnd:      start,
			BodyStart:      start,
			BodyEnd:        end,
			End:            end,
			Boundary:       BoundaryClose,
			DeclaredLength: -1,
			ExpectedEnd:    -1,
		})
	}
	return ParseResult{
		Strategy:  "scan",
		Methods:   methods,
		Raw:       raw,
		Responses: responses,
		Minimal:   true,
	}
}

// consumesEveryResponse treats every response, including 1xx, as consuming a
// request.
func consumesEveryResponse(methods []string, raw []byte) ParseResult {
	result := ParseLenient(methods, raw)
	result.Strategy = "consume-every-response"
	for i := range result.Responses {
		result.Responses[i].RequestIndex = i
	}
	return result
}
