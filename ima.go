package netsec

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// XattrName is the extended attribute used by IMA appraisal.
const XattrName = "security.ima"

// SecurityIMA is an alias for XattrName.
const SecurityIMA = XattrName

const (
	// IMAHeaderSize is the fixed signature-v2 header size.
	IMAHeaderSize = 9

	// IMAVersion2 is the only accepted attribute version.
	IMAVersion2 = 2

	// TypeDigest marks an attribute that carries a digest but no signature.
	TypeDigest = 0x01
	// TypeSignature marks an attribute that carries a signature.
	TypeSignature = 0x03

	IMATypeDigest    = TypeDigest
	IMATypeSignature = TypeSignature

	// IMA v2 hash algorithm identifiers.
	AlgorithmSHA1   = 0
	AlgorithmSHA256 = 1
	AlgorithmSHA384 = 2
	AlgorithmSHA512 = 3
	AlgorithmSHA224 = 4

	AlgoSHA1   = AlgorithmSHA1
	AlgoSHA256 = AlgorithmSHA256
	AlgoSHA384 = AlgorithmSHA384
	AlgoSHA512 = AlgorithmSHA512
	AlgoSHA224 = AlgorithmSHA224
)

// The exported sentinel errors are intended to be matched with errors.Is.
var (
	ErrAttributeMissing  = errors.New("ima: attribute is missing")
	ErrShortAttribute    = errors.New("ima: attribute is shorter than the header")
	ErrBadVersion        = errors.New("ima: unsupported attribute version")
	ErrLengthMismatch    = errors.New("ima: declared payload length does not match attribute size")
	ErrUnknownType       = errors.New("ima: unknown attribute type")
	ErrUnknownAlgorithm  = errors.New("ima: unknown digest or signature algorithm")
	ErrEmptySignature    = errors.New("ima: signature payload is empty")
	ErrSignatureRequired = errors.New("ima: path requires a signature attribute")
	ErrDigestNotAllowed  = ErrSignatureRequired
	ErrUntrustedKeyID    = errors.New("ima: signing key is not trusted")
	ErrXattrUnsupported  = errors.New("ima: extended attributes are unsupported on this platform")
	ErrFileChanged       = errors.New("ima: file changed during appraisal")
)

// Header is the signature-v2 attribute header:
//
//	offset 0: type (1 byte)
//	offset 1: version (1 byte)
//	offset 2: algorithm (1 byte)
//	offset 3: key id (4 bytes, big endian)
//	offset 7: payload length (2 bytes, big endian)
type Header struct {
	Type            uint8
	Version         uint8
	Algorithm       uint8
	KeyID           uint32
	SignatureLength uint16
}

// SigLen returns the payload length declared by the header.
func (h Header) SigLen() uint16 { return h.SignatureLength }

// IsSignature reports whether the header describes a signature attribute.
func (h Header) IsSignature() bool { return h.Type == TypeSignature }

// IsDigest reports whether the header describes a digest-only attribute.
func (h Header) IsDigest() bool { return h.Type == TypeDigest }

// Attribute is a fully parsed IMA extended attribute.
type Attribute struct {
	Header
	Payload []byte
}

// Signature returns the signature bytes. For a digest attribute it returns the
// digest bytes, so callers should check Type before using it as a signature.
func (a Attribute) Signature() []byte { return a.Payload }

func knownAlgorithm(algo uint8) bool {
	switch algo {
	case AlgorithmSHA1, AlgorithmSHA256, AlgorithmSHA384, AlgorithmSHA512, AlgorithmSHA224:
		return true
	default:
		return false
	}
}

// ParseAttribute parses and strictly validates a complete IMA attribute.
//
// A truncated header, version other than 2, unknown type, unknown algorithm,
// and any difference between the declared payload length and len(raw)-9 are
// rejected. The declared length is never used as an allocation size.
func ParseAttribute(raw []byte) (Attribute, error) {
	if len(raw) < IMAHeaderSize {
		return Attribute{}, fmt.Errorf("%w: got %d bytes", ErrShortAttribute, len(raw))
	}

	h := Header{
		Type:            raw[0],
		Version:         raw[1],
		Algorithm:       raw[2],
		KeyID:           binary.BigEndian.Uint32(raw[3:7]),
		SignatureLength: binary.BigEndian.Uint16(raw[7:9]),
	}

	if h.Version != IMAVersion2 {
		return Attribute{}, fmt.Errorf("%w: got %d, want %d", ErrBadVersion, h.Version, IMAVersion2)
	}
	if h.Type != TypeDigest && h.Type != TypeSignature {
		return Attribute{}, fmt.Errorf("%w: got %#x", ErrUnknownType, h.Type)
	}
	if !knownAlgorithm(h.Algorithm) {
		return Attribute{}, fmt.Errorf("%w: got %d", ErrUnknownAlgorithm, h.Algorithm)
	}

	declared := int(h.SignatureLength)
	actual := len(raw) - IMAHeaderSize
	if declared != actual {
		return Attribute{}, fmt.Errorf("%w: declared %d, actual %d", ErrLengthMismatch, declared, actual)
	}

	payload := make([]byte, actual)
	copy(payload, raw[IMAHeaderSize:])
	return Attribute{Header: h, Payload: payload}, nil
}

// ParseIMAHeader is an alias for ParseAttribute kept for callers that name the
// operation after the attribute header. It validates the whole attribute, not
// just the first nine bytes.
func ParseIMAHeader(raw []byte) (Header, error) {
	a, err := ParseAttribute(raw)
	return a.Header, err
}

// Parse is an alias for ParseAttribute.
func Parse(raw []byte) (Attribute, error) { return ParseAttribute(raw) }

// ParseHeader is an alias for ParseIMAHeader.
func ParseHeader(raw []byte) (Header, error) { return ParseIMAHeader(raw) }

// Policy controls which attributes are acceptable for a path.
//
// RequireSignature is the default for all paths. RequiredPaths and ExemptPaths
// are optional glob patterns (filepath.Match syntax) that override the default.
// TrustedKeyIDs is the authoritative key-id set; TrustedKeys is accepted as a
// convenient slice form.
type Policy struct {
	RequireSignature bool
	TrustedKeyIDs    map[uint32]struct{}
	TrustedKeys      []uint32
	RequiredPaths    []string
	ExemptPaths      []string
}

// NewPolicy builds a policy that requires signatures on every path and trusts
// the supplied key ids.
func NewPolicy(requireSignature bool, trusted ...uint32) Policy {
	p := Policy{RequireSignature: requireSignature}
	if len(trusted) != 0 {
		p.TrustedKeyIDs = make(map[uint32]struct{}, len(trusted))
		for _, id := range trusted {
			p.TrustedKeyIDs[id] = struct{}{}
		}
	}
	return p
}

// RequireSignatures returns a policy that requires signatures everywhere and
// trusts the supplied key ids.
func RequireSignatures(trusted ...uint32) Policy { return NewPolicy(true, trusted...) }

// IsTrustedKeyID reports whether id is in the policy's trusted set.
func (p Policy) IsTrustedKeyID(id uint32) bool {
	if _, ok := p.TrustedKeyIDs[id]; ok {
		return true
	}
	for _, trusted := range p.TrustedKeys {
		if trusted == id {
			return true
		}
	}
	return false
}

func clonePolicy(p Policy) Policy {
	cp := p
	if p.TrustedKeyIDs != nil {
		cp.TrustedKeyIDs = make(map[uint32]struct{}, len(p.TrustedKeyIDs))
		for id := range p.TrustedKeyIDs {
			cp.TrustedKeyIDs[id] = struct{}{}
		}
	}
	cp.TrustedKeys = append([]uint32(nil), p.TrustedKeys...)
	cp.RequiredPaths = append([]string(nil), p.RequiredPaths...)
	cp.ExemptPaths = append([]string(nil), p.ExemptPaths...)
	return cp
}

func matchesAny(patterns []string, path string) bool {
	clean := filepath.ToSlash(path)
	for _, pattern := range patterns {
		pattern = filepath.ToSlash(pattern)
		if ok, _ := filepath.Match(pattern, clean); ok {
			return true
		}
		// Matching the base name as well makes patterns such as "*.so" useful
		// for absolute paths without requiring ** support.
		if dir, base := filepath.Split(clean); dir != "" {
			if ok, _ := filepath.Match(pattern, base); ok {
				return true
			}
		}
	}
	return false
}

// RequiresSignature reports whether path is subject to mandatory signing.
func (p Policy) RequiresSignature(path string) bool {
	if matchesAny(p.RequiredPaths, path) {
		return true
	}
	if matchesAny(p.ExemptPaths, path) {
		return false
	}
	return p.RequireSignature
}

// Appraise applies the policy to an already parsed attribute.
func (p Policy) Appraise(path string, a Attribute) error {
	required := p.RequiresSignature(path)

	switch a.Type {
	case TypeDigest:
		if required {
			return fmt.Errorf("%w: digest-only attribute at %s", ErrSignatureRequired, path)
		}
		return nil

	case TypeSignature:
		if !p.IsTrustedKeyID(a.KeyID) {
			return fmt.Errorf("%w: key id %d at %s", ErrUntrustedKeyID, a.KeyID, path)
		}
		if len(a.Payload) == 0 {
			return fmt.Errorf("%w at %s", ErrEmptySignature, path)
		}
		return nil

	default:
		return fmt.Errorf("%w: got %#x", ErrUnknownType, a.Type)
	}
}

// EvaluateAttribute applies policy to raw attribute bytes without consulting
// the filesystem. It is useful when an engine has already obtained the
// attribute and only needs the appraisal decision.
func EvaluateAttribute(path string, policy Policy, raw []byte) error {
	a, err := ParseAttribute(raw)
	if err != nil {
		return err
	}
	return policy.Appraise(path, a)
}

// AttributeReader retrieves one extended attribute. A missing attribute must
// wrap ErrAttributeMissing.
type AttributeReader interface {
	ReadAttribute(path, name string) ([]byte, error)
}

// XattrReader is an alias for AttributeReader.
type XattrReader = AttributeReader

// XattrStore is an alias for AttributeReader.
type XattrStore = AttributeReader

// AttributeReaderFunc adapts a function to AttributeReader.
type AttributeReaderFunc func(path, name string) ([]byte, error)

// ReadAttribute implements AttributeReader.
func (f AttributeReaderFunc) ReadAttribute(path, name string) ([]byte, error) {
	return f(path, name)
}

// OSReader reads attributes from the operating system.
type OSReader struct{}

// ReadAttribute implements AttributeReader.
func (OSReader) ReadAttribute(path, name string) ([]byte, error) {
	raw, err := platformReadXattr(path, name)
	if err != nil {
		return nil, err
	}
	return raw, nil
}

// DefaultReader is the platform attribute reader.
var DefaultReader AttributeReader = OSReader{}

// CacheStats contains cumulative evaluator cache counters.
type CacheStats struct {
	Hits      int64
	Misses    int64
	Evictions int64
}

type fileID struct {
	dev  uint64
	ino  uint64
	path string
}

type fileVersion struct {
	size     int64
	mode     os.FileMode
	modNS    int64
	changeNS int64
}

type cacheEntry struct {
	path     string
	version  fileVersion
	attrHash [sha256.Size]byte
	required bool
	err      error
	header   Header
}

// Evaluator checks attributes and caches decisions by file identity, so hard
// links share one entry while a write or attribute replacement invalidates it.
type Evaluator struct {
	mu     sync.Mutex
	policy Policy
	reader AttributeReader
	cache  map[fileID]cacheEntry
	stats  CacheStats
}

// Engine is an alias for Evaluator.
type Engine = Evaluator

// NewEvaluator returns an evaluator that reads security.ima from the OS.
func NewEvaluator(policy Policy) *Evaluator {
	return NewEvaluatorWithReader(policy, DefaultReader)
}

// New is an alias for NewEvaluator.
func New(policy Policy) *Evaluator { return NewEvaluator(policy) }

// NewEngine is an alias for NewEvaluator.
func NewEngine(policy Policy) *Evaluator { return NewEvaluator(policy) }

// NewEvaluatorWithReader returns an evaluator using the supplied attribute
// reader. A nil reader falls back to DefaultReader.
func NewEvaluatorWithReader(policy Policy, reader AttributeReader) *Evaluator {
	if reader == nil {
		reader = DefaultReader
	}
	return &Evaluator{
		policy: clonePolicy(policy),
		reader: reader,
		cache:  make(map[fileID]cacheEntry),
	}
}

// NewEvaluatorWithStore is an alias for NewEvaluatorWithReader.
func NewEvaluatorWithStore(policy Policy, reader AttributeReader) *Evaluator {
	return NewEvaluatorWithReader(policy, reader)
}

// NewEvaluatorWithXattr is an alias for NewEvaluatorWithReader.
func NewEvaluatorWithXattr(policy Policy, reader AttributeReader) *Evaluator {
	return NewEvaluatorWithReader(policy, reader)
}

// NewEngineWithReader is an alias for NewEvaluatorWithReader.
func NewEngineWithReader(policy Policy, reader AttributeReader) *Evaluator {
	return NewEvaluatorWithReader(policy, reader)
}

// SetPolicy replaces the policy and clears cached decisions.
func (e *Evaluator) SetPolicy(policy Policy) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.policy = clonePolicy(policy)
	e.cache = make(map[fileID]cacheEntry)
}

// Policy returns the current policy.
func (e *Evaluator) Policy() Policy {
	e.mu.Lock()
	defer e.mu.Unlock()
	return clonePolicy(e.policy)
}

// Stats returns a snapshot of the cache counters.
func (e *Evaluator) Stats() CacheStats {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.stats
}

// ResetStats clears the cache counters without clearing the cache.
func (e *Evaluator) ResetStats() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.stats = CacheStats{}
}

// ClearCache discards all cached decisions.
func (e *Evaluator) ClearCache() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cache = make(map[fileID]cacheEntry)
}

// Invalidate discards cached decisions for path. When path is already gone,
// entries that were last evaluated through that path are removed.
func (e *Evaluator) Invalidate(path string) {
	e.mu.Lock()
	defer e.mu.Unlock()

	info, err := os.Stat(path)
	if err == nil {
		id := fileIDFor(path, info)
		delete(e.cache, id)
		return
	}
	clean := filepath.Clean(path)
	for id, entry := range e.cache {
		if entry.path == clean {
			delete(e.cache, id)
		}
	}
}

// Evaluate checks the IMA attribute for path and returns nil only when the
// attribute is acceptable under the policy. Callers should invoke it before
// exec or mmap.
func (e *Evaluator) Evaluate(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}

	raw, err := e.reader.ReadAttribute(path, XattrName)
	if err != nil {
		e.invalidateID(fileIDFor(path, info))
		return err
	}

	// Fail closed if the path was replaced or written while the attribute was
	// being read. The caller may retry the appraisal.
	after, err := os.Stat(path)
	if err != nil {
		e.invalidateID(fileIDFor(path, info))
		return err
	}
	if fileIDFor(path, after) != fileIDFor(path, info) || fileVersionOf(after) != fileVersionOf(info) {
		e.invalidateID(fileIDFor(path, info))
		return fmt.Errorf("%w: %s", ErrFileChanged, path)
	}
	return e.evaluateParsed(path, after, raw)
}

// EvaluateData evaluates already-read attribute bytes for path. It is useful
// for callers that obtain the attribute themselves, and for tests.
func (e *Evaluator) EvaluateData(path string, raw []byte) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	return e.evaluateParsed(path, info, raw)
}

func (e *Evaluator) evaluateParsed(path string, info os.FileInfo, raw []byte) error {
	attr, parseErr := ParseAttribute(raw)
	hash := sha256.Sum256(raw)
	id := fileIDFor(path, info)
	version := fileVersionOf(info)
	cleanPath := filepath.Clean(path)

	e.mu.Lock()
	defer e.mu.Unlock()

	required := e.policy.RequiresSignature(path)
	entry, ok := e.cache[id]
	if ok && entry.version == version && entry.attrHash == hash && entry.required == required {
		e.stats.Hits++
		return entry.err
	}
	if ok {
		e.stats.Evictions++
	}
	e.stats.Misses++

	var result error
	if parseErr != nil {
		result = parseErr
	} else {
		result = e.policy.Appraise(path, attr)
	}

	e.cache[id] = cacheEntry{
		path:     cleanPath,
		version:  version,
		attrHash: hash,
		required: required,
		err:      result,
		header:   attr.Header,
	}

	return result
}

func (e *Evaluator) invalidateID(id fileID) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.cache, id)
}

// Check is an alias for Evaluate.
func (e *Evaluator) Check(path string) error { return e.Evaluate(path) }

// Verify is an alias for Evaluate.
func (e *Evaluator) Verify(path string) error { return e.Evaluate(path) }

// BeforeExec evaluates path before execution.
func (e *Evaluator) BeforeExec(path string) error { return e.Evaluate(path) }

// BeforeMmap evaluates path before memory mapping.
func (e *Evaluator) BeforeMmap(path string) error { return e.Evaluate(path) }

// EvaluateForExec evaluates path before execution.
func (e *Evaluator) EvaluateForExec(path string) error { return e.Evaluate(path) }

// EvaluateForMmap evaluates path before memory mapping.
func (e *Evaluator) EvaluateForMmap(path string) error { return e.Evaluate(path) }

// EvaluateFile evaluates path with a fresh, uncached evaluator.
func EvaluateFile(path string, policy Policy) error {
	return NewEvaluator(policy).Evaluate(path)
}

// Evaluate is the package-level convenience form of EvaluateFile.
func Evaluate(path string, policy Policy) error { return EvaluateFile(path, policy) }

// CheckFile is an alias for EvaluateFile.
func CheckFile(path string, policy Policy) error { return EvaluateFile(path, policy) }

// VerifyFile is an alias for EvaluateFile.
func VerifyFile(path string, policy Policy) error { return EvaluateFile(path, policy) }

// Verify is the package-level form of EvaluateFile.
func Verify(path string, policy Policy) error { return EvaluateFile(path, policy) }

// Check is the package-level form of EvaluateFile.
func Check(path string, policy Policy) error { return EvaluateFile(path, policy) }

// BeforeExec is the package-level convenience form for pre-exec checks.
func BeforeExec(path string, policy Policy) error { return EvaluateFile(path, policy) }

// BeforeMmap is the package-level convenience form for pre-mmap checks.
func BeforeMmap(path string, policy Policy) error { return EvaluateFile(path, policy) }
