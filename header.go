package netsec

import (
	"bufio"
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"io"
	"strings"
)

// Version is the magic line that opens every age v1 header.
const Version = "age-encryption.org/v1"

// FileKeySize is the length of the age file key in bytes.
const FileKeySize = 16

// macTagHexLen is the length of the hex-encoded HMAC-SHA256 tag.
const macTagHexLen = 64

var (
	// ErrHeaderNotAuthenticated is returned when the payload reader is asked
	// for bytes before the header HMAC has been verified. The reader returns no
	// plaintext until authentication has succeeded.
	ErrHeaderNotAuthenticated = errors.New("netsec: header has not been authenticated")

	errBadVersion   = errors.New("netsec: invalid age version line")
	errBadMACLine   = errors.New("netsec: malformed header MAC line")
	errBadMAC       = errors.New("netsec: header MAC verification failed")
	errNoRecipients = errors.New("netsec: header contains no recipient stanzas")
)

// Header is a parsed age header.
//
// raw holds the exact bytes covered by the header HMAC, i.e. every byte from
// the version line through the "---" prefix of the MAC line. Keeping the
// original bytes means unknown stanza types are preserved verbatim and always
// participate in authentication, even if this package does not understand
// them.
type Header struct {
	Stanzas []*Stanza

	// MAC is the observed MAC tag from the "--- hmac" line, hex encoded.
	MAC string

	// raw is the authenticated prefix, ending right after "---".
	raw []byte

	// payloadKey caches the STREAM key derived after authentication.
	payloadKey []byte

	// authenticated flips to true only after VerifyMAC succeeds.
	authenticated bool
}

// ParseHeader reads and parses an age header from r. It stops immediately
// after the MAC line; all payload bytes remain untouched in r for later
// reading through the returned PayloadReader.
//
// Parsing never verifies authenticity: callers must recover the file key with
// an Identity and call VerifyMAC before reading any payload byte.
func ParseHeader(r *bufio.Reader) (*Header, *PayloadReader, error) {
	h := &Header{}

	versionLine, err := readHeaderLine(r)
	if err != nil {
		return nil, nil, err
	}
	if versionLine != Version {
		return nil, nil, errBadVersion
	}
	h.raw = append(h.raw, Version...)
	h.raw = append(h.raw, '\n')

	var macLine string
	for {
		line, err := readHeaderLine(r)
		if err != nil {
			return nil, nil, err
		}
		if line == "---" || strings.HasPrefix(line, "--- ") {
			macLine = line
			break
		}

		// Parse the logical stanza while preserving its exact on-wire bytes
		// for MAC coverage. Any stanza type, including unknown ones, is kept.
		stanza, rawStanza, err := ReadStanza(r, line)
		if err != nil {
			return nil, nil, err
		}
		h.raw = append(h.raw, rawStanza...)
		h.Stanzas = append(h.Stanzas, stanza)
	}

	if len(h.Stanzas) == 0 {
		return nil, nil, errNoRecipients
	}

	// The MAC line is "--- hmac <tag>"; only a single such line may exist.
	macFields := strings.Fields(macLine)
	if len(macFields) != 3 || macFields[0] != "---" || macFields[1] != "hmac" {
		return nil, nil, errBadMACLine
	}
	tag := macFields[2]
	if len(tag) != macTagHexLen {
		return nil, nil, errBadMACLine
	}
	for _, c := range tag {
		if !('0' <= c && c <= '9') && !('a' <= c && c <= 'f') {
			return nil, nil, errBadMACLine
		}
	}
	h.MAC = tag

	// Everything up to and including "---" is covered by the HMAC.
	h.raw = append(h.raw, "---"...)

	pr := &PayloadReader{
		header: h,
		source: r,
	}
	return h, pr, nil
}

// Marshal serializes the header back to its canonical wire encoding,
// including the MAC line. It is used by the encryption path and in tests.
func (h *Header) Marshal(fileKey []byte) ([]byte, error) {
	if len(fileKey) != FileKeySize {
		return nil, errors.New("netsec: invalid file key length")
	}
	var b bytes.Buffer
	b.WriteString(Version)
	b.WriteByte('\n')
	for _, s := range h.Stanzas {
		b.Write(MarshalStanza(s))
	}
	b.WriteString("--- hmac ")
	b.WriteString(h.ComputeMAC(fileKey))
	b.WriteByte('\n')
	return b.Bytes(), nil
}

// ComputeMAC returns the hex-encoded HMAC-SHA256 over every header byte up to
// and including the "---" prefix, keyed with HMAC-SHA256("", fileKey) as
// specified by age v1.
func (h *Header) ComputeMAC(fileKey []byte) string {
	key := hmacSHA256(nil, fileKey)
	mac := hmacSHA256(key, h.raw)
	return hexEncode(mac)
}

// VerifyMAC checks the header HMAC against the given file key using constant
// time comparison. On success the payload reader is unlocked. A failed check
// leaves every payload reader for this header sealed.
func (h *Header) VerifyMAC(fileKey []byte) error {
	if len(fileKey) != FileKeySize {
		return errBadMAC
	}
	expected := h.ComputeMAC(fileKey)
	if !hmac.Equal([]byte(expected), []byte(h.MAC)) {
		return errBadMAC
	}
	payloadKey, err := hkdfKey(sha256.New, fileKey, nil, "payload", streamKeySize)
	if err != nil {
		return err
	}
	h.payloadKey = payloadKey
	h.authenticated = true
	return nil
}

// Authenticated reports whether VerifyMAC has succeeded.
func (h *Header) Authenticated() bool {
	return h.authenticated
}

// PayloadReader is the stream of encrypted payload bytes. It implements
// io.Reader but refuses to return any byte until the header HMAC has been
// verified, so a tampered header can never cause plaintext to be exposed.
type PayloadReader struct {
	header *Header
	source io.Reader
	stream *streamReader
}

// Read returns ErrHeaderNotAuthenticated and zero bytes while the header is
// unauthenticated. After successful verification it decrypts the STREAM
// payload.
func (p *PayloadReader) Read(plaintext []byte) (int, error) {
	if !p.header.authenticated {
		return 0, ErrHeaderNotAuthenticated
	}
	if p.stream == nil {
		p.stream = newStreamReader(p.source, p.header.payloadKey)
	}
	return p.stream.Read(plaintext)
}

// Sealed reports whether the reader is still blocked by pending header
// authentication.
func (p *PayloadReader) Sealed() bool {
	return !p.header.authenticated
}

func hmacSHA256(key, message []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(message)
	return m.Sum(nil)
}

func hexEncode(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[2*i] = digits[v>>4]
		out[2*i+1] = digits[v&0x0f]
	}
	return string(out)
}
