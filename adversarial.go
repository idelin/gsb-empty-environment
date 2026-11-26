package h2h1

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
)

// AttackKind classifies an adversarial H2-to-H1 downgrade case.
type AttackKind string

const (
	AttackH2CL          AttackKind = "H2.CL"
	AttackH2TE          AttackKind = "H2.TE"
	AttackCRLFInjection AttackKind = "CRLF-INJECTION"
	AttackNULInjection  AttackKind = "NUL-INJECTION"
	AttackContinuation  AttackKind = "CONTINUATION"
	AttackFraming       AttackKind = "FRAMING"
	AttackHPACK         AttackKind = "HPACK"
	AttackPseudoHeader  AttackKind = "PSEUDO-HEADER"
	AttackConnection    AttackKind = "CONNECTION-HEADER"
)

// AttackCase is a self-contained raw HTTP/2 frame sequence for a weak
// downgrader test harness.
type AttackCase struct {
	Name        string
	Kind        AttackKind
	Description string
	Frames      []Frame
}

// Bytes serialises the complete adversarial frame sequence.
func (c AttackCase) Bytes() []byte {
	return MarshalFrames(c.Frames)
}

// RejectedByDefense feeds the case through the strict decoder and reports
// whether it was rejected. Accepted is always an error for this package's
// security contract.
func (c AttackCase) RejectedByDefense() (bool, error) {
	_, err := ParseH2Requests(c.Bytes())
	if err == nil {
		return false, errors.New("h2h1: adversarial case was accepted by the defense")
	}
	return true, nil
}

func mustFrame(frameType FrameType, flags uint8, streamID uint32, payload []byte) Frame {
	f, err := BuildFrame(frameType, flags, streamID, payload)
	if err != nil {
		panic(err)
	}
	return f
}

// BuildHeaderFrames constructs a HEADERS frame, optionally split across
// CONTINUATION frames. splitAt must be between 1 and len(block)-1. A zero
// splitAt emits a single HEADERS frame.
func BuildHeaderFrames(streamID uint32, block []byte, endStream bool, splitAt int) ([]Frame, error) {
	flags := uint8(0)
	if endStream {
		flags |= FlagHeadersEndStream
	}
	if splitAt <= 0 {
		flags |= FlagHeadersEndHeaders
		frame, err := BuildFrame(FrameHeaders, flags, streamID, block)
		if err != nil {
			return nil, err
		}
		return []Frame{frame}, nil
	}
	if splitAt >= len(block) {
		return nil, errors.New("h2h1: CONTINUATION split leaves no second fragment")
	}
	head, err := BuildFrame(FrameHeaders, flags, streamID, block[:splitAt])
	if err != nil {
		return nil, err
	}
	cont, err := BuildFrame(FrameContinuation, FlagContinuationEndHeaders, streamID, block[splitAt:])
	if err != nil {
		return nil, err
	}
	frames := []Frame{head, cont}
	return frames, nil
}

// BuildRequestFrames constructs a complete legal HTTP/2 request. A splitAt
// value above zero splits the HPACK block immediately after that many bytes.
func BuildRequestFrames(streamID uint32, fields []HeaderField, body []byte, splitAt int) ([]Frame, error) {
	block := EncodeHeaderBlock(fields)
	endStreamOnHeaders := len(body) == 0
	frames, err := BuildHeaderFrames(streamID, block, endStreamOnHeaders, splitAt)
	if err != nil {
		return nil, err
	}
	if len(body) != 0 {
		data, err := BuildFrame(FrameData, FlagDataEndStream, streamID, body)
		if err != nil {
			return nil, err
		}
		frames = append(frames, data)
	}
	return frames, nil
}

func baseRequestFields(method, path string) []HeaderField {
	return []HeaderField{
		{Name: ":method", Value: method},
		{Name: ":scheme", Value: "https"},
		{Name: ":path", Value: path},
		{Name: ":authority", Value: "victim.example"},
	}
}

func requestCase(name string, kind AttackKind, description string, fields []HeaderField, body []byte, splitAt int) AttackCase {
	frames, err := BuildRequestFrames(1, fields, body, splitAt)
	if err != nil {
		panic(err)
	}
	return AttackCase{Name: name, Kind: kind, Description: description, Frames: frames}
}

func headerCase(name string, kind AttackKind, description string, extra HeaderField) AttackCase {
	fields := append(baseRequestFields("GET", "/safe"), extra)
	return requestCase(name, kind, description, fields, nil, 0)
}

// GenerateAdversarialCases returns all H2-to-H1 downgrade smuggling probes.
// Each case is a complete raw frame stream. Header payloads and frame
// sequencing are generated without stdlib HTTP/2 or HPACK code.
func GenerateAdversarialCases() []AttackCase {
	var cases []AttackCase

	// H2.CL: a body carries a hidden HTTP/1 request while Content-Length is
	// deliberately too small.
	fields := append(baseRequestFields("POST", "/upload"), HeaderField{Name: "content-length", Value: "0"})
	cases = append(cases, requestCase(
		"H2.CL-overrun", AttackH2CL,
		"DATA contains a hidden request beyond Content-Length: 0",
		fields, []byte("GET /admin HTTP/1.1\r\nHost: victim.example\r\n\r\n"), 0))

	fields = append(baseRequestFields("POST", "/upload"), HeaderField{Name: "content-length", Value: "5"})
	cases = append(cases, requestCase(
		"H2.CL-truncated", AttackH2CL,
		"END_STREAM arrives before the declared Content-Length",
		fields, []byte("abc"), 0))

	fields = append(baseRequestFields("GET", "/safe"), HeaderField{Name: "content-length", Value: "4"})
	cases = append(cases, requestCase(
		"H2.CL-HEADERS-END_STREAM", AttackH2CL,
		"HEADERS ends the stream while Content-Length requires a body",
		fields, nil, 0))

	// H2.TE: both the H2-only TE value restriction and the forbidden
	// Transfer-Encoding field are exercised.
	cases = append(cases, headerCase(
		"H2.TE-chunked", AttackH2TE,
		"TE: chunked is forbidden in HTTP/2",
		HeaderField{Name: "te", Value: "chunked"}))
	fields = append(baseRequestFields("POST", "/safe"), HeaderField{Name: "transfer-encoding", Value: "chunked"})
	cases = append(cases, requestCase(
		"H2.TE-transfer-encoding", AttackH2TE,
		"HTTP/1 transfer coding leaked into HTTP/2",
		fields, []byte("0\r\n\r\nGET /admin HTTP/1.1\r\nHost: victim.example\r\n\r\n"), 0))
	cases = append(cases, headerCase(
		"H2.TE-list", AttackH2TE,
		"TE contains a transfer coding in addition to trailers",
		HeaderField{Name: "te", Value: "trailers, chunked"}))

	// Header-byte injection. These are legal HPACK strings.
	crlfValue := HeaderField{
		Name:  "x-forwarded-preview",
		Value: "ok\r\nContent-Length: 0\r\n\r\nGET /admin HTTP/1.1\r\nHost: victim.example",
	}
	cases = append(cases, requestCase(
		"CRLF-value", AttackCRLFInjection,
		"HPACK value injects complete HTTP/1 header and request lines",
		append(baseRequestFields("GET", "/safe"), crlfValue), []byte("smuggled"), 0))

	crlfName := HeaderField{
		Name:  "x-test\r\nContent-Length: 0\r\n\r\nGET /admin HTTP/1.1\r\nHost: victim.example",
		Value: "ok",
	}
	cases = append(cases, requestCase(
		"CRLF-name", AttackCRLFInjection,
		"HPACK header name contains CR/LF and a hidden request",
		append(baseRequestFields("GET", "/safe"), crlfName), nil, 0))

	cases = append(cases, headerCase(
		"NUL-value", AttackNULInjection,
		"header value contains NUL",
		HeaderField{Name: "x-test", Value: "safe\x00evil"}))
	cases = append(cases, headerCase(
		"NUL-name", AttackNULInjection,
		"header name contains NUL",
		HeaderField{Name: "x\x00test", Value: "safe"}))

	// CONTINUATION is part of one logical header block and cannot be used to
	// smuggle a complete field boundary past a weak decoder.
	contFields := append(baseRequestFields("GET", "/safe"), HeaderField{
		Name:  "x-preview",
		Value: "ok\r\nContent-Length: 0\r\n\r\nGET /admin HTTP/1.1\r\nHost: victim.example",
	})
	block := EncodeHeaderBlock(contFields)
	split := bytes.Index(block, []byte("\r\nContent-Length"))
	if split <= 0 || split >= len(block) {
		split = len(block) / 2
	} else {
		// Split the CRLF pair itself so a weak concatenator is tempted to
		// treat the fragments as independent text.
		split++
	}
	frames, err := BuildHeaderFrames(1, block, false, split)
	if err != nil {
		panic(err)
	}
	frames = append(frames, mustFrame(FrameData, FlagDataEndStream, 1, []byte("body")))
	cases = append(cases, AttackCase{
		Name:        "CONTINUATION-CRLF-split",
		Kind:        AttackContinuation,
		Description: "injected CRLF header is split across HEADERS and CONTINUATION",
		Frames:      frames,
	})

	fields = append(baseRequestFields("POST", "/split"), HeaderField{Name: "content-length", Value: "0"})
	splitBlock := EncodeHeaderBlock(fields)
	splitFrames, splitErr := BuildHeaderFrames(1, splitBlock, false, len(splitBlock)/2)
	if splitErr != nil {
		panic(splitErr)
	}
	splitFrames = append(splitFrames, mustFrame(FrameData, FlagDataEndStream, 1,
		[]byte("GET /admin HTTP/1.1\r\nHost: victim.example\r\n\r\n")))
	cases = append(cases, AttackCase{
		Name:        "CONTINUATION-H2.CL-split",
		Kind:        AttackContinuation,
		Description: "Content-Length: 0 header block is split before a DATA overrun",
		Frames:      splitFrames,
	})

	validBlock := EncodeHeaderBlock(baseRequestFields("GET", "/safe"))
	head, err := BuildHeaderFrames(1, validBlock, false, 1)
	if err != nil {
		panic(err)
	}
	cases = append(cases, AttackCase{
		Name:        "CONTINUATION-interleaved-other-stream",
		Kind:        AttackContinuation,
		Description: "WINDOW_UPDATE on stream 3 is inserted inside stream 1's CONTINUATION sequence",
		Frames: []Frame{
			head[0],
			mustFrame(FrameWindowUpdate, 0, 3, []byte{0, 0, 0, 1}),
			head[1],
		},
	})

	cases = append(cases, AttackCase{
		Name:        "CONTINUATION-wrong-stream",
		Kind:        AttackContinuation,
		Description: "CONTINUATION switches to a different stream",
		Frames: []Frame{
			head[0],
			mustFrame(FrameContinuation, FlagContinuationEndHeaders, 3, validBlock[1:]),
		},
	})

	cases = append(cases, AttackCase{
		Name:        "CONTINUATION-truncated",
		Kind:        AttackContinuation,
		Description: "HEADERS is never completed by END_HEADERS",
		Frames: []Frame{
			head[0],
			mustFrame(FrameContinuation, 0, 1, validBlock[1:]),
		},
	})

	// HPACK state-machine attacks.
	afterHeader := append(EncodeLiteralHeader(":method", "GET", false, false), EncodeTableSizeUpdate(0)...)
	afterHeader = append(afterHeader, EncodeHeaderBlock(baseRequestFields("GET", "/safe")[1:])...)
	frames = []Frame{mustFrame(FrameHeaders, FlagHeadersEndHeaders|FlagHeadersEndStream, 1, afterHeader)}
	cases = append(cases, AttackCase{
		Name:        "HPACK-size-update-after-header",
		Kind:        AttackHPACK,
		Description: "dynamic table size update appears after the first header",
		Frames:      frames,
	})

	largeUpdate := append(EncodeTableSizeUpdate(5000), EncodeHeaderBlock(baseRequestFields("GET", "/safe"))...)
	cases = append(cases, AttackCase{
		Name:        "HPACK-size-update-over-settings",
		Kind:        AttackHPACK,
		Description: "dynamic table size update exceeds SETTINGS_HEADER_TABLE_SIZE 4096",
		Frames:      []Frame{mustFrame(FrameHeaders, FlagHeadersEndHeaders|FlagHeadersEndStream, 1, largeUpdate)},
	})

	cases = append(cases, AttackCase{
		Name:        "HPACK-index-zero",
		Kind:        AttackHPACK,
		Description: "indexed representation uses the reserved index zero",
		Frames:      []Frame{mustFrame(FrameHeaders, FlagHeadersEndHeaders|FlagHeadersEndStream, 1, []byte{0x80})},
	})

	// A legal HPACK Huffman string with more than seven all-ones padding bits.
	badHuffmanName := []byte{0x00, 0x82}
	badHuffmanName = append(badHuffmanName, HuffmanEncode([]byte("x"))...)
	badHuffmanName = append(badHuffmanName, 0xff)
	badHuffmanName = append(badHuffmanName, 0x01, 'y')
	cases = append(cases, AttackCase{
		Name:        "HPACK-huffman-bad-padding",
		Kind:        AttackHPACK,
		Description: "Huffman name has eight all-ones padding bits",
		Frames:      []Frame{mustFrame(FrameHeaders, FlagHeadersEndHeaders|FlagHeadersEndStream, 1, badHuffmanName)},
	})

	// Header semantics beyond CL/TE.
	cases = append(cases, headerCase(
		"uppercase-header-name", AttackFraming,
		"HPACK name contains uppercase ASCII",
		HeaderField{Name: "X-Smuggle", Value: "1"}))
	cases = append(cases, requestCase(
		"pseudo-after-regular", AttackPseudoHeader,
		":path follows a regular header",
		[]HeaderField{
			{Name: ":method", Value: "GET"},
			{Name: ":scheme", Value: "https"},
			{Name: ":authority", Value: "victim.example"},
			{Name: "cookie", Value: "a=b"},
			{Name: ":path", Value: "/safe"},
		}, nil, 0))
	cases = append(cases, requestCase(
		"duplicate-method", AttackPseudoHeader,
		":method appears twice",
		[]HeaderField{
			{Name: ":method", Value: "GET"},
			{Name: ":method", Value: "POST"},
			{Name: ":scheme", Value: "https"},
			{Name: ":path", Value: "/safe"},
		}, nil, 0))
	cases = append(cases, requestCase(
		"missing-scheme", AttackPseudoHeader,
		"required :scheme is absent",
		[]HeaderField{
			{Name: ":method", Value: "GET"},
			{Name: ":path", Value: "/safe"},
		}, nil, 0))
	cases = append(cases, requestCase(
		"unknown-status-pseudo", AttackPseudoHeader,
		"response pseudo-header :status appears in a request",
		[]HeaderField{
			{Name: ":method", Value: "GET"},
			{Name: ":scheme", Value: "https"},
			{Name: ":path", Value: "/safe"},
			{Name: ":status", Value: "200"},
		}, nil, 0))
	cases = append(cases, requestCase(
		"path-raw-space", AttackPseudoHeader,
		":path can split the generated HTTP/1 request line",
		[]HeaderField{
			{Name: ":method", Value: "GET"},
			{Name: ":scheme", Value: "https"},
			{Name: ":path", Value: "/safe HTTP/1.1\r\nHost: victim.example"},
		}, nil, 0))

	cases = append(cases, headerCase(
		"connection-header", AttackConnection,
		"Connection is connection-specific and illegal in HTTP/2",
		HeaderField{Name: "connection", Value: "keep-alive"}))
	cases = append(cases, headerCase(
		"keep-alive-header", AttackConnection,
		"Keep-Alive is connection-specific",
		HeaderField{Name: "keep-alive", Value: "timeout=5"}))
	cases = append(cases, headerCase(
		"proxy-connection-header", AttackConnection,
		"Proxy-Connection is connection-specific",
		HeaderField{Name: "proxy-connection", Value: "keep-alive"}))
	cases = append(cases, headerCase(
		"upgrade-header", AttackConnection,
		"Upgrade is connection-specific",
		HeaderField{Name: "upgrade", Value: "h2c"}))

	fields = append(baseRequestFields("POST", "/safe"),
		HeaderField{Name: "content-length", Value: "3"},
		HeaderField{Name: "content-length", Value: "4"})
	cases = append(cases, requestCase(
		"content-length-conflict", AttackH2CL,
		"duplicate Content-Length fields disagree",
		fields, []byte("abcd"), 0))
	cases = append(cases, headerCase(
		"content-length-list", AttackH2CL,
		"single Content-Length contains a comma-separated list",
		HeaderField{Name: "content-length", Value: "3, 4"}))

	// Truncated/absent END_STREAM is a framing failure even without CL.
	headOnly, err := BuildHeaderFrames(1, EncodeHeaderBlock(baseRequestFields("GET", "/safe")), false, 0)
	if err != nil {
		panic(err)
	}
	cases = append(cases, AttackCase{
		Name:        "stream-never-ended",
		Kind:        AttackFraming,
		Description: "request stream reaches EOF without END_STREAM",
		Frames:      headOnly,
	})

	return cases
}

// GenerateSmugglingCases is an alias for GenerateAdversarialCases.
func GenerateSmugglingCases() []AttackCase {
	return GenerateAdversarialCases()
}

// GenerateNormalMultiplexFrames produces three legal, interleaved request
// streams. Stream 1 uses a split HPACK block, streams 1 and 3 carry bodies, and
// stream 5 ends in HEADERS.
func GenerateNormalMultiplexFrames() ([]Frame, error) {
	fields1 := append(baseRequestFields("POST", "/alpha"), HeaderField{Name: "content-length", Value: "5"})
	block1 := EncodeHeaderBlock(fields1)
	headers1, err := BuildHeaderFrames(1, block1, false, 3)
	if err != nil {
		return nil, err
	}

	fields3 := append(baseRequestFields("PUT", "/beta"),
		HeaderField{Name: "content-length", Value: "7"},
		HeaderField{Name: "te", Value: "trailers"},
		HeaderField{Name: "x-normal", Value: "multiplexed"},
	)
	block3 := EncodeHeaderBlock(fields3)
	headers3, err := BuildHeaderFrames(3, block3, false, 0)
	if err != nil {
		return nil, err
	}

	fields5 := baseRequestFields("GET", "/gamma")
	block5 := EncodeHeaderBlock(fields5)
	headers5, err := BuildHeaderFrames(5, block5, true, 0)
	if err != nil {
		return nil, err
	}

	frames := append([]Frame{}, headers1...)
	frames = append(frames, headers3...)
	frames = append(frames, headers5...)
	frames = append(frames,
		mustFrame(FrameData, 0, 1, []byte("ab")),
		mustFrame(FrameData, 0, 3, []byte("123")),
		mustFrame(FrameData, FlagDataEndStream, 1, []byte("cde")),
		mustFrame(FrameData, FlagDataEndStream, 3, []byte("4567")),
	)
	return frames, nil
}

// CaseResult records one adversarial case result.
type CaseResult struct {
	Name     string
	Kind     AttackKind
	Rejected bool
	Error    string
}

// NormalResult records one normal multiplex result and its strict H1 parse.
type NormalResult struct {
	StreamID uint32
	Passed   bool
	Error    string
}

// VerificationReport is the result of the built-in defense self-check.
type VerificationReport struct {
	Cases        []CaseResult
	Rejected     int
	Accepted     int
	Normal       []NormalResult
	NormalPassed int
	NormalFailed int
}

// ZeroSmuggle is true only when every adversarial case was rejected.
func (r VerificationReport) ZeroSmuggle() bool {
	return r.Accepted == 0
}

// NormalAllPassed is true only when every normal multiplex request survived
// H2 decoding, safe H1 rendering, and strict H1 parsing.
func (r VerificationReport) NormalAllPassed() bool {
	return r.NormalFailed == 0
}

// OK is the complete security contract: zero smuggle and all normal requests
// passing.
func (r VerificationReport) OK() bool {
	return r.ZeroSmuggle() && r.NormalAllPassed()
}

// VerifyAdversarialCases runs every case through the defense package.
func VerifyAdversarialCases(cases []AttackCase) []CaseResult {
	results := make([]CaseResult, 0, len(cases))
	for _, c := range cases {
		_, err := ParseH2Requests(c.Bytes())
		result := CaseResult{Name: c.Name, Kind: c.Kind, Rejected: err != nil}
		if err != nil {
			result.Error = err.Error()
		}
		results = append(results, result)
	}
	return results
}

// VerifyNormalMultiplex decodes the built-in interleaved normal streams and
// requires each safe downgrade to be accepted by the strict H1 parser.
func VerifyNormalMultiplex() []NormalResult {
	frames, err := GenerateNormalMultiplexFrames()
	if err != nil {
		return []NormalResult{{Error: err.Error()}}
	}
	requests, err := ParseH2Requests(MarshalFrames(frames))
	if err != nil {
		return []NormalResult{{Error: err.Error()}}
	}
	results := make([]NormalResult, 0, len(requests))
	for _, req := range requests {
		h1, renderErr := RenderH1Request(req)
		if renderErr != nil {
			results = append(results, NormalResult{StreamID: req.StreamID, Error: renderErr.Error()})
			continue
		}
		_, parseErr := ParseH1Request(h1)
		result := NormalResult{StreamID: req.StreamID, Passed: parseErr == nil}
		if parseErr != nil {
			result.Error = parseErr.Error()
		}
		results = append(results, result)
	}
	return results
}

// VerifyDefense runs the complete built-in adversarial and normal suite.
func VerifyDefense() VerificationReport {
	cases := GenerateAdversarialCases()
	report := VerificationReport{Cases: VerifyAdversarialCases(cases)}
	for _, result := range report.Cases {
		if result.Rejected {
			report.Rejected++
		} else {
			report.Accepted++
		}
	}
	report.Normal = VerifyNormalMultiplex()
	for _, result := range report.Normal {
		if result.Passed {
			report.NormalPassed++
		} else {
			report.NormalFailed++
		}
	}
	return report
}

// RunSelfCheck returns nil only when all built-in attacks are rejected and all
// normal multiplex requests pass.
func RunSelfCheck() error {
	report := VerifyDefense()
	if !report.ZeroSmuggle() {
		var accepted []string
		for _, result := range report.Cases {
			if !result.Rejected {
				accepted = append(accepted, result.Name)
			}
		}
		return fmt.Errorf("h2h1: defense accepted smuggling cases: %s", strings.Join(accepted, ", "))
	}
	if !report.NormalAllPassed() {
		var failed []string
		for _, result := range report.Normal {
			if !result.Passed {
				failed = append(failed, fmt.Sprintf("stream %d: %s", result.StreamID, result.Error))
			}
		}
		return fmt.Errorf("h2h1: normal multiplex requests failed: %s", strings.Join(failed, "; "))
	}
	return nil
}
