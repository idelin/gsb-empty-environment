package netsec

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const (
	cacheEntryFormat = "netsec/cache-entry/v1"
	outputTreeFormat = "netsec/output-tree/v1"
)

const (
	cacheTagOutputRoot   byte = 0x01
	cacheTagProducer     byte = 0x02
	cacheTagActionKey    byte = 0x03
	cacheTagIssuedAt     byte = 0x04
	cacheTagOutputDigest byte = 0x05
)

// ErrInvalidCacheEntry means metadata required to sign or validate a cache
// entry is absent or malformed.
var ErrInvalidCacheEntry = errors.New("netsec: invalid cache entry")

// CacheEntry is signed metadata for a cached build output. The signature binds
// every field below, including the output tree digest. Signature itself is not
// part of the signed message.
type CacheEntry struct {
	OutputRoot   string
	Producer     string
	ActionKey    ActionKey
	IssuedAt     time.Time
	OutputDigest string
	Signature    []byte
}

// NewCacheEntry computes the action key from inputs and constructs an unsigned
// entry. If issuedAt is zero, the current UTC time is used. Call Sign before
// publishing the entry.
func NewCacheEntry(
	inputs ActionInputs,
	secretHMACKey []byte,
	producer string,
	outputRoot string,
	outputDigest string,
	issuedAt time.Time,
) (*CacheEntry, error) {
	key, err := inputs.Key(secretHMACKey)
	if err != nil {
		return nil, err
	}
	if producer == "" {
		return nil, fmt.Errorf("%w: producer identity", ErrInvalidCacheEntry)
	}
	if outputRoot == "" {
		return nil, fmt.Errorf("%w: output tree root", ErrInvalidCacheEntry)
	}
	if outputDigest == "" {
		return nil, fmt.Errorf("%w: output tree digest", ErrInvalidCacheEntry)
	}
	if issuedAt.IsZero() {
		issuedAt = time.Now().UTC()
	}
	return &CacheEntry{
		OutputRoot:   outputRoot,
		Producer:     producer,
		ActionKey:    key,
		IssuedAt:     issuedAt.UTC(),
		OutputDigest: outputDigest,
	}, nil
}

func (e CacheEntry) validateUnsigned() error {
	if e.OutputRoot == "" {
		return fmt.Errorf("%w: output tree root", ErrInvalidCacheEntry)
	}
	if e.Producer == "" {
		return fmt.Errorf("%w: producer identity", ErrInvalidCacheEntry)
	}
	if e.ActionKey.IsZero() {
		return fmt.Errorf("%w: action key", ErrInvalidCacheEntry)
	}
	if e.IssuedAt.IsZero() {
		return fmt.Errorf("%w: issuance time", ErrInvalidCacheEntry)
	}
	if e.OutputDigest == "" {
		return fmt.Errorf("%w: output tree digest", ErrInvalidCacheEntry)
	}
	return nil
}

// SigningBytes returns the canonical bytes covered by the Ed25519 signature.
// Issuance time is encoded as UTC Unix seconds plus nanoseconds.
func (e CacheEntry) SigningBytes() ([]byte, error) {
	if err := e.validateUnsigned(); err != nil {
		return nil, err
	}

	issued := e.IssuedAt.UTC()
	var b canonicalBuilder
	b.writeBytes([]byte(cacheEntryFormat))
	b.writeTag(cacheTagOutputRoot)
	b.writeString(e.OutputRoot)
	b.writeTag(cacheTagProducer)
	b.writeString(e.Producer)
	b.writeTag(cacheTagActionKey)
	b.writeBytes(e.ActionKey[:])
	b.writeTag(cacheTagIssuedAt)
	var issuedBytes [12]byte
	binary.BigEndian.PutUint64(issuedBytes[0:8], uint64(issued.Unix()))
	binary.BigEndian.PutUint32(issuedBytes[8:12], uint32(issued.Nanosecond()))
	b.writeBytes(issuedBytes[:])
	b.writeTag(cacheTagOutputDigest)
	b.writeString(e.OutputDigest)
	return append([]byte(nil), b.Bytes()...), nil
}

// Sign sets Signature over SigningBytes. A 32-byte seed is accepted in
// addition to a 64-byte Ed25519 private key.
func (e *CacheEntry) Sign(privateKey ed25519.PrivateKey) error {
	if e == nil {
		return fmt.Errorf("%w: nil entry", ErrInvalidCacheEntry)
	}
	priv := privateKey
	if len(priv) == ed25519.SeedSize {
		priv = ed25519.NewKeyFromSeed(priv)
	}
	if len(priv) != ed25519.PrivateKeySize {
		return fmt.Errorf("%w: Ed25519 private key size %d", ErrInvalidCacheEntry, len(privateKey))
	}
	message, err := e.SigningBytes()
	if err != nil {
		return err
	}
	e.Signature = ed25519.Sign(priv, message)
	return nil
}

// VerifySignature verifies the entry Signature with publicKey.
func (e CacheEntry) VerifySignature(publicKey ed25519.PublicKey) error {
	if len(publicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: Ed25519 public key size %d", ErrInvalidCacheEntry, len(publicKey))
	}
	if len(e.Signature) != ed25519.SignatureSize {
		return fmt.Errorf("%w: Ed25519 signature size %d", ErrInvalidCacheEntry, len(e.Signature))
	}
	message, err := e.SigningBytes()
	if err != nil {
		return err
	}
	if !ed25519.Verify(publicKey, message, e.Signature) {
		return fmt.Errorf("%w: Ed25519 signature does not verify", ErrInvalidCacheEntry)
	}
	return nil
}

// MissReason identifies why a cache entry is not reusable.
type MissReason string

const (
	MissNone                 MissReason = ""
	MissNoEntry              MissReason = "no_entry"
	MissMissingField         MissReason = "missing_field"
	MissInvalidActionInputs  MissReason = "invalid_action_inputs"
	MissActionKeyMismatch    MissReason = "action_key_mismatch"
	MissUnauthorizedProducer MissReason = "unauthorized_producer"
	MissBadSignature         MissReason = "bad_signature"
	MissExpired              MissReason = "expired"
	MissOutputRootMismatch   MissReason = "output_root_mismatch"
	MissUnreadableOutput     MissReason = "unreadable_output"
	MissOutputDigestMismatch MissReason = "output_digest_mismatch"
)

// HitResult is the outcome of a cache hit check. A false Hit is a cache miss,
// never a permission to use a weaker key.
type HitResult struct {
	Hit    bool
	Reason MissReason
	Detail string
}

// OK reports whether the entry may be reused.
func (r HitResult) OK() bool {
	return r.Hit
}

func (r HitResult) String() string {
	if r.Hit {
		return "cache hit"
	}
	if r.Detail == "" {
		return "cache miss: " + string(r.Reason)
	}
	return "cache miss: " + string(r.Reason) + ": " + r.Detail
}

func miss(reason MissReason, detail string) HitResult {
	return HitResult{Reason: reason, Detail: detail}
}

// HitPolicy contains the host trust configuration used to accept an entry.
// ProducerKeys is the authorization list: a producer identity not in the list
// is not authorized. Authorize, when non-nil, takes precedence and permits
// callers to resolve keys from another trust store.
//
// MaxAge of zero disables expiry checking. OutputTreeDigest defaults to
// HashOutputTree. A non-empty ExpectedOutputRoot pins the entry to one tree.
type HitPolicy struct {
	SecretHMACKey      []byte
	ProducerKeys       map[string]ed25519.PublicKey
	Authorize          func(producer string) (ed25519.PublicKey, bool)
	ExpectedOutputRoot string
	MaxAge             time.Duration
	Now                func() time.Time
	OutputTreeDigest   func(root string) (string, error)
}

func (p HitPolicy) now() time.Time {
	if p.Now == nil {
		return time.Now().UTC()
	}
	return p.Now().UTC()
}

func (p HitPolicy) authorizedKey(producer string) (ed25519.PublicKey, bool) {
	if p.Authorize != nil {
		return p.Authorize(producer)
	}
	key, ok := p.ProducerKeys[producer]
	return key, ok
}

func (p HitPolicy) outputDigest(root string) (string, error) {
	if p.OutputTreeDigest != nil {
		return p.OutputTreeDigest(root)
	}
	return HashOutputTree(root)
}

// Check recomputes every action input, verifies the producer authorization and
// signature, checks issuance time, and hashes the output tree. Every missing
// field or mismatch is a miss.
func (p HitPolicy) Check(inputs ActionInputs, entry *CacheEntry) HitResult {
	if entry == nil {
		return miss(MissNoEntry, "cache entry is nil")
	}
	switch {
	case entry.OutputRoot == "":
		return miss(MissMissingField, "output tree root")
	case entry.Producer == "":
		return miss(MissMissingField, "producer identity")
	case entry.ActionKey.IsZero():
		return miss(MissMissingField, "action key")
	case entry.IssuedAt.IsZero():
		return miss(MissMissingField, "issuance time")
	case entry.OutputDigest == "":
		return miss(MissMissingField, "output tree digest")
	case len(entry.Signature) != ed25519.SignatureSize:
		return miss(MissMissingField, "Ed25519 signature")
	}

	if p.ExpectedOutputRoot != "" && entry.OutputRoot != p.ExpectedOutputRoot {
		return miss(MissOutputRootMismatch, fmt.Sprintf("entry root %q does not match expected root %q", entry.OutputRoot, p.ExpectedOutputRoot))
	}

	publicKey, authorized := p.authorizedKey(entry.Producer)
	if !authorized || len(publicKey) != ed25519.PublicKeySize {
		return miss(MissUnauthorizedProducer, fmt.Sprintf("producer %q is not authorized", entry.Producer))
	}

	recomputed, err := inputs.Key(p.SecretHMACKey)
	if err != nil {
		return miss(MissInvalidActionInputs, err.Error())
	}
	if recomputed != entry.ActionKey {
		return miss(MissActionKeyMismatch, fmt.Sprintf("recomputed %s, entry %s", recomputed.Hex(), entry.ActionKey.Hex()))
	}

	if err := entry.VerifySignature(publicKey); err != nil {
		return miss(MissBadSignature, err.Error())
	}

	if p.MaxAge > 0 {
		now := p.now()
		if now.After(entry.IssuedAt.UTC().Add(p.MaxAge)) {
			return miss(MissExpired, fmt.Sprintf("issued at %s, now %s", entry.IssuedAt.UTC().Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)))
		}
	}

	actualDigest, err := p.outputDigest(entry.OutputRoot)
	if err != nil {
		return miss(MissUnreadableOutput, err.Error())
	}
	if subtle.ConstantTimeCompare([]byte(actualDigest), []byte(entry.OutputDigest)) != 1 {
		return miss(MissOutputDigestMismatch, fmt.Sprintf("output tree digest %s does not match entry %s", actualDigest, entry.OutputDigest))
	}

	return HitResult{Hit: true}
}

// VerifyCacheHit is a package-level convenience function for HitPolicy.Check.
func VerifyCacheHit(inputs ActionInputs, entry *CacheEntry, policy HitPolicy) HitResult {
	return policy.Check(inputs, entry)
}

type outputRecord struct {
	rel     string
	kind    byte
	mode    uint32
	size    int64
	content [sha256.Size]byte
	target  string
}

// HashOutputTree returns the SHA-256 hex digest of a deterministic encoding of
// the output tree's structure and contents. Paths are slash-normalized and
// sorted; regular-file contents, permission/special mode bits, directories,
// and symlink targets are covered. Symlinks are never followed.
func HashOutputTree(root string) (string, error) {
	if root == "" {
		return "", fmt.Errorf("netsec: empty output tree root")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("netsec: resolve output root: %w", err)
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return "", fmt.Errorf("netsec: stat output root: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		abs, err = filepath.EvalSymlinks(abs)
		if err != nil {
			return "", fmt.Errorf("netsec: resolve output root symlink: %w", err)
		}
		info, err = os.Lstat(abs)
		if err != nil {
			return "", fmt.Errorf("netsec: stat resolved output root: %w", err)
		}
	}
	if !info.IsDir() {
		return "", fmt.Errorf("netsec: output root %q is not a directory", root)
	}

	var records []outputRecord
	err = filepath.WalkDir(abs, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == abs {
			return nil
		}
		rel, err := filepath.Rel(abs, path)
		if err != nil {
			return err
		}
		entryInfo, err := d.Info()
		if err != nil {
			return err
		}

		rec := outputRecord{
			rel:  filepath.ToSlash(rel),
			mode: uint32(entryInfo.Mode()),
		}
		switch {
		case entryInfo.IsDir():
			rec.kind = 'd'
		case entryInfo.Mode().IsRegular():
			rec.kind = 'f'
			rec.size = entryInfo.Size()
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			hasher := sha256.New()
			_, copyErr := io.Copy(hasher, file)
			closeErr := file.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
			copy(rec.content[:], hasher.Sum(nil))
		case entryInfo.Mode()&os.ModeSymlink != 0:
			rec.kind = 'l'
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			rec.target = target
		default:
			return fmt.Errorf("netsec: unsupported output tree entry %q (mode %s)", rec.rel, entryInfo.Mode())
		}
		records = append(records, rec)
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("netsec: walk output tree: %w", err)
	}

	sort.Slice(records, func(i, j int) bool {
		return records[i].rel < records[j].rel
	})

	var b canonicalBuilder
	b.writeBytes([]byte(outputTreeFormat))
	b.writeUint64(uint64(len(records)))
	for _, rec := range records {
		b.writeByte(rec.kind)
		b.writeString(rec.rel)
		b.writeUint64(uint64(rec.mode))
		switch rec.kind {
		case 'f':
			b.writeUint64(uint64(rec.size))
			b.writeBytes(rec.content[:])
		case 'l':
			b.writeString(rec.target)
		}
	}
	sum := sha256.Sum256(b.Bytes())
	return hex.EncodeToString(sum[:]), nil
}
