package sigverify

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"strconv"
)

// Checkpoint is the parsed content of a signed checkpoint note.
//
// The wire format is the five-line note envelope:
//
//	<origin>\n
//	<treeSize>\n
//	<b64(SHA-256 root)>\n
//	\n
//	\u2014 \u2014 \u2014 <keyName> <b64(signature)>\n
//
// The signed payload (the first four lines, including the blank separator) is
// verified with the trusted Rekor Ed25519 key.
type Checkpoint struct {
	Origin     string
	TreeSize   uint64
	RootHash   []byte
	KeyName    string
	Signature  []byte
	signedBody []byte
}

const signatureSeparator = "\u2014 \u2014 \u2014 "

// ParseCheckpoint decodes and structurally validates a signed checkpoint.
func ParseCheckpoint(raw []byte) (*Checkpoint, error) {
	// Split on newlines, expecting exactly five lines plus a trailing newline
	// (i.e. six split elements with the final one empty).
	parts := bytes.Split(raw, []byte("\n"))
	if len(parts) != 6 || len(parts[5]) != 0 {
		return nil, ErrCheckpoint
	}
	origin, sizeLine, rootLine, blank, sigLine := parts[0], parts[1], parts[2], parts[3], parts[4]

	if len(origin) == 0 || len(blank) != 0 {
		return nil, ErrCheckpoint
	}
	size, err := strconv.ParseUint(string(sizeLine), 10, 64)
	if err != nil || size == 0 {
		return nil, ErrCheckpoint
	}
	root, err := base64.StdEncoding.DecodeString(string(rootLine))
	if err != nil || len(root) != sha256Size {
		return nil, ErrCheckpoint
	}
	if !bytes.HasPrefix(sigLine, []byte(signatureSeparator)) {
		return nil, ErrCheckpoint
	}
	sigFields := bytes.SplitN(sigLine[len(signatureSeparator):], []byte(" "), 2)
	if len(sigFields) != 2 {
		return nil, ErrCheckpoint
	}
	signature, err := base64.StdEncoding.DecodeString(string(sigFields[1]))
	if err != nil || len(signature) != ed25519.SignatureSize {
		return nil, ErrCheckpoint
	}

	return &Checkpoint{
		Origin:    string(origin),
		TreeSize:  size,
		RootHash:  root,
		KeyName:   string(sigFields[0]),
		Signature: signature,
		// The signed payload is the four content lines including the blank
		// line and its terminating newline.
		signedBody: append(bytes.Join(parts[:4], []byte("\n")), '\n'),
	}, nil
}

// verify checks the Ed25519 signature against the trusted log key and, when
// expectedOrigin is non-empty, enforces an exact origin match.
func (c *Checkpoint) verify(logKey ed25519.PublicKey, expectedOrigin string) error {
	if len(logKey) != ed25519.PublicKeySize {
		return ErrCheckpointSignature
	}
	if !ed25519.Verify(logKey, c.signedBody, c.Signature) {
		return ErrCheckpointSignature
	}
	if expectedOrigin != "" && c.Origin != expectedOrigin {
		return ErrCheckpointOrigin
	}
	return nil
}
