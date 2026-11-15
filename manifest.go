package netsec

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"fmt"
	"sort"
	"strconv"
)

// ManifestEntry is one canonical path and its signed content digest.
type ManifestEntry struct {
	Path   string
	SHA256 [sha256.Size]byte
}

// EncodeManifest canonicalizes the supplied paths, rejects duplicate paths,
// sorts records by path byte order, and returns the exact bytes to sign.
func EncodeManifest(entries []ManifestEntry) ([]byte, error) {
	canonical := make([]ManifestEntry, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		path, err := NormalizePath(entry.Path)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[path]; exists {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateManifestPath, path)
		}
		seen[path] = struct{}{}
		entry.Path = path
		canonical = append(canonical, entry)
	}

	sort.Slice(canonical, func(i, j int) bool {
		return canonical[i].Path < canonical[j].Path
	})

	var encoded bytes.Buffer
	for _, entry := range canonical {
		encoded.WriteString(strconv.Itoa(len(entry.Path)))
		encoded.WriteByte(':')
		encoded.WriteString(entry.Path)
		encoded.Write(entry.SHA256[:])
	}
	return encoded.Bytes(), nil
}

// SignManifest encodes entries with EncodeManifest and signs those exact bytes
// with Ed25519.
func SignManifest(privateKey ed25519.PrivateKey, entries []ManifestEntry) (manifest, signature []byte, err error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return nil, nil, fmt.Errorf("%w: Ed25519 private key has length %d", ErrInvalidSignature, len(privateKey))
	}
	manifest, err = EncodeManifest(entries)
	if err != nil {
		return nil, nil, err
	}
	return manifest, ed25519.Sign(privateKey, manifest), nil
}

// ParseManifest decodes and validates a canonical signed-manifest payload.
//
// Each record is a base-10 path length, a colon, the path bytes, and a raw
// 32-byte SHA-256 digest. Records must be strictly ordered by path bytes and
// contain distinct canonical paths.
func ParseManifest(manifest []byte) ([]ManifestEntry, error) {
	entries := make([]ManifestEntry, 0)
	seen := make(map[string]struct{})
	pos := 0
	previousPath := ""
	first := true

	for pos < len(manifest) {
		recordStart := pos
		colonOffset := bytes.IndexByte(manifest[pos:], ':')
		if colonOffset <= 0 {
			return nil, fmt.Errorf("%w: offset %d: missing decimal length", ErrInvalidManifest, recordStart)
		}

		lengthBytes := manifest[pos : pos+colonOffset]
		if len(lengthBytes) > 1 && lengthBytes[0] == '0' {
			return nil, fmt.Errorf("%w: offset %d: non-canonical path length", ErrInvalidManifest, recordStart)
		}
		for _, digit := range lengthBytes {
			if digit < '0' || digit > '9' {
				return nil, fmt.Errorf("%w: offset %d: invalid path length", ErrInvalidManifest, recordStart)
			}
		}

		pathLength, err := strconv.ParseUint(string(lengthBytes), 10, 63)
		if err != nil {
			return nil, fmt.Errorf("%w: offset %d: invalid path length: %v", ErrInvalidManifest, recordStart, err)
		}
		pos += colonOffset + 1

		remaining := len(manifest) - pos
		if pathLength > uint64(remaining) || uint64(remaining)-pathLength < sha256.Size {
			return nil, fmt.Errorf("%w: offset %d: truncated record", ErrInvalidManifest, recordStart)
		}

		pathBytes := manifest[pos : pos+int(pathLength)]
		pos += int(pathLength)
		digestStart := pos
		pos += sha256.Size

		path, err := NormalizePath(string(pathBytes))
		if err != nil {
			return nil, fmt.Errorf("%w: record at offset %d: %w", ErrInvalidManifest, recordStart, err)
		}
		if path != string(pathBytes) {
			return nil, fmt.Errorf("%w: record at offset %d: path %q is not canonical (%q)", ErrInvalidManifest, recordStart, pathBytes, path)
		}
		if _, exists := seen[path]; exists {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateManifestPath, path)
		}
		if !first && path < previousPath {
			return nil, fmt.Errorf("%w: %q follows %q", ErrManifestOrder, path, previousPath)
		}

		entry := ManifestEntry{Path: path}
		copy(entry.SHA256[:], manifest[digestStart:pos])
		entries = append(entries, entry)
		seen[path] = struct{}{}
		previousPath = path
		first = false
	}

	return entries, nil
}

// VerifySignedManifest verifies signature over the exact manifest bytes, then
// parses and validates the manifest.
func VerifySignedManifest(publicKey ed25519.PublicKey, manifest, signature []byte) ([]ManifestEntry, error) {
	if len(publicKey) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("%w: Ed25519 public key has length %d", ErrInvalidSignature, len(publicKey))
	}
	if len(signature) != ed25519.SignatureSize {
		return nil, fmt.Errorf("%w: Ed25519 signature has length %d", ErrInvalidSignature, len(signature))
	}
	if !ed25519.Verify(publicKey, manifest, signature) {
		return nil, ErrInvalidSignature
	}

	entries, err := ParseManifest(manifest)
	if err != nil {
		return nil, err
	}
	if err := validateManifestPaths(entries); err != nil {
		return nil, err
	}
	return entries, nil
}
