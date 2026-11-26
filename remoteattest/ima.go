package remoteattest

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	// TemplateIMANG is the "ima-ng" IMA template name.
	TemplateIMANG = "ima-ng"
	// TemplateIMASIG is the "ima-sig" IMA template name.
	TemplateIMASIG = "ima-sig"

	imaTemplateHashSize = sha1.Size // 20-byte claimed template hash per entry
	sha256DigestName    = "sha256"

	// maxTemplateData bounds a single length-prefixed template field so a
	// corrupt length cannot exhaust memory.
	maxTemplateData = 1 << 20
)

var (
	// ErrMalformedIMALog is returned when the IMA event stream is truncated
	// or uses an unsupported layout/template.
	ErrMalformedIMALog = errors.New("remoteattest: malformed IMA measurement log")
	// ErrTemplateHashMismatch is returned when the 20-byte template hash
	// claimed by an entry differs from the hash recomputed from its fields.
	ErrTemplateHashMismatch = errors.New("remoteattest: claimed IMA template hash does not match recomputed value")
	// ErrUnsupportedIMATemplate is returned for an unknown template name.
	ErrUnsupportedIMATemplate = errors.New("remoteattest: unsupported IMA template")
	// ErrUnsupportedDigestAlg is returned when a d-ng digest is not SHA-256.
	ErrUnsupportedDigestAlg = errors.New("remoteattest: unsupported IMA file digest algorithm")
	// ErrPCRIndexOutOfRange is returned for an out-of-range PCR index.
	ErrPCRIndexOutOfRange = errors.New("remoteattest: IMA PCR index out of range")
	// ErrPCRBankIncomplete is returned when a quote selects a PCR that the
	// verifier cannot reconstruct instead of silently treating it as zero.
	ErrPCRBankIncomplete = errors.New("remoteattest: selected PCR missing from reconstructed bank")
)

// IMAEntry is one fully decoded IMA binary measurement entry.
type IMAEntry struct {
	// PCRIndex is the 4-byte little-endian target PCR (IMA uses 10).
	PCRIndex uint32
	// TemplateHash is the 20-byte SHA-1 template hash claimed in the log.
	TemplateHash []byte
	// TemplateName is "ima-ng" or "ima-sig".
	TemplateName string
	// FileDigestAlg is the d-ng algorithm name ("sha256").
	FileDigestAlg string
	// FileDigest is the measured file digest bytes carried by d-ng.
	FileDigest []byte
	// Path is the measured file path (without the trailing NUL byte).
	Path string
	// Signature is the ima-sig signature field payload (nil for ima-ng).
	Signature []byte

	// RecomputedHash is SHA-1 over the real template fields.
	RecomputedHash []byte
	// ZeroHash marks a ToMToU/open-writers violation: the entry presents an
	// all-zero template hash or an all-zero file digest.
	ZeroHash bool
}

// ParseIMALog decodes the full concatenated IMA binary event stream.
//
// Every entry is laid out as:
//
//	pcr            4 bytes little-endian
//	templateHash   20 bytes (SHA-1)
//	templateName   NUL-terminated ASCII ("ima-ng"/"ima-sig")
//	templateData   variable length fields:
//	  d-ng:        u16 BE length, algName + NUL, raw digest
//	  sig (only):  u16 BE length, raw signature
//	  path:        NUL-terminated
func ParseIMALog(log []byte) ([]*IMAEntry, error) {
	var entries []*IMAEntry
	pos := 0
	for pos < len(log) {
		if len(log)-pos < 4+imaTemplateHashSize {
			return nil, fmt.Errorf("%w: truncated entry header at offset %d", ErrMalformedIMALog, pos)
		}
		pcr := binary.LittleEndian.Uint32(log[pos : pos+4])
		pos += 4
		templateHash := append([]byte(nil), log[pos:pos+imaTemplateHashSize]...)
		pos += imaTemplateHashSize

		name, next, err := readNULString(log, pos)
		if err != nil {
			return nil, fmt.Errorf("%w: template name: %v", ErrMalformedIMALog, err)
		}
		pos = next

		entry := &IMAEntry{
			PCRIndex:     pcr,
			TemplateHash: templateHash,
			TemplateName: name,
		}

		switch name {
		case TemplateIMANG, TemplateIMASIG:
		default:
			return nil, fmt.Errorf("%w: %q", ErrUnsupportedIMATemplate, name)
		}

		algName, digest, after, err := parseDigestField(log, pos)
		if err != nil {
			return nil, err
		}
		entry.FileDigestAlg = algName
		entry.FileDigest = digest
		pos = after

		if name == TemplateIMASIG {
			sig, afterSig, err := readLengthPrefixed(log, pos)
			if err != nil {
				return nil, fmt.Errorf("%w: signature field: %v", ErrMalformedIMALog, err)
			}
			entry.Signature = sig
			pos = afterSig
		}

		path, afterPath, err := readNULString(log, pos)
		if err != nil {
			return nil, fmt.Errorf("%w: path field: %v", ErrMalformedIMALog, err)
		}
		entry.Path = path
		pos = afterPath

		entry.RecomputedHash = recomputeTemplateHash(entry)
		entry.ZeroHash = isAllZero(entry.TemplateHash) || isAllZero(entry.FileDigest)

		entries = append(entries, entry)
	}
	return entries, nil
}

// parseDigestField reads the d-ng field: u16 BE size, "sha256\0", digest.
func parseDigestField(log []byte, pos int) (string, []byte, int, error) {
	field, next, err := readLengthPrefixed(log, pos)
	if err != nil {
		return "", nil, 0, fmt.Errorf("%w: d-ng field: %v", ErrMalformedIMALog, err)
	}
	alg, algEnd, err := readNULString(field, 0)
	if err != nil {
		return "", nil, 0, fmt.Errorf("%w: d-ng algorithm: %v", ErrMalformedIMALog, err)
	}
	if alg != sha256DigestName {
		return "", nil, 0, fmt.Errorf("%w: %q", ErrUnsupportedDigestAlg, alg)
	}
	digest := field[algEnd:]
	if len(digest) != sha256.Size {
		return "", nil, 0, fmt.Errorf("%w: d-ng digest is %d bytes, want %d",
			ErrMalformedIMALog, len(digest), sha256.Size)
	}
	return alg, append([]byte(nil), digest...), next, nil
}

func readNULString(buf []byte, pos int) (string, int, error) {
	for i := pos; i < len(buf); i++ {
		if buf[i] == 0 {
			return string(buf[pos:i]), i + 1, nil
		}
	}
	return "", 0, errors.New("unterminated NUL string")
}

func readLengthPrefixed(buf []byte, pos int) ([]byte, int, error) {
	if len(buf)-pos < 2 {
		return nil, 0, errors.New("missing 2-byte length prefix")
	}
	n := int(binary.BigEndian.Uint16(buf[pos : pos+2]))
	pos += 2
	if n > maxTemplateData || pos+n > len(buf) {
		return nil, 0, fmt.Errorf("length %d exceeds available %d bytes", n, len(buf)-pos)
	}
	return append([]byte(nil), buf[pos:pos+n]...), pos + n, nil
}

// templateFieldBytes returns the exact bytes hashed into the template digest,
// following the kernel rule: the d-ng digest field is skipped, then the
// remaining fields are concatenated in order. For ima-ng this is the
// NUL-terminated path; for ima-sig it is the raw signature followed by the
// NUL-terminated path.
func templateFieldBytes(entry *IMAEntry) []byte {
	pathWithNUL := append([]byte(entry.Path), 0)
	if entry.TemplateName == TemplateIMASIG {
		out := make([]byte, 0, len(entry.Signature)+len(pathWithNUL))
		out = append(out, entry.Signature...)
		out = append(out, pathWithNUL...)
		return out
	}
	return pathWithNUL
}

// recomputeTemplateHash recomputes the 20-byte SHA-1 template hash from the
// real template fields (kernel ima_ng_digest / ima_sig_digest ordering).
func recomputeTemplateHash(entry *IMAEntry) []byte {
	sum := sha1.Sum(templateFieldBytes(entry))
	return sum[:]
}

// entryDigest returns the 32-byte value extended into PCR 10 for an entry.
// Normal entries extend SHA-256 over the same template bytes whose SHA-1 is
// the template hash; ToMToU/open-writers violations extend 0xFF..FF.
func entryDigest(entry *IMAEntry) []byte {
	if entry.ZeroHash {
		return bytesFF(sha256.Size)
	}
	sum := sha256.Sum256(templateFieldBytes(entry))
	return sum[:]
}

func isAllZero(b []byte) bool {
	for _, x := range b {
		if x != 0 {
			return false
		}
	}
	return len(b) > 0
}

func bytesFF(n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = 0xFF
	}
	return out
}

// ReplayResult holds the outcome of replaying the full IMA log.
type ReplayResult struct {
	// PCRs maps every touched PCR index to its final 32-byte value.
	PCRs map[uint32][]byte
	// Violations lists the zero-hash (ToMToU) entries in log order.
	Violations []*IMAEntry
	// Entries is the fully decoded, hash-verified event stream.
	Entries []*IMAEntry
}

// ReplayIMALog parses the stream, rejects entries whose claimed SHA-1 template
// hash differs from the recomputed value, then extends every entry from an
// all-zero initial bank using PCR_new = SHA256(PCR_old || entryDigest).
// Zero-hash violation entries are extended with 0xFF..FF and recorded.
func ReplayIMALog(log []byte) (*ReplayResult, error) {
	entries, err := ParseIMALog(log)
	if err != nil {
		return nil, err
	}

	result := &ReplayResult{
		PCRs: make(map[uint32][]byte),
	}

	for idx, entry := range entries {
		if entry.PCRIndex >= 24 {
			return nil, fmt.Errorf("%w: entry %d PCR %d", ErrPCRIndexOutOfRange, idx, entry.PCRIndex)
		}
		if !entry.ZeroHash && !equalBytes(entry.TemplateHash, entry.RecomputedHash) {
			return nil, fmt.Errorf("%w: entry %d path %q", ErrTemplateHashMismatch, idx, entry.Path)
		}

		pcr, ok := result.PCRs[entry.PCRIndex]
		if !ok {
			pcr = make([]byte, sha256.Size)
		}
		extended := make([]byte, 0, sha256.Size*2)
		extended = append(extended, pcr...)
		extended = append(extended, entryDigest(entry)...)
		sum := sha256.Sum256(extended)
		result.PCRs[entry.PCRIndex] = sum[:]

		if entry.ZeroHash {
			result.Violations = append(result.Violations, entry)
		}
	}
	result.Entries = entries
	return result, nil
}

// PCRDigest concatenates the selected PCR values in ascending index order and
// returns their SHA-256, matching the TPM pcrDigest construction. Every
// selected index must be present in the reconstructed bank; an unknown PCR is
// an error rather than an implicit all-zero value, so a bitmap-shifting
// attacker cannot get a free "zero PCR" match.
func PCRDigest(pcrs map[uint32][]byte, selection PCRSelection) ([]byte, error) {
	var concat []byte
	for _, idx := range selection.Indices() {
		v, ok := pcrs[idx]
		if !ok {
			return nil, fmt.Errorf("%w: PCR %d", ErrPCRBankIncomplete, idx)
		}
		concat = append(concat, v...)
	}
	sum := sha256.Sum256(concat)
	return sum[:], nil
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
