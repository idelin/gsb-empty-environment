package h2h1

import (
	"bytes"
	"encoding/hex"
	"errors"
	"io"
	"testing"
)

func TestRawFrameRoundTrip(t *testing.T) {
	f := mustFrame(FrameHeaders, FlagHeadersEndHeaders|FlagHeadersEndStream, 7, []byte{0x82, 0x84})
	raw := f.Bytes()
	got, rest, err := ParseFrameBytes(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(rest) != 0 {
		t.Fatalf("remaining bytes: %d", len(rest))
	}
	if got.Header.Type != FrameHeaders || got.Header.StreamID != 7 || got.Header.Flags != f.Header.Flags {
		t.Fatalf("bad parsed header: %+v", got.Header)
	}
	if !bytes.Equal(got.Payload, f.Payload) {
		t.Fatal("payload changed")
	}

	_, _, err = ParseFrameBytes(raw[:5])
	if err == nil {
		t.Fatal("expected truncated frame header error")
	}

	rd := bytes.NewReader(append(raw, raw...))
	frames, err := ParseFrameStream(rd)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 2 {
		t.Fatalf("frames=%d", len(frames))
	}
	if _, err := ReadFrame(bytes.NewReader(nil)); !errors.Is(err, io.EOF) {
		t.Fatalf("empty reader error = %v", err)
	}
}

func TestFramePaddingExtraction(t *testing.T) {
	data := mustFrame(FrameData, FlagDataPadded, 1, []byte{2, 'a', 'b', 0xee, 0xee})
	body, err := data.DataBody()
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "ab" {
		t.Fatalf("body=%q", body)
	}

	headers := mustFrame(FrameHeaders, FlagHeadersPadded|FlagHeadersPriority|FlagHeadersEndHeaders,
		1, []byte{1, 0, 0, 0, 1, 0, 0x82, 0xee})
	frag, err := headers.HeaderBlockFragment()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(frag, []byte{0x82}) {
		t.Fatalf("fragment=%x", frag)
	}

	push := mustFrame(FramePushPromise, FlagPushPromisePadded|FlagPushPromiseEndHeaders,
		1, []byte{1, 0, 0, 0, 2, 0x82, 0xee})
	frag, err = push.HeaderBlockFragment()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(frag, []byte{0x82}) {
		t.Fatalf("push fragment=%x", frag)
	}
}

func TestHuffmanRFCVectorsAndStrictness(t *testing.T) {
	vectors := []struct {
		plain string
		hex   string
	}{
		{"www.example.com", "f1e3c2e5f23a6ba0ab90f4ff"},
		{"no-cache", "a8eb10649cbf"},
	}
	for _, vector := range vectors {
		encoded, err := hex.DecodeString(vector.hex)
		if err != nil {
			t.Fatal(err)
		}
		got, err := HuffmanDecode(encoded)
		if err != nil {
			t.Fatalf("%q: %v", vector.plain, err)
		}
		if string(got) != vector.plain {
			t.Fatalf("%q decoded as %q", vector.plain, got)
		}
		if got := HuffmanEncode([]byte(vector.plain)); !bytes.Equal(got, encoded) {
			t.Fatalf("%q encoded as %x, want %x", vector.plain, got, encoded)
		}
	}

	for i := 0; i < 256; i++ {
		plain := []byte{byte(i)}
		encoded := HuffmanEncode(plain)
		got, err := HuffmanDecode(encoded)
		if err != nil {
			t.Fatalf("roundtrip byte %d: %v", i, err)
		}
		if !bytes.Equal(got, plain) {
			t.Fatalf("roundtrip byte %d: %x", i, got)
		}
	}

	if _, err := HuffmanDecode([]byte{0xff, 0xff, 0xff, 0xff}); err == nil {
		t.Fatal("expected EOS rejection")
	}
	badPadding := HuffmanEncode([]byte("a"))
	badPadding[len(badPadding)-1] &^= 0x07
	if _, err := HuffmanDecode(badPadding); err == nil {
		t.Fatal("expected non-ones padding rejection")
	}
	tooMuchPadding := append(HuffmanEncode([]byte("x")), 0xff)
	if _, err := HuffmanDecode(tooMuchPadding); err == nil {
		t.Fatal("expected >7 bits padding rejection")
	}
}

func TestHPACKRFCRequestExample(t *testing.T) {
	block, err := hex.DecodeString("828684410f7777772e6578616d706c652e636f6d")
	if err != nil {
		t.Fatal(err)
	}
	d := NewHPACKDecoder(4096)
	fields, err := d.Decode(block)
	if err != nil {
		t.Fatal(err)
	}
	want := []HeaderField{
		{Name: ":method", Value: "GET"},
		{Name: ":scheme", Value: "http"},
		{Name: ":path", Value: "/"},
		{Name: ":authority", Value: "www.example.com"},
	}
	if len(fields) != len(want) {
		t.Fatalf("fields=%+v", fields)
	}
	for i := range want {
		if fields[i] != want[i] {
			t.Fatalf("field %d=%+v want %+v", i, fields[i], want[i])
		}
	}
}

func TestHPACKStaticDynamicAndSizeUpdates(t *testing.T) {
	if len(StaticTable) != 62 || StaticTable[1].Name != ":authority" || StaticTable[61].Name != "www-authenticate" {
		t.Fatalf("static table shape is wrong: len=%d", len(StaticTable))
	}
	d := NewHPACKDecoder(4096)
	fields, err := d.Decode([]byte{0x82, 0x84})
	if err != nil {
		t.Fatal(err)
	}
	if len(fields) != 2 || fields[0].Name != ":method" || fields[0].Value != "GET" ||
		fields[1].Name != ":path" || fields[1].Value != "/" {
		t.Fatalf("fields=%+v", fields)
	}

	block := EncodeLiteralHeader("x-dynamic", "value", true, false)
	fields, err = d.Decode(block)
	if err != nil {
		t.Fatal(err)
	}
	if len(fields) != 1 || fields[0].Name != "x-dynamic" || d.DynamicTableEntries() != 1 {
		t.Fatalf("fields=%+v dynamic=%d", fields, d.DynamicTableEntries())
	}
	indexed, _ := EncodeIndexedHeader(62)
	fields, err = d.Decode(indexed)
	if err != nil {
		t.Fatal(err)
	}
	if fields[0].Name != "x-dynamic" || fields[0].Value != "value" {
		t.Fatalf("dynamic lookup=%+v", fields)
	}

	update := EncodeTableSizeUpdate(64)
	block = append(update, 0x82)
	fields, err = d.Decode(block)
	if err != nil {
		t.Fatal(err)
	}
	if fields[0].Value != "GET" || d.TableSize() > 64 {
		t.Fatalf("fields=%+v tableSize=%d", fields, d.TableSize())
	}

	bad := append([]byte{0x82}, EncodeTableSizeUpdate(0)...)
	if _, err := d.Decode(bad); err == nil {
		t.Fatal("expected late table size update rejection")
	}
	tooLarge := append(EncodeTableSizeUpdate(5000), 0x82)
	if _, err := d.Decode(tooLarge); err == nil {
		t.Fatal("expected over-SETTINGS table size update rejection")
	}
	if _, err := d.Decode([]byte{0x80}); err == nil {
		t.Fatal("expected index zero rejection")
	}
}

func TestHPACKHuffmanString(t *testing.T) {
	d := NewHPACKDecoder(4096)
	fields := []HeaderField{{Name: ":method", Value: "GET"}, {Name: "user-agent", Value: "www.example.com"}}
	block := EncodeHeaderBlockHuffman(fields)
	got, err := d.Decode(block)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1].Value != "www.example.com" {
		t.Fatalf("got=%+v", got)
	}

	bads := [][]byte{
		{0x00, 0x84, 'a', 'b', 0x00, 0x00},
		{0x00, 0x81, 0xff, 0x01, 'v'},
	}
	for _, block := range bads {
		if _, err := d.Decode(block); err == nil {
			t.Fatalf("expected bad Huffman string rejection for %x", block)
		}
	}
}

func TestHeaderSemantics(t *testing.T) {
	valid := []HeaderField{
		{Name: ":method", Value: "POST"},
		{Name: ":scheme", Value: "https"},
		{Name: ":path", Value: "/submit"},
		{Name: ":authority", Value: "example.com"},
		{Name: "content-length", Value: "4"},
		{Name: "content-length", Value: "4"},
		{Name: "te", Value: "Trailers"},
	}
	meta, err := ValidateRequestHeaders(valid)
	if err != nil {
		t.Fatal(err)
	}
	if !meta.HasContentLength || meta.ContentLength != 4 {
		t.Fatalf("meta=%+v", meta)
	}

	bad := []struct {
		name   string
		fields []HeaderField
	}{
		{"uppercase", []HeaderField{{Name: ":method", Value: "GET"}, {Name: ":scheme", Value: "https"}, {Name: ":path", Value: "/"}, {Name: "X-Test", Value: "1"}}},
		{"pseudo order", []HeaderField{{Name: ":method", Value: "GET"}, {Name: "cookie", Value: "x"}, {Name: ":scheme", Value: "https"}, {Name: ":path", Value: "/"}}},
		{"dup method", []HeaderField{{Name: ":method", Value: "GET"}, {Name: ":method", Value: "POST"}, {Name: ":scheme", Value: "https"}, {Name: ":path", Value: "/"}}},
		{"missing path", []HeaderField{{Name: ":method", Value: "GET"}, {Name: ":scheme", Value: "https"}}},
		{"connection", []HeaderField{{Name: ":method", Value: "GET"}, {Name: ":scheme", Value: "https"}, {Name: ":path", Value: "/"}, {Name: "connection", Value: "close"}}},
		{"te", []HeaderField{{Name: ":method", Value: "GET"}, {Name: ":scheme", Value: "https"}, {Name: ":path", Value: "/"}, {Name: "te", Value: "gzip"}}},
		{"crlf", []HeaderField{{Name: ":method", Value: "GET"}, {Name: ":scheme", Value: "https"}, {Name: ":path", Value: "/"}, {Name: "x", Value: "a\r\nb"}}},
		{"nul", []HeaderField{{Name: ":method", Value: "GET"}, {Name: ":scheme", Value: "https"}, {Name: ":path", Value: "/"}, {Name: "x", Value: "a\x00b"}}},
		{"cl conflict", []HeaderField{{Name: ":method", Value: "POST"}, {Name: ":scheme", Value: "https"}, {Name: ":path", Value: "/"}, {Name: "content-length", Value: "1"}, {Name: "content-length", Value: "2"}}},
	}
	for _, test := range bad {
		if _, err := ValidateRequestHeaders(test.fields); err == nil {
			t.Errorf("%s: expected rejection", test.name)
		}
	}
}

func TestH2DecoderNormalMultiplex(t *testing.T) {
	frames, err := GenerateNormalMultiplexFrames()
	if err != nil {
		t.Fatal(err)
	}
	requests, err := ParseH2Requests(MarshalFrames(frames))
	if err != nil {
		t.Fatal(err)
	}
	if len(requests) != 3 {
		t.Fatalf("requests=%d", len(requests))
	}
	byID := make(map[uint32]*Request)
	for _, req := range requests {
		byID[req.StreamID] = req
	}
	if string(byID[1].Body) != "abcde" || string(byID[3].Body) != "1234567" || len(byID[5].Body) != 0 {
		t.Fatalf("bodies: 1=%q 3=%q 5=%q", byID[1].Body, byID[3].Body, byID[5].Body)
	}
	for _, req := range requests {
		h1, err := RenderH1Request(req)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := ParseH1Request(h1)
		if err != nil {
			t.Fatalf("stream %d H1 parse: %v\n%s", req.StreamID, err, h1)
		}
		if len(parsed.Body) != len(req.Body) {
			t.Fatalf("stream %d body mismatch", req.StreamID)
		}
	}
}

func TestAdversarialCasesAllRejected(t *testing.T) {
	cases := GenerateAdversarialCases()
	if len(cases) < 20 {
		t.Fatalf("suspiciously few attack cases: %d", len(cases))
	}
	report := VerifyDefense()
	if !report.OK() {
		for _, result := range report.Cases {
			if !result.Rejected {
				t.Errorf("attack accepted: %s", result.Name)
			}
		}
		for _, result := range report.Normal {
			if !result.Passed {
				t.Errorf("normal stream %d failed: %s", result.StreamID, result.Error)
			}
		}
		t.Fatalf("report rejected=%d accepted=%d normalPassed=%d normalFailed=%d",
			report.Rejected, report.Accepted, report.NormalPassed, report.NormalFailed)
	}
	if err := RunSelfCheck(); err != nil {
		t.Fatal(err)
	}
}

func TestContinuationContinuity(t *testing.T) {
	fields := baseRequestFields("GET", "/safe")
	block := EncodeHeaderBlock(fields)
	head, err := BuildHeaderFrames(1, block, false, 1)
	if err != nil {
		t.Fatal(err)
	}
	_, err = ParseH2Requests(MarshalFrames([]Frame{
		head[0],
		mustFrame(FramePing, 0, 0, []byte{1, 2, 3, 4, 5, 6, 7, 8}),
		head[1],
	}))
	if err == nil {
		t.Fatal("expected interleaved PING rejection")
	}
}

func TestDowngradeHasCanonicalFraming(t *testing.T) {
	req := &Request{
		Method:    "POST",
		Scheme:    "https",
		Path:      "/submit",
		Authority: "victim.example",
		Headers: []HeaderField{
			{Name: ":method", Value: "POST"},
			{Name: ":scheme", Value: "https"},
			{Name: ":path", Value: "/submit"},
			{Name: ":authority", Value: "victim.example"},
			{Name: "content-length", Value: "3"},
			{Name: "te", Value: "trailers"},
			{Name: "x-normal", Value: "ok"},
		},
		Body:             []byte("abc"),
		ContentLength:    3,
		HasContentLength: true,
	}
	h1, err := RenderH1Request(req)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Count(h1, []byte("Content-Length:")) != 1 {
		t.Fatalf("Content-Length count:\n%s", h1)
	}
	if bytes.Contains(h1, []byte("\r\nTE:")) || bytes.Contains(h1, []byte("\r\nTransfer-Encoding")) {
		t.Fatalf("unexpected TE:\n%s", h1)
	}
	parsed, err := ParseH1Request(h1)
	if err != nil {
		t.Fatalf("%v\n%s", err, h1)
	}
	if string(parsed.Body) != "abc" {
		t.Fatalf("body=%q", parsed.Body)
	}
}

func TestStrictH1ParserRejectsSmuggling(t *testing.T) {
	valid := "GET /safe HTTP/1.1\r\nHost: victim.example\r\n\r\n"
	if _, err := ParseH1Request([]byte(valid)); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
	binaryBody := "POST /upload HTTP/1.1\r\nHost: victim.example\r\nContent-Length: 5\r\n\r\nA\x00\r\nB"
	if parsed, err := ParseH1Request([]byte(binaryBody)); err != nil {
		t.Fatalf("binary body rejected: %v", err)
	} else if string(parsed.Body) != "A\x00\r\nB" {
		t.Fatalf("binary body=%q", parsed.Body)
	}
	bad := []string{
		valid + "GET /admin HTTP/1.1\r\nHost: victim.example\r\n\r\n",
		"GET /safe HTTP/1.1\r\nHost: victim.example\r\nTransfer-Encoding: chunked\r\n\r\n0\r\n\r\n",
		"GET /safe HTTP/1.1\r\nHost: victim.example\r\nContent-Length: 1\r\nContent-Length: 2\r\n\r\nab",
		"GET /safe HTTP/1.1\r\nHost: victim.example\r\nContent-Length: 3\r\n\r\nabcGET /admin HTTP/1.1\r\n\r\n",
		"GET /safe HTTP/1.1\nHost: victim.example\n\n",
		"GET /safe HTTP/1.1\r\nHost: victim.example\r\n folded\r\n\r\n",
		"GET /safe HTTP/1.1\r\nHost: victim.example\r\nX: a\x00b\r\n\r\n",
		"GET /safe HTTP/1.1\r\nHost: victim.example\r\nContent-Length: 3, 3\r\n\r\nabc",
	}
	for _, raw := range bad {
		if _, err := ParseH1Request([]byte(raw)); err == nil {
			t.Errorf("expected rejection: %q", raw)
		}
	}
}

func TestDataLengthMismatch(t *testing.T) {
	fields := append(baseRequestFields("POST", "/safe"), HeaderField{Name: "content-length", Value: "4"})
	frames, err := BuildRequestFrames(1, fields, []byte("abcd"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseH2Requests(MarshalFrames(frames)); err != nil {
		t.Fatalf("valid body rejected: %v", err)
	}

	fields = append(baseRequestFields("POST", "/safe"), HeaderField{Name: "content-length", Value: "4"})
	frames, _ = BuildRequestFrames(1, fields, []byte("abc"), 0)
	if _, err := ParseH2Requests(MarshalFrames(frames)); err == nil {
		t.Fatal("expected truncated body rejection")
	}

	fields = append(baseRequestFields("POST", "/safe"), HeaderField{Name: "content-length", Value: "2"})
	frames, _ = BuildRequestFrames(1, fields, []byte("abcd"), 0)
	if _, err := ParseH2Requests(MarshalFrames(frames)); err == nil {
		t.Fatal("expected overrun body rejection")
	}
}

func TestConfiguredSETTINGSHeaderTableSize(t *testing.T) {
	block := append(EncodeTableSizeUpdate(128), EncodeHeaderBlock(baseRequestFields("GET", "/"))...)
	frames, err := BuildHeaderFrames(1, block, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseH2Requests(MarshalFrames(frames), WithHeaderTableSize(64)); err == nil {
		t.Fatal("expected table size above configured SETTINGS limit to be rejected")
	}

	block = append(EncodeTableSizeUpdate(64), EncodeHeaderBlock(baseRequestFields("GET", "/"))...)
	frames, err = BuildHeaderFrames(1, block, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseH2Requests(MarshalFrames(frames), WithHeaderTableSize(64)); err != nil {
		t.Fatalf("exact SETTINGS limit rejected: %v", err)
	}
}
