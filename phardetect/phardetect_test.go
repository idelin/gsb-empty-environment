package phardetect

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

// encodeManifest encodes a little-endian phar manifest following the layout
// required by the task: manifest length, file count, API version, flags,
// alias, metadata length and metadata.
func encodeManifest(fileCount uint32, api uint16, flags uint32, alias, metadata []byte, extra []byte) []byte {
	manifestLength := uint32(18 + len(alias) + 4 + len(metadata) + len(extra))
	buf := bytes.NewBuffer(nil)

	write := func(value any) {
		if err := binary.Write(buf, binary.LittleEndian, value); err != nil {
			panic(err)
		}
	}

	write(manifestLength)
	write(fileCount)
	write(api)
	write(flags)
	write(uint32(len(alias)))
	buf.Write(alias)
	write(uint32(len(metadata)))
	buf.Write(metadata)
	buf.Write(extra)

	return buf.Bytes()
}

func buildManifest(t *testing.T, fileCount uint32, api uint16, flags uint32, alias, metadata []byte, extra []byte) []byte {
	t.Helper()
	manifest := encodeManifest(fileCount, api, flags, alias, metadata, extra)
	if got := binary.LittleEndian.Uint32(manifest[0:4]); int(got) != len(manifest) {
		t.Fatalf("manifest size mismatch: got %d want %d", len(manifest), got)
	}
	return manifest
}

// jpegHeader is a minimal but structurally valid JPEG start-of-image segment.
var jpegHeader = []byte{
	0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10,
	'J', 'F', 'I', 'F', 0x00, 0x01, 0x01, 0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 0x00,
}

// chunkReader delivers content through reads of at most chunkSize bytes so the
// streaming detector proves it does not rely on large or aligned reads.
type chunkReader struct {
	data      []byte
	chunkSize int
}

func (c *chunkReader) Read(p []byte) (int, error) {
	if len(c.data) == 0 {
		return 0, io.EOF
	}
	n := min(c.chunkSize, len(p), len(c.data))
	copy(p, c.data[:n])
	c.data = c.data[n:]
	return n, nil
}

func TestDetectJPEGPolyglotWithMetadata(t *testing.T) {
	alias := []byte("evil.phar")
	metadata := []byte(`O:8:"EvilMeta":0:{}`)
	manifest := buildManifest(t, 1, 0x1100, 0x00010000, alias, metadata, []byte("file-entry-bytes"))

	content := bytes.Join([][]byte{
		jpegHeader,
		[]byte("<?php // harmless looking image stub\n"),
		[]byte(haltMarker),
		{'\n'},
		manifest,
		{0xDE, 0xAD, 0xBE, 0xEF},
	}, nil)

	markerOffset := bytes.Index(content, []byte(haltMarker))

	report, err := DetectBytes(content)
	if err != nil {
		t.Fatalf("DetectBytes returned error: %v", err)
	}
	if report == nil {
		t.Fatal("expected phar report, got nil")
	}
	if report.StubOffset != int64(markerOffset) {
		t.Fatalf("stub offset = %d, want %d", report.StubOffset, markerOffset)
	}
	if report.FileCount != 1 || report.APIVersion != 0x1100 || report.Flags != 0x00010000 {
		t.Fatalf("header fields decoded incorrectly: %+v", report)
	}
	if report.Alias != "evil.phar" {
		t.Fatalf("alias = %q, want evil.phar", report.Alias)
	}
	if report.MetadataLength != uint32(len(metadata)) || !report.HasMetadata {
		t.Fatalf("metadata not flagged: %+v", report)
	}

	for _, chunkSize := range []int{1, 2, 3, 5, 7, 13, 21, 64, 4096} {
		streamReport, streamErr := DetectReader(&chunkReader{
			data:      append([]byte(nil), content...),
			chunkSize: chunkSize,
		})
		if streamErr != nil || streamReport == nil ||
			streamReport.StubOffset != report.StubOffset ||
			streamReport.MetadataLength != report.MetadataLength ||
			!streamReport.HasMetadata {
			t.Fatalf("chunk size %d: report=%+v err=%v", chunkSize, streamReport, streamErr)
		}
	}
}

func TestDetectCRLFAndNoNewline(t *testing.T) {
	manifest := buildManifest(t, 0, 0x1100, 0, nil, nil, nil)

	crlfContent := bytes.Join([][]byte{[]byte("prefix-"), []byte(haltMarker), {'\r', '\n'}, manifest}, nil)
	report, err := DetectBytes(crlfContent)
	if err != nil || report == nil || report.HasMetadata {
		t.Fatalf("CRLF case: report=%+v err=%v", report, err)
	}

	noNewlineContent := bytes.Join([][]byte{[]byte("prefix-"), []byte(haltMarker), manifest}, nil)
	report, err = DetectBytes(noNewlineContent)
	if err != nil || report == nil || report.HasMetadata {
		t.Fatalf("no-newline case: report=%+v err=%v", report, err)
	}

	streamReport, err := DetectReader(bytes.NewReader(crlfContent))
	if err != nil || streamReport == nil || streamReport.HasMetadata {
		t.Fatalf("stream CRLF case: report=%+v err=%v", streamReport, err)
	}
}

func TestDetectNoMetadataArchive(t *testing.T) {
	manifest := buildManifest(t, 2, 0x1100, 0, []byte("alias"), nil, []byte("entries"))
	content := append(append([]byte("stub-"), []byte(haltMarker)...), '\n')
	content = append(content, manifest...)

	report, err := DetectBytes(content)
	if err != nil {
		t.Fatalf("DetectBytes returned error: %v", err)
	}
	if report == nil {
		t.Fatal("expected phar report")
	}
	if report.MetadataLength != 0 || report.HasMetadata {
		t.Fatalf("expected no metadata, got %+v", report)
	}
	if report.FileCount != 2 || report.Alias != "alias" {
		t.Fatalf("unexpected fields: %+v", report)
	}
}

func TestDetectLegitimateJPEG(t *testing.T) {
	content := append(append([]byte(nil), jpegHeader...),
		[]byte("ordinary scan data __HALT_COMPILER without terminator")...)

	report, err := DetectBytes(content)
	if err != nil || report != nil {
		t.Fatalf("DetectBytes: report=%+v err=%v", report, err)
	}

	streamReport, err := DetectReader(bytes.NewReader(content))
	if err != nil || streamReport != nil {
		t.Fatalf("DetectReader: report=%+v err=%v", streamReport, err)
	}
}

func TestDetectMalformedManifest(t *testing.T) {
	manifest := buildManifest(t, 1, 0x1100, 0, nil, []byte("meta"), nil)
	base := append(append([]byte{}, []byte(haltMarker)...), '\n')

	cases := map[string][]byte{
		"truncated header":      append(append([]byte{}, base...), manifest[:10]...),
		"truncated metadata":    append(append([]byte{}, base...), manifest[:len(manifest)-2]...),
		"declared length huge":  append(append([]byte{}, base...), manifestWithLength(0xFFFFFFFF)...),
		"declared length small": append(append([]byte{}, base...), manifestWithLength(10)...),
		"alias overflows":       append(append([]byte{}, base...), manifestWithAliasOverflow()...),
	}

	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := DetectBytes(content); !errors.Is(err, ErrMalformed) {
				t.Fatalf("DetectBytes err = %v, want ErrMalformed", err)
			}
			if _, err := DetectReader(bytes.NewReader(content)); !errors.Is(err, ErrMalformed) {
				t.Fatalf("DetectReader err = %v, want ErrMalformed", err)
			}
		})
	}
}

func manifestWithLength(length uint32) []byte {
	manifest := encodeManifest(0, 0x1100, 0, nil, nil, nil)
	binary.LittleEndian.PutUint32(manifest[0:4], length)
	return manifest
}

func manifestWithAliasOverflow() []byte {
	manifest := encodeManifest(0, 0x1100, 0, nil, nil, nil)
	binary.LittleEndian.PutUint32(manifest[14:18], 0xFFFFFF00)
	return manifest
}

func TestDetectLongPrefixMarker(t *testing.T) {
	manifest := buildManifest(t, 0, 0x1100, 0, nil, []byte("m"), nil)
	content := bytes.Repeat([]byte{'A'}, 1<<16)
	content = append(content, []byte(haltMarker)...)
	content = append(content, '\n')
	content = append(content, manifest...)

	report, err := DetectReader(bytes.NewReader(content))
	if err != nil {
		t.Fatalf("DetectReader: %v", err)
	}
	if report == nil || report.StubOffset != 1<<16 || !report.HasMetadata {
		t.Fatalf("unexpected report: %+v", report)
	}
}

func TestDetectPartialMarkerOverlap(t *testing.T) {
	// The tail of this prefix shares the opening "__" of the marker. A naive
	// one-byte sliding window loses this overlap and misses the real marker;
	// KMP backtracking must still find it.
	manifest := buildManifest(t, 0, 0x1100, 0, nil, nil, nil)
	content := bytes.Join([][]byte{
		[]byte("payload __"),
		[]byte(haltMarker),
		{'\n'},
		manifest,
	}, nil)

	wantOffset := bytes.Index(content, []byte(haltMarker))

	report, err := DetectReader(bytes.NewReader(content))
	if err != nil || report == nil {
		t.Fatalf("DetectReader: report=%+v err=%v", report, err)
	}
	if report.StubOffset != int64(wantOffset) {
		t.Fatalf("offset = %d, want %d", report.StubOffset, wantOffset)
	}

	for _, chunkSize := range []int{1, 2, 4, 21} {
		chunkReport, chunkErr := DetectReader(&chunkReader{
			data:      append([]byte(nil), content...),
			chunkSize: chunkSize,
		})
		if chunkErr != nil || chunkReport == nil ||
			chunkReport.StubOffset != int64(wantOffset) {
			t.Fatalf("chunk size %d: report=%+v err=%v", chunkSize, chunkReport, chunkErr)
		}
	}
}

func TestHasPharWrapper(t *testing.T) {
	positive := []string{
		"phar:///var/www/uploads/avatar.jpg",
		"PHAR://avatar.jpg",
		"PhAr://avatar.jpg",
		"compress.zlib://phar://avatar.jpg",
		"COMPRESS.ZLIB://PHAR://avatar.jpg",
		"php://filter/resource=phar://avatar.jpg",
		"php://filter/convert.base64-encode/resource=phar://avatar.jpg",
		"PHP://FILTER/resource=compress.zlib://phar://avatar.jpg",
		"php://filter/resource=compress.zlib://PHAR://avatar.jpg",
	}
	for _, path := range positive {
		if !HasPharWrapper(path) {
			t.Errorf("expected wrapper detection for %q", path)
		}
	}

	negative := []string{
		"/var/uploads/avatar.jpg",
		"https://example.com/path",
		"file:///tmp/avatar.jpg",
		"compress.zlib:///tmp/avatar.jpg",
		"php://filter/resource=/tmp/avatar.jpg",
		"",
	}
	for _, path := range negative {
		if HasPharWrapper(path) {
			t.Errorf("unexpected wrapper detection for %q", path)
		}
	}
}
