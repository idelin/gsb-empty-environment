package netsec

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
)

// emDash marks the start of a signature line. Note text never begins a line
// with the UTF-8 encoding of U+2014 followed by a space.
const emDash = "— "

// MaxNoteSize bounds the amount of data Open is willing to parse.
const MaxNoteSize = 1 << 20

// A Verifier verifies signed notes and identifies a public key.
type Verifier interface {
	Name() string
	KeyHash() uint32
	Verify(msg, sig []byte) bool
}

// A Signer signs notes with a private key.
type Signer interface {
	Name() string
	KeyHash() uint32
	Sign(msg []byte) ([]byte, error)
}

// Verifiers is the trusted-key set used to open a note.
type Verifiers interface {
	Verifiers() ([]Verifier, int)
}

// A Note is a parsed signed note.
type Note struct {
	Text       []byte // canonical note body, always ending in "\n"
	Signatures []Signature
}

// A Signature is a single parsed signature line.
type Signature struct {
	Name   string
	Hash   uint32
	Base64 string // encoded hash || algorithm identifier || signature
}

// Open parses and verifies a signed note against the trusted verifiers.
//
// Unknown signers are ignored. Each trusted verifier that produced a valid
// signature is counted once, and the number of distinct trusted signers must
// reach the configured threshold. The returned Note retains every valid
// trusted signature.
func Open(verifiers Verifiers, note []byte) (*Note, error) {
	if len(note) > MaxNoteSize {
		return nil, fmt.Errorf("note too large: %d bytes", len(note))
	}

	body, sigs, err := parse(note)
	if err != nil {
		return nil, err
	}

	trusted, threshold := verifiers.Verifiers()
	if threshold < 1 {
		return nil, errors.New("netsec: threshold must be at least 1")
	}
	if len(trusted) < threshold {
		return nil, errors.New("netsec: not enough trusted verifiers for threshold")
	}

	var accepted []Signature
	matched := make(map[int]bool)

	for _, sig := range sigs {
		raw, err := base64.StdEncoding.DecodeString(sig.Base64)
		if err != nil {
			return nil, errors.New("netsec: malformed signature: base64 decode failed")
		}
		if len(raw) < 5 {
			return nil, errors.New("netsec: malformed signature: too short")
		}

		index := -1
		for i, v := range trusted {
			if v.Name() == sig.Name && v.KeyHash() == sig.Hash {
				index = i
				break
			}
		}
		if index < 0 {
			// Signed by an untrusted key: harmless but ignored.
			continue
		}
		if matched[index] {
			// The same trusted key already provided a valid signature.
			continue
		}
		if !trusted[index].Verify(body, raw[4:]) {
			return nil, fmt.Errorf("netsec: invalid signature from %q", sig.Name)
		}
		matched[index] = true
		accepted = append(accepted, sig)
	}

	if len(matched) < threshold {
		return nil, fmt.Errorf("netsec: verified %d signatures, need %d", len(matched), threshold)
	}

	return &Note{Text: body, Signatures: accepted}, nil
}

// parse splits a note into its canonical body and parsed signature lines.
func parse(note []byte) (body []byte, sigs []Signature, err error) {
	sep := bytes.Index(note, []byte("\n"+emDash))
	if sep < 0 {
		return nil, nil, errors.New("netsec: malformed note: no signature separator")
	}

	// sep points at the blank separator's newline; the signed body is the
	// text before it and already ends in its own newline.
	body = note[:sep]
	if !bytes.HasSuffix(body, []byte("\n")) {
		return nil, nil, errors.New("netsec: malformed note: body must end in newline")
	}

	rest := note[sep+1:]
	for len(rest) > 0 {
		line := rest
		if idx := bytes.IndexByte(rest, '\n'); idx >= 0 {
			line, rest = rest[:idx], rest[idx+1:]
		} else {
			rest = nil
		}

		if !bytes.HasPrefix(line, []byte(emDash)) {
			return nil, nil, errors.New("netsec: malformed note: signature line missing em dash")
		}
		fields := bytes.SplitN(line[len(emDash):], []byte(" "), 2)
		if len(fields) != 2 || len(fields[0]) == 0 || len(fields[1]) == 0 {
			return nil, nil, errors.New("netsec: malformed note: bad signature line")
		}

		name := string(fields[0])
		if !validName(name) {
			return nil, nil, fmt.Errorf("netsec: malformed note: invalid verifier name %q", name)
		}

		encoded := string(fields[1])
		raw, derr := base64.StdEncoding.DecodeString(encoded)
		if derr != nil || len(raw) < 5 {
			return nil, nil, errors.New("netsec: malformed note: undecodable signature")
		}

		sigs = append(sigs, Signature{
			Name:   name,
			Hash:   uint32(raw[0])<<24 | uint32(raw[1])<<16 | uint32(raw[2])<<8 | uint32(raw[3]),
			Base64: encoded,
		})
	}

	return body, sigs, nil
}

// Sign assembles a signed note from a body and one or more signers.
//
// The body is canonicalized to end in a newline. Every supplied signer must
// have a distinct key hash.
func Sign(signers []Signer, text []byte) ([]byte, error) {
	if len(signers) == 0 {
		return nil, errors.New("netsec: missing signers")
	}
	if !bytes.HasSuffix(text, []byte("\n")) {
		text = append(append([]byte(nil), text...), '\n')
	}

	var note bytes.Buffer
	note.Write(text)
	note.WriteByte('\n')

	seen := make(map[uint32]bool)
	for _, signer := range signers {
		if !validName(signer.Name()) {
			return nil, fmt.Errorf("netsec: invalid signer name %q", signer.Name())
		}
		if seen[signer.KeyHash()] {
			return nil, fmt.Errorf("netsec: duplicate signer %q", signer.Name())
		}
		seen[signer.KeyHash()] = true

		sig, err := signer.Sign(text)
		if err != nil {
			return nil, err
		}

		hash := make([]byte, 4)
		hashUint := signer.KeyHash()
		hash[0] = byte(hashUint >> 24)
		hash[1] = byte(hashUint >> 16)
		hash[2] = byte(hashUint >> 8)
		hash[3] = byte(hashUint)

		encoded := base64.StdEncoding.EncodeToString(append(hash, sig...))
		note.WriteString(emDash)
		note.WriteString(signer.Name())
		note.WriteByte(' ')
		note.WriteString(encoded)
		note.WriteByte('\n')
	}

	return note.Bytes(), nil
}

// keyHash computes SHA-256(name || "\n" || 0x01 || key) and returns the first
// four bytes interpreted as a big-endian uint32.
func keyHash(name string, key []byte) uint32 {
	h := sha256.New()
	h.Write([]byte(name))
	h.Write([]byte("\n\x01"))
	h.Write(key)
	sum := h.Sum(nil)
	return uint32(sum[0])<<24 | uint32(sum[1])<<16 | uint32(sum[2])<<8 | uint32(sum[3])
}

// validName reports whether name is a legal verifier name: non-empty, at most
// 1024 bytes, and free of control characters and spaces.
func validName(name string) bool {
	if len(name) == 0 || len(name) > 1024 {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c <= ' ' || c == 0x7f {
			return false
		}
	}
	return true
}

// LeafHash is the transparency-log leaf hash of a canonical note body:
// SHA-256(0x00 || body).
func LeafHash(body []byte) [sha256.Size]byte {
	h := sha256.New()
	h.Write([]byte{0x00})
	h.Write(body)
	var sum [sha256.Size]byte
	copy(sum[:], h.Sum(nil))
	return sum
}

// LeafHash returns the leaf hash of the note's canonical body.
func (n *Note) LeafHash() [sha256.Size]byte {
	return LeafHash(n.Text)
}

// Message returns the exact bytes covered by the note's signatures.
func (n *Note) Message() []byte {
	return n.Text
}
