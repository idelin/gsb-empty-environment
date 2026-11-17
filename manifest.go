package netsec

import (
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"strconv"
	"unicode/utf8"
)

var (
	ErrBadSignature         = errors.New("netsec: manifest signature is invalid")
	ErrManifestSyntax       = errors.New("netsec: manifest syntax error")
	ErrManifestNotCanonical = errors.New("netsec: manifest path is not canonical")
	ErrManifestOrder        = errors.New("netsec: manifest paths not in strict byte order")
)

// manifestEntry is one record of a parsed manifest.
type manifestEntry struct {
	path   string
	digest [sha256.Size]byte
}

// verifyManifestSignature checks the Ed25519 signature over the raw manifest
// bytes against the trusted public key.
func verifyManifestSignature(key ed25519.PublicKey, manifest, signature []byte) error {
	if len(key) != ed25519.PublicKeySize {
		return ErrBadSignature
	}
	if !ed25519.Verify(key, manifest, signature) {
		return ErrBadSignature
	}
	return nil
}

// parseManifest decodes a manifest. The encoding is the concatenation, in
// strictly increasing path byte order, of records of the form:
//
//	<decimal UTF-8 byte length of path> ":" <path bytes> <32-byte digest>
//
// Paths must already be in canonical form and must be unique (enforced by the
// strict ordering check).
func parseManifest(data []byte) ([]manifestEntry, error) {
	var out []manifestEntry
	i := 0
	for i < len(data) {
		j := i
		for j < len(data) && data[j] >= '0' && data[j] <= '9' {
			j++
		}
		if j == i || j >= len(data) || data[j] != ':' {
			return nil, ErrManifestSyntax
		}
		num := string(data[i:j])
		if len(num) > 1 && num[0] == '0' {
			return nil, ErrManifestSyntax // no leading zeros
		}
		n, err := strconv.Atoi(num)
		if err != nil || n <= 0 {
			return nil, ErrManifestSyntax
		}
		i = j + 1
		if len(data)-i < n+sha256.Size {
			return nil, ErrManifestSyntax
		}
		pathBytes := data[i : i+n]
		i += n
		var digest [sha256.Size]byte
		copy(digest[:], data[i:i+sha256.Size])
		i += sha256.Size

		if !utf8.Valid(pathBytes) {
			return nil, ErrManifestSyntax
		}
		path := string(pathBytes)
		canon, err := canonicalizePath(path)
		if err != nil || canon != path {
			return nil, ErrManifestNotCanonical
		}
		if len(out) > 0 && out[len(out)-1].path >= path {
			return nil, ErrManifestOrder // duplicates and misordering rejected
		}
		out = append(out, manifestEntry{path: path, digest: digest})
	}
	return out, nil
}
