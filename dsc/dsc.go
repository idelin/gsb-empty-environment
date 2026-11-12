// Package dsc parses Debian .dsc (Debian Source Control) files carried in
// OpenPGP cleartext signatures and validates the trust chain that binds a
// source package together:
//
//	InRelease (repository signature) -> Sources index -> .dsc -> referenced tarballs
//
// The security properties enforced here are:
//
//   - Only fields inside the cleartext-signed block are trusted. Fields
//     injected before the armor header or after the armor footer, or injected
//     into the signature armor itself, are rejected.
//   - The cleartext signature is verified through a caller-supplied
//     [Verifier]; this package never makes trust decisions about keys itself.
//   - Dash escaping ("- " prefix, RFC 4880 section 7.1) is reversed before
//     fields are parsed, and malformed dash escaping is rejected.
//   - Files and Checksums-Sha256 / Checksums-Sha1 entries describing the same
//     path must agree on size and on every recorded digest, and every digest
//     must match the actual file on disk.
//   - Referenced paths may not contain "/" or start with ".".
//   - The SHA-256 of the raw .dsc bytes must be published for the same
//     filename in a trusted Sources index.
package dsc

import (
	"bytes"
	"context"
	"errors"
)

// Sentinel errors returned (possibly wrapped) by this package. Use errors.Is
// rather than string matching.
var (
	// ErrMalformed is returned for syntactically invalid clearsign armor,
	// cleartext headers or checksum records.
	ErrMalformed = errors.New("dsc: malformed clearsigned message or header block")

	// ErrOutsideBlock is returned when text appears before the cleartext
	// signature armor header or after the armor footer.
	ErrOutsideBlock = errors.New("dsc: content outside the clearsigned block")

	// ErrSignature is returned when the injected [Verifier] rejects the
	// cleartext signature.
	ErrSignature = errors.New("dsc: signature verification failed")

	// ErrField is returned for missing, duplicated or otherwise invalid
	// control fields (including invalid referenced file names).
	ErrField = errors.New("dsc: invalid control field")

	// ErrChecksumMismatch is returned when Files and Checksums-* records
	// disagree, or a recorded digest/size does not match an actual file.
	ErrChecksumMismatch = errors.New("dsc: checksum or size mismatch")

	// ErrSources is returned when the .dsc digest is not published for the
	// .dsc filename in the Sources index.
	ErrSources = errors.New("dsc: dsc digest not found in Sources index")
)

// Verifier verifies an OpenPGP cleartext-signed message. The message passed
// in is the complete armor block exactly as it appeared in the input, from
// the "-----BEGIN PGP SIGNED MESSAGE-----" line through the terminating
// newline of the "-----END PGP SIGNATURE-----" line.
//
// Implementations are responsible for keyring/trust decisions and for
// canonicalization (for example RFC 4880 dash-escaping) while verifying the
// signature over the cleartext.
type Verifier interface {
	VerifyClearsigned(ctx context.Context, message []byte) error
}

// VerifierFunc adapts a plain function to the [Verifier] interface.
type VerifierFunc func(ctx context.Context, message []byte) error

// VerifyClearsigned calls f(ctx, message).
func (f VerifierFunc) VerifyClearsigned(ctx context.Context, message []byte) error {
	return f(ctx, message)
}

// FileEntry is one referenced source file, merged from the Files and
// Checksums-* fields of a .dsc.
type FileEntry struct {
	// Name is the basename of the referenced file (never containing "/" and
	// never starting with ".").
	Name string

	// Size is the recorded file size in bytes; Files and Checksums-* records
	// must agree on it.
	Size int64

	// MD5 holds the digest from the Files field when present.
	MD5 []byte

	// SHA1 holds the digest from Checksums-Sha1 when present.
	SHA1 []byte

	// SHA256 holds the digest from Checksums-Sha256. It is always populated
	// for a successfully parsed [DSC].
	SHA256 []byte
}

// Field is one control field from the signed cleartext. Values keep folded
// continuation lines unfolded into separate newline-terminated logical lines.
type Field struct {
	Name  string
	Value string
}

// DSC is a parsed and structurally validated .dsc file. It is only produced
// after the injected [Verifier] has accepted the clearsign signature.
type DSC struct {
	// Fields holds every signed control field in document order.
	Fields []Field

	// Files holds the referenced files in the order of the Files field.
	Files []FileEntry

	// raw is the complete original .dsc input.
	raw []byte

	// signed is the dash-unescaped cleartext (the exact content that was
	// signed, with a trailing newline).
	signed []byte

	// armor is the complete clearsign armor block.
	armor []byte
}

// Raw returns the complete original .dsc bytes.
func (d *DSC) Raw() []byte { return d.raw }

// SignedContent returns the dash-unescaped cleartext that the signature
// covers, including its trailing newline.
func (d *DSC) SignedContent() []byte { return d.signed }

// ArmorBlock returns the exact OpenPGP clearsign armor block that was handed
// to the [Verifier].
func (d *DSC) ArmorBlock() []byte { return d.armor }

// FieldValues returns the unfolded values recorded under name (field names
// are case-insensitive, as in RFC 5322). Most fields appear at most once.
func (d *DSC) FieldValues(name string) []string {
	var out []string
	for _, f := range d.Fields {
		if equalFoldASCII(f.Name, name) {
			out = append(out, f.Value)
		}
	}
	return out
}

// Value returns the unfolded value of the first field with the given
// case-insensitive name, or "" if absent.
func (d *DSC) Value(name string) string {
	for _, f := range d.Fields {
		if equalFoldASCII(f.Name, name) {
			return f.Value
		}
	}
	return ""
}

const (
	beginMarker  = "-----BEGIN PGP SIGNED MESSAGE-----"
	sigBegin     = "-----BEGIN PGP SIGNATURE-----"
	sigEnd       = "-----END PGP SIGNATURE-----"
	sha256HexLen = 64
	sha1HexLen   = 40
	md5HexLen    = 32
)

// Parse splits the clearsign armor, verifies it with v, and parses the signed
// control fields and checksum records. It does not touch the filesystem.
//
// Any byte outside the single clearsign armor block (other than a single
// trailing newline at end of input) causes an [ErrOutsideBlock] error.
func Parse(ctx context.Context, data []byte, v Verifier) (*DSC, error) {
	if v == nil {
		return nil, errors.New("dsc: nil verifier")
	}

	armor, signed, err := splitClearsigned(data)
	if err != nil {
		return nil, err
	}

	if err := v.VerifyClearsigned(ctx, armor); err != nil {
		return nil, errors.Join(ErrSignature, err)
	}

	fields, err := parseParagraph(signed)
	if err != nil {
		return nil, err
	}

	d := &DSC{
		Fields: fields,
		raw:    append([]byte(nil), data...),
		signed: append([]byte(nil), signed...),
		armor:  append([]byte(nil), armor...),
	}
	if err := d.buildFiles(); err != nil {
		return nil, err
	}
	return d, nil
}

// splitClearsigned enforces the structure of an OpenPGP cleartext-signed
// message (RFC 4880 section 7) and returns the exact armor block and the
// dash-unescaped cleartext (with its trailing newline).
func splitClearsigned(data []byte) (armor []byte, cleartext []byte, err error) {
	if bytes.ContainsRune(data, '\r') {
		return nil, nil, errors.Join(ErrMalformed, errors.New("CRLF input is not accepted; normalize to LF first"))
	}

	beginLine := []byte(beginMarker + "\n")
	if !bytes.HasPrefix(data, beginLine) {
		if bytes.Contains(data, beginLine) {
			return nil, nil, errors.Join(ErrOutsideBlock, errors.New("text before armor header"))
		}
		return nil, nil, errors.Join(ErrMalformed, errors.New("missing armor header"))
	}

	endLine := []byte(sigEnd + "\n")
	endIdx := bytes.Index(data, endLine)
	if endIdx < 0 {
		return nil, nil, errors.Join(ErrMalformed, errors.New("missing armor footer"))
	}
	blockEnd := endIdx + len(endLine)
	switch tail := data[blockEnd:]; {
	case len(tail) == 0:
	case len(tail) == 1 && tail[0] == '\n':
	default:
		return nil, nil, errors.Join(ErrOutsideBlock, errors.New("text after armor footer"))
	}

	blockLines := bytes.Split(data[:blockEnd], []byte("\n"))
	// blockLines ends in "" because the block ends with "\n".
	if len(blockLines) < 2 || len(blockLines[len(blockLines)-1]) != 0 {
		return nil, nil, errors.Join(ErrMalformed, errors.New("truncated armor block"))
	}
	blockLines = blockLines[:len(blockLines)-1]

	// Line 0: armor header. Lines 1..h: optional armor headers, terminated by
	// an empty line.
	i := 1
	sawHeaderEnd := false
	for ; i < len(blockLines); i++ {
		line := string(blockLines[i])
		if line == "" {
			sawHeaderEnd = true
			i++
			break
		}
		if !armorHeaderLine(line) {
			return nil, nil, errors.Join(ErrMalformed, errors.New("invalid armor header line"))
		}
	}
	if !sawHeaderEnd {
		return nil, nil, errors.Join(ErrMalformed, errors.New("armor headers not terminated"))
	}

	// Cleartext: up to (but not including) the signature armor header line.
	sigIdx := -1
	for j := i; j < len(blockLines); j++ {
		if string(blockLines[j]) == sigBegin {
			sigIdx = j
			break
		}
	}
	if sigIdx < 0 {
		return nil, nil, errors.Join(ErrMalformed, errors.New("missing signature header"))
	}

	clearLines := make([][]byte, 0, sigIdx-i+1)
	for j := i; j < sigIdx; j++ {
		unescaped, perr := unescapeDash(blockLines[j])
		if perr != nil {
			return nil, nil, perr
		}
		clearLines = append(clearLines, unescaped)
	}

	// The cleartext of a cleartext signature always ends with a newline: the
	// empty line directly before the signature header.
	if len(clearLines) == 0 || len(clearLines[len(clearLines)-1]) != 0 {
		return nil, nil, errors.Join(ErrMalformed, errors.New("cleartext not terminated before signature"))
	}
	cleartext = bytes.Join(clearLines, []byte("\n"))

	// Signature armor: header line, optional headers, empty line, base64
	// body, footer line.
	k := sigIdx + 1
	sawSigHeaderEnd := false
	for ; k < len(blockLines); k++ {
		line := string(blockLines[k])
		if line == "" {
			sawSigHeaderEnd = true
			k++
			break
		}
		if !armorHeaderLine(line) {
			return nil, nil, errors.Join(ErrMalformed, errors.New("invalid signature armor header"))
		}
	}
	if !sawSigHeaderEnd {
		return nil, nil, errors.Join(ErrMalformed, errors.New("signature headers not terminated"))
	}

	sawBody := false
	for ; k < len(blockLines); k++ {
		line := string(blockLines[k])
		if line == sigEnd {
			if k != len(blockLines)-1 {
				return nil, nil, errors.Join(ErrMalformed, errors.New("trailing signature lines"))
			}
			if !sawBody {
				return nil, nil, errors.Join(ErrMalformed, errors.New("empty signature body"))
			}
			return data[:blockEnd], cleartext, nil
		}
		if !armorBodyLine(line) {
			return nil, nil, errors.Join(ErrMalformed, errors.New("invalid base64 signature body"))
		}
		sawBody = true
	}
	return nil, nil, errors.Join(ErrMalformed, errors.New("missing armor footer line"))
}

// unescapeDash reverses RFC 4880 section 7.1 dash escaping. A line beginning
// with "- " loses that prefix; any other line beginning with "-" is malformed
// because a valid signer would have escaped it.
func unescapeDash(line []byte) ([]byte, error) {
	if len(line) > 0 && line[0] == '-' {
		if len(line) < 2 || line[1] != ' ' {
			return nil, errors.Join(ErrMalformed, errors.New("invalid dash escaping in cleartext"))
		}
		return append([]byte(nil), line[2:]...), nil
	}
	return append([]byte(nil), line...), nil
}

func equalFoldASCII(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}
