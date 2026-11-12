// Package phardetect inspects uploaded file paths and contents for PHP phar
// archives before they are written to disk.
//
// PHP file operations resolve phar:// stream wrappers (including ones nested
// behind compress.zlib:// and php://filter wrappers), parse the phar manifest
// and deserialize its metadata. A polyglot file carrying a JPEG header followed
// by a phar stub can therefore trigger object injection when it is later
// referenced through such a path.
package phardetect

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"io"
)

// haltMarker is the exact stub terminator that precedes every phar manifest.
const haltMarker = `__HALT_COMPILER(); ?>`

// ErrMalformed reports that a phar stub was found but the manifest is
// truncated, overflows the content or declares an impossible length.
var ErrMalformed = errors.New("phardetect: malformed phar manifest")

// Report describes the phar archive found inside an inspected payload.
type Report struct {
	// StubOffset is the byte offset of __HALT_COMPILER(); ?> in the content.
	StubOffset int64
	// ManifestLength is the 4-byte manifest length declared by the archive.
	ManifestLength uint32
	// FileCount is the number of archived files declared by the manifest.
	FileCount uint32
	// APIVersion is the 2-byte little-endian phar API version.
	APIVersion uint16
	// Flags is the 4-byte little-endian manifest flag word.
	Flags uint32
	// Alias is the default phar alias stored in the manifest.
	Alias string
	// MetadataLength is the length of the serialized metadata blob.
	MetadataLength uint32
	// HasMetadata is true when MetadataLength is non-zero, meaning PHP would
	// attempt to unserialize attacker-controlled metadata.
	HasMetadata bool
}

// HasPharWrapper reports whether path references a phar:// stream wrapper.
// The check is ASCII case-insensitive and recognizes wrappers nested behind
// compress.zlib:// or php://filter, in any order and depth, for example:
//
//	phar://upload.jpg
//	compress.zlib://phar://upload.jpg
//	php://filter/resource=phar://upload.jpg
//	PHP://FILTER/resource=compress.zlib://PHAR://upload.jpg
func HasPharWrapper(path string) bool {
	return containsFold(path, "phar://")
}

// DetectBytes inspects an in-memory payload. It returns (nil, nil) when no
// phar stub is present. A present but unparseable manifest returns
// ErrMalformed together with whatever fields were decoded.
func DetectBytes(content []byte) (*Report, error) {
	offset := bytes.Index(content, []byte(haltMarker))
	if offset < 0 {
		return nil, nil
	}

	pos := offset + len(haltMarker)
	if pos < len(content) {
		switch content[pos] {
		case '\n':
			pos++
		case '\r':
			if pos+1 < len(content) && content[pos+1] == '\n' {
				pos += 2
			}
		}
	}

	report := &Report{StubOffset: int64(offset)}
	if err := parseManifest(bufio.NewReader(bytes.NewReader(content[pos:])), report); err != nil {
		return report, err
	}
	return report, nil
}

// DetectReader inspects a streaming payload such as an upload body. It scans
// for the stub terminator across prefixes of any length without buffering the
// whole file, then reads the manifest. The returned semantics match
// DetectBytes.
func DetectReader(r io.Reader) (*Report, error) {
	offset, reader, found, err := findMarkerStream(r)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}

	report := &Report{StubOffset: offset}
	if err := parseManifest(reader, report); err != nil {
		return report, err
	}
	return report, nil
}

// findMarkerStream scans r for haltMarker with KMP backtracking and returns a
// reader positioned at the first byte after the marker. Memory use stays
// bounded regardless of the prefix length.
func findMarkerStream(r io.Reader) (offset int64, rest *bufio.Reader, found bool, err error) {
	br := bufio.NewReaderSize(r, 4096)
	needle := []byte(haltMarker)
	failure := buildKMPFailure(needle)
	matched := 0
	var totalRead int64

	for {
		b, readErr := br.ReadByte()
		if readErr == io.EOF {
			return 0, nil, false, nil
		}
		if readErr != nil {
			return 0, nil, false, readErr
		}

		for matched > 0 && b != needle[matched] {
			matched = failure[matched-1]
		}
		if b == needle[matched] {
			matched++
		}
		totalRead++
		if matched == len(needle) {
			return totalRead - int64(len(needle)), br, true, nil
		}
	}
}

// buildKMPFailure returns the prefix-function used to resume matching after a
// partial-match mismatch without re-reading consumed bytes.
func buildKMPFailure(needle []byte) []int {
	failure := make([]int, len(needle))
	for i := 1; i < len(needle); i++ {
		j := failure[i-1]
		for j > 0 && needle[i] != needle[j] {
			j = failure[j-1]
		}
		if needle[i] == needle[j] {
			j++
		}
		failure[i] = j
	}
	return failure
}

// parseManifest decodes the little-endian phar manifest header following the
// stub. Every length field is validated against the declared manifest size and
// the available input; truncation or overflow surfaces as ErrMalformed.
func parseManifest(r *bufio.Reader, report *Report) error {
	skipLineEnding(r)

	var header [18]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return ErrMalformed
	}

	report.ManifestLength = binary.LittleEndian.Uint32(header[0:4])
	report.FileCount = binary.LittleEndian.Uint32(header[4:8])
	report.APIVersion = binary.LittleEndian.Uint16(header[8:10])
	report.Flags = binary.LittleEndian.Uint32(header[10:14])
	aliasLength := binary.LittleEndian.Uint32(header[14:18])

	// The fixed header alone occupies 18 bytes; anything smaller means the
	// manifest runs past its own declared end.
	if report.ManifestLength < 18 {
		return ErrMalformed
	}
	remaining := uint64(report.ManifestLength) - 18
	if uint64(aliasLength) > remaining {
		return ErrMalformed
	}

	alias := make([]byte, aliasLength)
	if _, err := io.ReadFull(r, alias); err != nil {
		return ErrMalformed
	}
	report.Alias = string(alias)
	remaining -= uint64(aliasLength)

	// The metadata length field needs 4 bytes and the metadata blob its own
	// declared size, both inside the manifest boundary.
	if remaining < 4 {
		return ErrMalformed
	}
	var metadataLengthBytes [4]byte
	if _, err := io.ReadFull(r, metadataLengthBytes[:]); err != nil {
		return ErrMalformed
	}
	report.MetadataLength = binary.LittleEndian.Uint32(metadataLengthBytes[:])
	remaining -= 4

	if uint64(report.MetadataLength) > remaining {
		return ErrMalformed
	}
	report.HasMetadata = report.MetadataLength != 0

	if _, err := io.CopyN(io.Discard, r, int64(report.MetadataLength)); err != nil {
		return ErrMalformed
	}

	// Remaining manifest bytes belong to per-file entries, which do not affect
	// the metadata decision but must exist for the manifest to be whole.
	if _, err := io.CopyN(io.Discard, r, int64(remaining-uint64(report.MetadataLength))); err != nil {
		return ErrMalformed
	}
	return nil
}

// skipLineEnding consumes the single optional LF or CRLF allowed right after
// the stub terminator. Any other byte remains buffered for the manifest
// parser. A missing trailing byte (stub at end of file) is tolerated.
func skipLineEnding(r *bufio.Reader) {
	peek, err := r.Peek(2)
	if err == io.EOF && len(peek) == 0 {
		return
	}
	if len(peek) > 0 && peek[0] == '\n' {
		_, _ = r.Discard(1)
		return
	}
	if len(peek) == 2 && peek[0] == '\r' && peek[1] == '\n' {
		_, _ = r.Discard(2)
	}
}

// containsFold is strings.Contains with ASCII case folding.
func containsFold(s, substr string) bool {
	if len(substr) == 0 {
		return true
	}
	for i := 0; i+len(substr) <= len(s); i++ {
		match := true
		for j := 0; j < len(substr); j++ {
			if asciiLower(s[i+j]) != asciiLower(substr[j]) {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func asciiLower(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + ('a' - 'A')
	}
	return c
}
