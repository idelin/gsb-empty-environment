package netsec

import (
	"fmt"
	"sync"
)

const defaultMaxTensorBytes = 256 << 20

// CurrentInfo is a safe metadata snapshot of the published generation.
type CurrentInfo struct {
	ID              uint64
	AdapterDigest   Digest
	BaseModelDigest Digest
	Version         string
}

// Manager verifies, validates, publishes, and revokes adapter generations.
type Manager struct {
	base      *BaseModel
	verifier  SignatureVerifier
	tensors   TensorProvider
	policy    SecurityPolicy
	maxRank   int
	allowed   map[DType]struct{}
	maxScaleN int64
	maxScaleD int64

	maxTensorBytes   int64
	maxManifestBytes int
	publishHook      func(*Generation)

	admitMu sync.Mutex
	mu      sync.RWMutex
	current *Generation
	revoked map[Digest]struct{}
	closed  bool
	nextID  uint64
}

// New validates cfg and creates an admission manager with no active adapter.
func New(cfg Config) (*Manager, error) {
	if cfg.Verifier == nil {
		return nil, fmt.Errorf("%w: nil signature verifier", ErrInvalidConfig)
	}
	if cfg.Tensors == nil {
		return nil, fmt.Errorf("%w: nil tensor provider", ErrInvalidConfig)
	}
	if cfg.MaxRank < 1 {
		return nil, fmt.Errorf("%w: MaxRank must be positive", ErrInvalidConfig)
	}
	if cfg.MaxScaleNumerator < 1 || cfg.MaxScaleDenominator < 1 {
		return nil, fmt.Errorf("%w: scale bounds must be positive", ErrInvalidConfig)
	}

	base, err := NewBaseModel(cfg.Base)
	if err != nil {
		return nil, err
	}

	allowed := make(map[DType]struct{})
	if cfg.AllowedDTypes == nil {
		for _, dtype := range []DType{DTypeFloat16, DTypeBFloat16, DTypeFloat32} {
			allowed[dtype] = struct{}{}
		}
	} else {
		for _, dtype := range cfg.AllowedDTypes {
			if _, ok := dtype.elementSize(); !ok {
				return nil, fmt.Errorf("%w: unsupported allowed dtype %q", ErrInvalidConfig, dtype)
			}
			allowed[dtype] = struct{}{}
		}
	}

	maxTensorBytes := cfg.MaxTensorBytes
	if maxTensorBytes == 0 {
		maxTensorBytes = defaultMaxTensorBytes
	}
	if maxTensorBytes < 0 {
		return nil, fmt.Errorf("%w: MaxTensorBytes must be non-negative", ErrInvalidConfig)
	}
	maxManifestBytes := cfg.MaxManifestBytes
	if maxManifestBytes == 0 {
		maxManifestBytes = defaultMaxManifestBytes
	}
	if maxManifestBytes < 0 {
		return nil, fmt.Errorf("%w: MaxManifestBytes must be non-negative", ErrInvalidConfig)
	}

	return &Manager{
		base:             base,
		verifier:         cfg.Verifier,
		tensors:          cfg.Tensors,
		policy:           cfg.Policy,
		maxRank:          cfg.MaxRank,
		allowed:          allowed,
		maxScaleN:        cfg.MaxScaleNumerator,
		maxScaleD:        cfg.MaxScaleDenominator,
		maxTensorBytes:   maxTensorBytes,
		maxManifestBytes: maxManifestBytes,
		publishHook:      cfg.PublishHook,
		revoked:          make(map[Digest]struct{}),
	}, nil
}

// NewManager is an alias for New.
func NewManager(cfg Config) (*Manager, error) {
	return New(cfg)
}

// BaseModel returns the host-fixed base model manifest.
func (m *Manager) BaseModel() *BaseModel {
	return m.base
}

// Load verifies and validates every target before atomically publishing a new
// generation. On failure the previously published generation is unchanged.
//
// The returned pointer is backed by the Manager's current-generation reference.
// A request must use Acquire or WithGeneration to pin a generation for the
// lifetime of that request; after a later switch or revocation, tensor bytes
// may be cleared once all leases are released.
func (m *Manager) Load(envelope AdapterEnvelope) (*Generation, error) {
	m.admitMu.Lock()
	defer m.admitMu.Unlock()

	m.mu.RLock()
	closed := m.closed
	m.mu.RUnlock()
	if closed {
		return nil, ErrClosed
	}

	payload := append([]byte(nil), envelope.Manifest...)
	signature := append([]byte(nil), envelope.Signature...)
	if len(payload) == 0 {
		return nil, fmt.Errorf("%w: empty manifest", ErrInvalidManifest)
	}
	if len(payload) > m.maxManifestBytes {
		return nil, fmt.Errorf("%w: manifest exceeds size limit", ErrInvalidManifest)
	}
	if len(signature) == 0 {
		return nil, fmt.Errorf("%w: empty signature", ErrInvalidSignature)
	}
	// The verifier gets an independent copy so it cannot alter the bytes that
	// admission subsequently parses.
	verifyPayload := append([]byte(nil), payload...)
	if err := m.verifier.Verify(verifyPayload, signature); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidSignature, err)
	}

	manifest, err := parseAdapterManifest(payload, m.maxManifestBytes)
	if err != nil {
		return nil, err
	}
	targets, err := validateAdapterManifest(
		manifest,
		m.base,
		m.policy,
		m.maxRank,
		m.allowed,
		m.maxScaleN,
		m.maxScaleD,
	)
	if err != nil {
		return nil, err
	}
	if m.isRevoked(manifest.AdapterDigest) {
		return nil, fmt.Errorf("%w: %q", ErrRevoked, manifest.AdapterDigest)
	}

	generation, err := m.buildGeneration(manifest, targets)
	if err != nil {
		return nil, err
	}
	return m.publish(generation)
}

func (m *Manager) buildGeneration(manifest AdapterManifest, targets []validatedTarget) (*Generation, error) {
	sensitive := make([][]byte, 0, len(targets)*2)
	success := false
	defer func() {
		if !success {
			for _, data := range sensitive {
				zeroBytes(data)
			}
		}
	}()

	layers := make(map[string]*LayerAdapter, len(targets))
	ordered := make([]*LayerAdapter, 0, len(targets))
	for _, target := range targets {
		aShape := TensorShape{Rows: target.spec.Rank, Cols: target.base.InputDim}
		bShape := TensorShape{Rows: target.base.OutputDim, Cols: target.spec.Rank}

		a, err := readAndVerifyTensor(
			m.tensors,
			target.spec.ADigest,
			aShape,
			target.spec.DType,
			m.maxTensorBytes,
		)
		if err != nil {
			return nil, layerError(target.spec.LayerID, fmt.Errorf("A: %w", err))
		}
		sensitive = append(sensitive, a)

		b, err := readAndVerifyTensor(
			m.tensors,
			target.spec.BDigest,
			bShape,
			target.spec.DType,
			m.maxTensorBytes,
		)
		if err != nil {
			return nil, layerError(target.spec.LayerID, fmt.Errorf("B: %w", err))
		}
		sensitive = append(sensitive, b)

		layer := &LayerAdapter{
			layerID: target.spec.LayerID,
			rank:    target.spec.Rank,
			scale:   target.spec.Scale,
			dtype:   target.spec.DType,
			aDigest: target.spec.ADigest,
			bDigest: target.spec.BDigest,
			aShape:  aShape,
			bShape:  bShape,
			a:       a,
			b:       b,
		}
		layers[layer.layerID] = layer
		ordered = append(ordered, layer)
	}

	generation := &Generation{
		adapterDigest:   manifest.AdapterDigest,
		baseModelDigest: manifest.BaseModelDigest,
		version:         manifest.Version,
		layers:          layers,
		ordered:         ordered,
	}
	generation.refs.Store(1)
	success = true
	return generation, nil
}

func (m *Manager) publish(generation *Generation) (*Generation, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		generation.release()
		return nil, ErrClosed
	}
	if _, revoked := m.revoked[generation.adapterDigest]; revoked {
		m.mu.Unlock()
		generation.release()
		return nil, fmt.Errorf("%w: %q", ErrRevoked, generation.adapterDigest)
	}

	m.nextID++
	generation.id = m.nextID
	old := m.current
	m.current = generation
	if m.publishHook != nil {
		generation.retain()
	}
	m.mu.Unlock()

	if old != nil {
		defer old.release()
	}
	if m.publishHook != nil {
		defer generation.release()
		m.publishHook(generation)
	}
	return generation, nil
}

// Acquire pins the current generation for a new request. A nil generation
// after revocation is reported as ErrNoGeneration.
func (m *Manager) Acquire() (*Lease, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.closed {
		return nil, ErrClosed
	}
	if m.current == nil || !m.current.retain() {
		return nil, ErrNoGeneration
	}
	return &Lease{generation: m.current}, nil
}

// WithGeneration runs fn with a pinned current generation and releases the
// request reference when fn returns. fn is never called without a complete,
// verified generation.
func (m *Manager) WithGeneration(fn func(*Generation) error) error {
	if fn == nil {
		return fmt.Errorf("%w: nil callback", ErrInvalidConfig)
	}
	lease, err := m.Acquire()
	if err != nil {
		return err
	}
	defer lease.Release()
	return fn(lease.Generation())
}

// Revoke permanently blocks new admissions and requests for adapterDigest. It
// reports whether the digest changed from allowed to revoked. In-flight leases
// continue to pin their old generation until released.
func (m *Manager) Revoke(adapterDigest Digest) bool {
	if adapterDigest == "" {
		return false
	}

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return false
	}
	_, alreadyRevoked := m.revoked[adapterDigest]
	m.revoked[adapterDigest] = struct{}{}
	var old *Generation
	if m.current != nil && m.current.adapterDigest == adapterDigest {
		old = m.current
		m.current = nil
	}
	m.mu.Unlock()

	if old != nil {
		old.release()
	}
	return !alreadyRevoked
}

// IsRevoked reports whether adapterDigest has been permanently revoked.
func (m *Manager) IsRevoked(adapterDigest Digest) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.revoked[adapterDigest]
	return ok
}

// Current returns metadata for the published generation, if any.
func (m *Manager) Current() (CurrentInfo, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.current == nil {
		return CurrentInfo{}, false
	}
	return CurrentInfo{
		ID:              m.current.id,
		AdapterDigest:   m.current.adapterDigest,
		BaseModelDigest: m.current.baseModelDigest,
		Version:         m.current.version,
	}, true
}

// Close rejects future loads and requests. Existing leases remain valid until
// released.
func (m *Manager) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	old := m.current
	m.current = nil
	m.mu.Unlock()

	if old != nil {
		old.release()
	}
	return nil
}

func (m *Manager) isRevoked(adapterDigest Digest) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.revoked[adapterDigest]
	return ok
}
