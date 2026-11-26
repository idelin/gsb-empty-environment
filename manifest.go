package rpki

import (
	"crypto/sha256"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"fmt"
	"math/big"
	"path"
	"strings"
	"time"
)

var (
	ErrManifest         = errors.New("rpki: manifest validation failed")
	ErrManifestHash     = errors.New("rpki: manifest file hash mismatch")
	ErrManifestMissing  = errors.New("rpki: manifest lists missing file")
	ErrManifestExtra    = errors.New("rpki: publication point has unlisted file")
	ErrManifestSequence = errors.New("rpki: manifest sequence/rollback violation")
	ErrManifestExpired  = errors.New("rpki: manifest expired")
)

// FileHash is one RFC 9286 FileAndHash entry.
type FileHash struct {
	Name   string
	SHA256 [sha256.Size]byte
}

// Manifest is the parsed RFC 9286 eContent.
type Manifest struct {
	ManifestNumber *big.Int
	ThisUpdate     time.Time
	NextUpdate     time.Time
	FileHashes     []FileHash
}

type manifestWire struct {
	Version        asn1.RawValue `asn1:"optional,tag:0,context"`
	ManifestNumber *big.Int
	ThisUpdate     time.Time `asn1:"generalized"`
	NextUpdate     time.Time `asn1:"generalized"`
	FileHashAlg    pkix.AlgorithmIdentifier
	FileList       []fileAndHashWire
}

type fileAndHashWire struct {
	File string
	Hash asn1.BitString
}

// ParseManifest parses and performs structural validation of manifest eContent.
func ParseManifest(der []byte) (*Manifest, error) {
	var wire manifestWire
	rest, err := asn1.Unmarshal(der, &wire)
	if err != nil {
		return nil, fmt.Errorf("%w: Manifest: %v", ErrManifest, err)
	}
	if len(rest) != 0 {
		return nil, fmt.Errorf("%w: trailing Manifest bytes", ErrManifest)
	}
	if wire.ManifestNumber == nil || wire.ManifestNumber.Sign() < 0 {
		return nil, fmt.Errorf("%w: manifestNumber missing or negative", ErrManifest)
	}
	if wire.ThisUpdate.IsZero() || wire.NextUpdate.IsZero() {
		return nil, fmt.Errorf("%w: manifest update times missing", ErrManifest)
	}
	if wire.NextUpdate.Before(wire.ThisUpdate) {
		return nil, fmt.Errorf("%w: nextUpdate before thisUpdate", ErrManifest)
	}
	if !wire.FileHashAlg.Algorithm.Equal(oidSHA256) {
		return nil, fmt.Errorf("%w: fileHashAlg must be SHA-256, got %s", ErrManifest, wire.FileHashAlg.Algorithm)
	}
	files := make([]FileHash, 0, len(wire.FileList))
	seen := map[string]struct{}{}
	for _, item := range wire.FileList {
		name, err := ValidateManifestFileName(item.File)
		if err != nil {
			return nil, err
		}
		if _, dup := seen[name]; dup {
			return nil, fmt.Errorf("%w: duplicate manifest entry %q", ErrManifest, name)
		}
		seen[name] = struct{}{}
		if item.Hash.BitLength != sha256.Size*8 || len(item.Hash.Bytes) != sha256.Size {
			return nil, fmt.Errorf("%w: %q hash is not 256 bits", ErrManifest, name)
		}
		var sum [sha256.Size]byte
		copy(sum[:], item.Hash.Bytes)
		files = append(files, FileHash{Name: name, SHA256: sum})
	}
	return &Manifest{
		ManifestNumber: new(big.Int).Set(wire.ManifestNumber),
		ThisUpdate:     wire.ThisUpdate.UTC(),
		NextUpdate:     wire.NextUpdate.UTC(),
		FileHashes:     files,
	}, nil
}

// ValidateManifestFileName permits only a basename in the publication point.
func ValidateManifestFileName(name string) (string, error) {
	if name == "" || name == "." || name == ".." {
		return "", fmt.Errorf("%w: invalid file name %q", ErrManifest, name)
	}
	if strings.ContainsAny(name, "/\\") || path.Base(name) != name {
		return "", fmt.Errorf("%w: manifest entry must be a basename: %q", ErrManifest, name)
	}
	if strings.Contains(name, "\x00") {
		return "", fmt.Errorf("%w: NUL in file name", ErrManifest)
	}
	return name, nil
}

// CheckFreshness enforces manifest validity time. A small clock skew may allow
// a thisUpdate slightly in the future.
func (m *Manifest) CheckFreshness(now time.Time, futureSkew time.Duration) error {
	now = now.UTC()
	if futureSkew < 0 {
		futureSkew = 0
	}
	if now.After(m.NextUpdate) {
		return fmt.Errorf("%w: manifest %s nextUpdate %s", ErrManifestExpired, m.ManifestNumber, m.NextUpdate.Format(time.RFC3339))
	}
	if m.ThisUpdate.After(now.Add(futureSkew)) {
		return fmt.Errorf("%w: manifest thisUpdate %s is in the future beyond %s", ErrManifest, m.ThisUpdate.Format(time.RFC3339), now.Format(time.RFC3339))
	}
	return nil
}

// VerifyHashes fails closed if any listed object is absent or any digest differs.
// It also rejects unlisted regular files when strictListing is true.
func (m *Manifest) VerifyHashes(repo Repository, dir, manifestFileName string, strictListing bool) ([]string, error) {
	manifestBase := path.Base(manifestFileName)
	listed := map[string][sha256.Size]byte{}
	for _, fh := range m.FileHashes {
		if fh.Name == manifestBase {
			return nil, fmt.Errorf("%w: manifest must not hash itself: %s", ErrManifest, fh.Name)
		}
		full, err := JoinRepositoryPath(dir, fh.Name)
		if err != nil {
			return nil, err
		}
		content, err := repo.Open(full)
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %v", ErrManifestMissing, fh.Name, err)
		}
		actual := sha256.Sum256(content)
		if actual != fh.SHA256 {
			return nil, fmt.Errorf("%w: %s", ErrManifestHash, fh.Name)
		}
		listed[fh.Name] = fh.SHA256
	}
	entries, err := repo.List(dir)
	if err != nil {
		return nil, fmt.Errorf("%w: list publication point: %v", ErrManifest, err)
	}
	var checked []string
	for _, entry := range entries {
		base := path.Base(entry)
		if base == manifestBase {
			continue
		}
		if _, ok := listed[base]; !ok {
			if strictListing {
				return nil, fmt.Errorf("%w: %s", ErrManifestExtra, base)
			}
			continue
		}
		checked = append(checked, entry)
	}
	if strictListing && len(checked) != len(m.FileHashes) {
		return nil, fmt.Errorf("%w: directory lists %d hashed files but manifest has %d", ErrManifest, len(checked), len(m.FileHashes))
	}
	return checked, nil
}

// MarshalManifest encodes manifest eContent. It is primarily used by tests and
// local publication tooling.
func MarshalManifest(m *Manifest) ([]byte, error) {
	if m == nil || m.ManifestNumber == nil || m.ManifestNumber.Sign() < 0 {
		return nil, fmt.Errorf("%w: manifest number required", ErrManifest)
	}
	files := make([]fileAndHashWire, 0, len(m.FileHashes))
	for _, fh := range m.FileHashes {
		name, err := ValidateManifestFileName(fh.Name)
		if err != nil {
			return nil, err
		}
		files = append(files, fileAndHashWire{
			File: name,
			Hash: asn1.BitString{Bytes: append([]byte(nil), fh.SHA256[:]...), BitLength: sha256.Size * 8},
		})
	}
	wire := manifestWire{
		ManifestNumber: new(big.Int).Set(m.ManifestNumber),
		ThisUpdate:     m.ThisUpdate.UTC().Truncate(time.Second),
		NextUpdate:     m.NextUpdate.UTC().Truncate(time.Second),
		FileHashAlg: pkix.AlgorithmIdentifier{
			Algorithm:  oidSHA256,
			Parameters: asn1.RawValue{Tag: asn1.TagNull},
		},
		FileList: files,
	}
	return asn1.Marshal(wire)
}
