package netsec

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"sync"
)

// Digest identifies a model, adapter, or tensor. Tensor digests produced by
// this package are lowercase, hex-encoded SHA-256 values. Model and adapter
// digests are treated as opaque host identifiers and are compared exactly.
type Digest string

// DigestOf returns the lowercase hexadecimal SHA-256 digest of data.
func DigestOf(data []byte) Digest {
	sum := sha256.Sum256(data)
	return Digest(hex.EncodeToString(sum[:]))
}

// DType is the element type of a tensor. Raw factor bytes are interpreted as
// little-endian IEEE floating-point values.
type DType string

const (
	DTypeFloat16  DType = "float16"
	DTypeBFloat16 DType = "bfloat16"
	DTypeFloat32  DType = "float32"
	DTypeFloat64  DType = "float64"
)

func (d DType) elementSize() (int64, bool) {
	switch d {
	case DTypeFloat16, DTypeBFloat16:
		return 2, true
	case DTypeFloat32:
		return 4, true
	case DTypeFloat64:
		return 8, true
	default:
		return 0, false
	}
}

// SupportedDTypes returns the tensor element types this package can validate.
func SupportedDTypes() []DType {
	return []DType{DTypeFloat16, DTypeBFloat16, DTypeFloat32, DTypeFloat64}
}

// Rational is an exact, signed rational number. A valid scale has a positive
// denominator and is already in lowest terms.
type Rational struct {
	Numerator   int64 `json:"numerator"`
	Denominator int64 `json:"denominator"`
}

// String returns the canonical "numerator/denominator" form.
func (r Rational) String() string {
	return fmt.Sprintf("%d/%d", r.Numerator, r.Denominator)
}

// BaseModelManifest is the host-fixed description of the base model loaded by
// the inference host. It must not come from the adapter's signer.
type BaseModelManifest struct {
	ModelDigest Digest      `json:"modelDigest"`
	Layers      []BaseLayer `json:"layers"`
}

// BaseLayer describes one stable layer in a base model.
type BaseLayer struct {
	ID               string `json:"id"`
	WeightDigest     Digest `json:"weightDigest"`
	InputDim         int    `json:"inputDim"`
	OutputDim        int    `json:"outputDim"`
	DType            DType  `json:"dtype"`
	SecurityCritical bool   `json:"securityCritical"`
}

// AdapterManifest is the signed portion of an adapter package.
type AdapterManifest struct {
	AdapterDigest   Digest          `json:"adapterDigest"`
	BaseModelDigest Digest          `json:"baseModelDigest"`
	Version         string          `json:"version"`
	Targets         []AdapterTarget `json:"targets"`
}

// AdapterTarget binds one adapter factor pair to a stable base-layer ID.
//
// A has shape [Rank, base.InputDim] and B has shape [base.OutputDim, Rank].
type AdapterTarget struct {
	LayerID string   `json:"layerId"`
	ADigest Digest   `json:"aDigest"`
	BDigest Digest   `json:"bDigest"`
	DType   DType    `json:"dtype"`
	Scale   Rational `json:"scale"`
	Rank    int      `json:"rank"`
}

// AdapterEnvelope carries exactly the bytes that were signed and the detached
// signature. The verifier and the parser both consume the same byte slice.
type AdapterEnvelope struct {
	Manifest  []byte
	Signature []byte
}

// SignatureVerifier verifies a detached signature over payload. The payload
// is the complete adapter manifest JSON, not a re-serialization of parsed
// fields.
type SignatureVerifier interface {
	Verify(payload, signature []byte) error
}

// VerifyFunc adapts a function to SignatureVerifier.
type VerifyFunc func(payload, signature []byte) error

// Verify calls f(payload, signature).
func (f VerifyFunc) Verify(payload, signature []byte) error {
	return f(payload, signature)
}

// SecurityPolicy decides whether a specific adapter digest may target
// security-critical layers. A nil policy denies all such targets.
type SecurityPolicy interface {
	AllowSecurityCritical(adapterDigest Digest) bool
}

// SecurityPolicyFunc adapts a function to SecurityPolicy.
type SecurityPolicyFunc func(adapterDigest Digest) bool

// AllowSecurityCritical calls f(adapterDigest).
func (f SecurityPolicyFunc) AllowSecurityCritical(adapterDigest Digest) bool {
	return f(adapterDigest)
}

// SecurityPolicySet is an allow-list keyed by adapter digest.
type SecurityPolicySet map[Digest]struct{}

// AllowSecurityCritical reports whether adapterDigest is present in the set.
func (s SecurityPolicySet) AllowSecurityCritical(adapterDigest Digest) bool {
	_, ok := s[adapterDigest]
	return ok
}

// TensorShape is the logical two-dimensional shape of a factor tensor.
type TensorShape struct {
	Rows int `json:"rows"`
	Cols int `json:"cols"`
}

// TensorDescriptor describes the bytes exposed by a tensor handle.
type TensorDescriptor struct {
	Shape TensorShape `json:"shape"`
	DType DType       `json:"dtype"`
}

// TensorHandle is an audited, read-only source of tensor bytes. OpenReadOnly
// must return a fresh stream positioned at byte zero. Admission reads and
// copies the complete stream before publishing a generation, so later changes
// to a provider's backing storage cannot change an admitted generation.
type TensorHandle interface {
	OpenReadOnly() (io.ReadCloser, error)
}

// TensorDescriptorProvider is implemented by handles that can attest their
// logical shape and dtype. When available, admission requires both to match
// the signed target and the expected layer-derived shape exactly.
type TensorDescriptorProvider interface {
	Descriptor() TensorDescriptor
}

// TensorShapeProvider is an optional compatibility interface for handles that
// expose only a shape.
type TensorShapeProvider interface {
	Shape() TensorShape
}

// TensorDTypeProvider is an optional compatibility interface for handles that
// expose only a dtype.
type TensorDTypeProvider interface {
	DType() DType
}

// TensorProvider resolves a signed tensor digest to an audited read-only
// handle. Implementations must not return mutable backing buffers directly.
type TensorProvider interface {
	OpenTensor(digest Digest) (TensorHandle, error)
}

// TensorProviderFunc adapts a function to TensorProvider.
type TensorProviderFunc func(digest Digest) (TensorHandle, error)

// OpenTensor calls f(digest).
func (f TensorProviderFunc) OpenTensor(digest Digest) (TensorHandle, error) {
	return f(digest)
}

// ReadOnlyTensor is an in-memory TensorHandle. Its constructor copies data,
// and OpenReadOnly returns an independent reader over the immutable copy.
type ReadOnlyTensor struct {
	mu         sync.RWMutex
	data       []byte
	descriptor TensorDescriptor
}

// NewReadOnlyTensor copies data and records its logical descriptor.
func NewReadOnlyTensor(data []byte, shape TensorShape, dtype DType) *ReadOnlyTensor {
	return &ReadOnlyTensor{
		data:       append([]byte(nil), data...),
		descriptor: TensorDescriptor{Shape: shape, DType: dtype},
	}
}

// OpenReadOnly returns a new reader over the immutable tensor bytes.
func (t *ReadOnlyTensor) OpenReadOnly() (io.ReadCloser, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return io.NopCloser(bytes.NewReader(t.data)), nil
}

// Descriptor returns the tensor's logical shape and dtype.
func (t *ReadOnlyTensor) Descriptor() TensorDescriptor {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.descriptor
}

// Config configures an admission Manager. MaxRank and both scale bounds are
// mandatory caller policy. A nil AllowedDTypes selects the package defaults;
// a non-nil empty slice denies every dtype.
type Config struct {
	Base BaseModelManifest

	Verifier            SignatureVerifier
	Tensors             TensorProvider
	Policy              SecurityPolicy
	MaxRank             int
	AllowedDTypes       []DType
	MaxScaleNumerator   int64
	MaxScaleDenominator int64

	// MaxTensorBytes and MaxManifestBytes are optional resource limits.
	MaxTensorBytes   int64
	MaxManifestBytes int

	// PublishHook runs only after a fully validated generation has been
	// published atomically. It must not call Manager.Load re-entrantly.
	PublishHook func(*Generation)
}
