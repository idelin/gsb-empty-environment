package netsec

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
)

type handleRecord struct {
	generation uint64
	active     bool
	unknown    bool
	session    *Session
}

type savedRecord struct {
	fingerprint [sha256.Size]byte
	binding     Binding
}

// Authority owns the numeric-handle generation registry and TPM startup
// binding. A context save/load or reset always allocates a new generation even
// when the numeric handle is unchanged.
type Authority struct {
	transport Transport

	// lifecycle serializes reset/context operations with the policy and final
	// command sequence. Multiple sessions may authorize concurrently; a reset or
	// context load cannot slip between PolicyCpHash and the protected command.
	lifecycle      sync.RWMutex
	mu             sync.Mutex
	startupCount   uint64
	nextGeneration uint64
	records        map[Handle]*handleRecord
	saved          map[[sha256.Size]byte]savedRecord
}

// NewAuthority constructs a handle-generation registry bound to one TPM
// transport and startup count.
func NewAuthority(transport Transport, startupCount uint64) (*Authority, error) {
	if transport == nil {
		return nil, errors.New("tpm: nil transport")
	}
	return &Authority{
		transport:    transport,
		startupCount: startupCount,
		records:      make(map[Handle]*handleRecord),
		saved:        make(map[[sha256.Size]byte]savedRecord),
	}, nil
}

// NewSession creates a standalone authority for one session. For multiple
// sessions sharing a TPM, create an Authority explicitly so generation and
// startup state are shared.
func NewSession(transport Transport, opts SessionOptions) (*Session, error) {
	a, err := NewAuthority(transport, opts.StartupCount)
	if err != nil {
		return nil, err
	}
	return a.Open(opts)
}

// StartupCount returns the TPM startup count currently trusted by the
// authority.
func (a *Authority) StartupCount() uint64 {
	a.lifecycle.RLock()
	defer a.lifecycle.RUnlock()
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.startupCount
}

// Open allocates a new numeric-handle generation and creates a fresh policy
// session. Reusing a live handle without Flush, ContextSave or ContextLoad
// fails.
func (a *Authority) Open(opts SessionOptions) (*Session, error) {
	opts, err := normalizeSessionOptions(opts)
	if err != nil {
		return nil, err
	}

	a.lifecycle.Lock()
	defer a.lifecycle.Unlock()
	a.mu.Lock()
	defer a.mu.Unlock()
	if opts.StartupCount != 0 && opts.StartupCount != a.startupCount {
		return nil, ErrStartupChanged
	}
	if rec := a.records[opts.Handle]; rec != nil {
		if rec.unknown {
			return nil, ErrSessionUnknown
		}
		if rec.active {
			return nil, ErrHandleInUse
		}
	}
	generation := a.allocateGenerationLocked()
	binding := Binding{
		StartupCount:  a.startupCount,
		SessionHandle: opts.Handle,
		Generation:    generation,
		HashAlg:       opts.HashAlg,
		NonceTPM:      append([]byte(nil), opts.NonceTPM...),
		PolicyDigest:  append([]byte(nil), opts.PolicyDigest...),
		Principal:     opts.Principal,
	}
	session := newSession(a, binding, opts.VerifyResponse, opts.VerifyRecovery)
	a.records[opts.Handle] = &handleRecord{
		generation: generation,
		active:     true,
		session:    session,
	}
	return session, nil
}

func normalizeSessionOptions(opts SessionOptions) (SessionOptions, error) {
	if opts.Handle == 0 {
		return SessionOptions{}, fmt.Errorf("%w: session handle is zero", ErrInvalidBinding)
	}
	if opts.HashAlg == 0 {
		opts.HashAlg = AlgSHA256
	}
	size, err := opts.HashAlg.Size()
	if err != nil {
		return SessionOptions{}, err
	}
	if len(opts.NonceTPM) != size {
		return SessionOptions{}, fmt.Errorf("%w: nonceTPM is %d bytes, want %d", ErrInvalidBinding, len(opts.NonceTPM), size)
	}
	if len(opts.PolicyDigest) == 0 {
		opts.PolicyDigest = make([]byte, size)
	}
	if len(opts.PolicyDigest) != size {
		return SessionOptions{}, fmt.Errorf("%w: policy digest is %d bytes, want %d", ErrInvalidBinding, len(opts.PolicyDigest), size)
	}
	if opts.Principal == "" {
		return SessionOptions{}, fmt.Errorf("%w: caller principal is empty", ErrInvalidBinding)
	}
	return opts, nil
}

func (a *Authority) allocateGenerationLocked() uint64 {
	a.nextGeneration++
	return a.nextGeneration
}

func (a *Authority) invalidateLocked(h Handle) {
	rec := a.records[h]
	if rec == nil {
		rec = &handleRecord{}
		a.records[h] = rec
	}
	rec.active = false
	rec.unknown = false
	rec.generation = a.allocateGenerationLocked()
}

func (a *Authority) check(s *Session) error {
	if s == nil {
		return ErrInvalidBinding
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.startupCount != s.binding.StartupCount {
		return ErrStartupChanged
	}
	rec := a.records[s.binding.SessionHandle]
	if rec == nil || !rec.active || rec.generation != s.binding.Generation || rec.session != s {
		return ErrGeneration
	}
	return nil
}

func (a *Authority) markUnknown(h Handle, generation uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if rec := a.records[h]; rec != nil && rec.generation == generation {
		rec.unknown = true
	}
}

func (a *Authority) clearUnknown(h Handle, generation uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if rec := a.records[h]; rec != nil && rec.generation == generation {
		rec.unknown = false
	}
}

// TPMReset records a TPM reset/startup and invalidates every old generation,
// including sessions whose numeric handle reappears after startup.
func (a *Authority) TPMReset(startupCount uint64) {
	a.lifecycle.Lock()
	defer a.lifecycle.Unlock()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.startupCount = startupCount
	for handle := range a.records {
		a.invalidateLocked(handle)
	}
}

// ObserveStartup invalidates old sessions when the observed startup count
// changes. It returns ErrStartupChanged on a reset.
func (a *Authority) ObserveStartup(startupCount uint64) error {
	a.lifecycle.Lock()
	defer a.lifecycle.Unlock()
	a.mu.Lock()
	defer a.mu.Unlock()
	if startupCount == a.startupCount {
		return nil
	}
	a.startupCount = startupCount
	for handle := range a.records {
		a.invalidateLocked(handle)
	}
	return ErrStartupChanged
}

func (s *Session) snapshotBindingLocked() Binding {
	b := s.binding.clone()
	b.PolicyDigest = append([]byte(nil), s.policyDigest...)
	return b
}

// ContextSave saves an idle session context. The host generation is retired
// immediately; a later ContextLoad creates a new generation with empty
// commitment state.
func (a *Authority) ContextSave(ctx context.Context, s *Session) ([]byte, error) {
	if s == nil || s.authority != a {
		return nil, ErrInvalidBinding
	}
	a.lifecycle.Lock()
	defer a.lifecycle.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.phase == StateUnknown {
		return nil, ErrSessionUnknown
	}
	if s.phase != StateIdle {
		return nil, fmt.Errorf("%w: context save requires an idle session, state is %s", ErrSessionBusy, s.phase)
	}
	if err := a.check(s); err != nil {
		return nil, err
	}

	resp, err := s.executeLocked(ctx, CCContextSave, []Handle{s.binding.SessionHandle}, nil, nil)
	if err != nil {
		return nil, err
	}
	if resp.Code != 0 {
		return nil, &TPMError{Code: resp.Code, Operation: "ContextSave"}
	}
	blob, next, err := readTPM2B(resp.Parameters, 0)
	if err != nil {
		s.phase = StateInvalid
		return nil, err
	}
	if next != len(resp.Parameters) {
		s.phase = StateInvalid
		return nil, fmt.Errorf("%w: trailing ContextSave bytes", ErrInvalidResponse)
	}
	blob = append([]byte(nil), blob...)
	fp := sha256.Sum256(blob)
	binding := s.snapshotBindingLocked()

	a.mu.Lock()
	a.saved[fp] = savedRecord{fingerprint: fp, binding: binding}
	a.invalidateLocked(s.binding.SessionHandle)
	a.mu.Unlock()

	s.phase = StateSaved
	return blob, nil
}

// ContextLoad loads a saved TPM session context as a new generation. If the
// context was saved by this Authority, omitted binding fields are restored
// from the saved record and any supplied mismatch is rejected.
func (a *Authority) ContextLoad(ctx context.Context, blob []byte, opts SessionOptions) (*Session, error) {
	if len(blob) == 0 {
		return nil, errors.New("tpm: empty context blob")
	}
	a.lifecycle.Lock()
	defer a.lifecycle.Unlock()
	fp := sha256.Sum256(blob)

	a.mu.Lock()
	saved, hasSaved := a.saved[fp]
	startupCount := a.startupCount
	a.mu.Unlock()

	var err error
	opts, err = mergeRestoredOptions(opts, saved, hasSaved, startupCount)
	if err != nil {
		return nil, err
	}

	// Do not allow ContextLoad to bypass an unresolved command.
	a.mu.Lock()
	if rec := a.records[opts.Handle]; rec != nil && rec.active && rec.unknown {
		a.mu.Unlock()
		return nil, ErrSessionUnknown
	}
	a.mu.Unlock()

	resp, err := a.execute(ctx, CCContextLoad, nil, appendTPM2B(nil, blob), nil)
	if err != nil {
		return nil, err
	}
	if resp.Code != 0 {
		return nil, &TPMError{Code: resp.Code, Operation: "ContextLoad"}
	}
	loadedValue, err := decodeU32(resp.Parameters)
	if err != nil {
		return nil, err
	}
	loaded := Handle(loadedValue)
	if opts.Handle != 0 && opts.Handle != loaded {
		_ = a.flushHandleLocked(ctx, loaded)
		return nil, fmt.Errorf("%w: loaded %#08x, expected %#08x", ErrHandleMismatch, uint32(loaded), uint32(opts.Handle))
	}
	opts.Handle = loaded

	a.mu.Lock()
	if rec := a.records[loaded]; rec != nil && rec.active && rec.unknown {
		a.mu.Unlock()
		_ = a.flushHandleLocked(ctx, loaded)
		return nil, ErrSessionUnknown
	}
	generation := a.allocateGenerationLocked()
	binding := Binding{
		StartupCount:  a.startupCount,
		SessionHandle: loaded,
		Generation:    generation,
		HashAlg:       opts.HashAlg,
		NonceTPM:      append([]byte(nil), opts.NonceTPM...),
		PolicyDigest:  append([]byte(nil), opts.PolicyDigest...),
		Principal:     opts.Principal,
	}
	session := newSession(a, binding, opts.VerifyResponse, opts.VerifyRecovery)
	a.records[loaded] = &handleRecord{
		generation: generation,
		active:     true,
		session:    session,
	}
	a.mu.Unlock()

	session.mu.Lock()
	actual, verifyErr := session.policyGetDigestLocked(ctx)
	if verifyErr == nil && !equalDigest(actual, opts.PolicyDigest) {
		verifyErr = ErrPolicyDigest
	}
	if verifyErr != nil {
		session.phase = StateInvalid
		session.mu.Unlock()
		_ = a.flushHandleLocked(ctx, loaded)
		return nil, verifyErr
	}
	session.mu.Unlock()
	return session, nil
}

func mergeRestoredOptions(opts SessionOptions, saved savedRecord, hasSaved bool, startupCount uint64) (SessionOptions, error) {
	if !hasSaved && !opts.AllowUnregisteredContext {
		return SessionOptions{}, ErrUnregisteredContext
	}
	if hasSaved && saved.binding.StartupCount != startupCount {
		return SessionOptions{}, ErrStartupChanged
	}
	if opts.HashAlg == 0 {
		if !hasSaved {
			opts.HashAlg = AlgSHA256
		} else {
			opts.HashAlg = saved.binding.HashAlg
		}
	}
	if hasSaved && opts.HashAlg != saved.binding.HashAlg {
		return SessionOptions{}, fmt.Errorf("%w: hash algorithm differs from saved context", ErrBindingMismatch)
	}
	size, err := opts.HashAlg.Size()
	if err != nil {
		return SessionOptions{}, err
	}
	if len(opts.NonceTPM) == 0 && hasSaved {
		opts.NonceTPM = append([]byte(nil), saved.binding.NonceTPM...)
	}
	if hasSaved && !equalDigest(opts.NonceTPM, saved.binding.NonceTPM) {
		return SessionOptions{}, fmt.Errorf("%w: nonceTPM differs from saved context", ErrBindingMismatch)
	}
	if len(opts.NonceTPM) != size {
		return SessionOptions{}, fmt.Errorf("%w: nonceTPM is %d bytes, want %d", ErrInvalidBinding, len(opts.NonceTPM), size)
	}
	if len(opts.PolicyDigest) == 0 && hasSaved {
		opts.PolicyDigest = append([]byte(nil), saved.binding.PolicyDigest...)
	}
	if hasSaved && !equalDigest(opts.PolicyDigest, saved.binding.PolicyDigest) {
		return SessionOptions{}, fmt.Errorf("%w: policy digest differs from saved context", ErrBindingMismatch)
	}
	if len(opts.PolicyDigest) != size {
		return SessionOptions{}, fmt.Errorf("%w: policy digest is %d bytes, want %d", ErrInvalidBinding, len(opts.PolicyDigest), size)
	}
	if opts.Principal == "" && hasSaved {
		opts.Principal = saved.binding.Principal
	}
	if hasSaved && opts.Principal != saved.binding.Principal {
		return SessionOptions{}, fmt.Errorf("%w: principal differs from saved context", ErrBindingMismatch)
	}
	if opts.Principal == "" {
		return SessionOptions{}, fmt.Errorf("%w: caller principal is empty", ErrInvalidBinding)
	}
	if opts.Handle == 0 && hasSaved {
		opts.Handle = saved.binding.SessionHandle
	}
	if opts.Handle == 0 {
		return SessionOptions{}, fmt.Errorf("%w: expected session handle is required", ErrInvalidBinding)
	}
	if hasSaved && opts.Handle != saved.binding.SessionHandle {
		return SessionOptions{}, fmt.Errorf("%w: session handle differs from saved context", ErrBindingMismatch)
	}
	if opts.StartupCount != 0 && opts.StartupCount != startupCount {
		return SessionOptions{}, ErrStartupChanged
	}
	opts.StartupCount = startupCount
	return opts, nil
}

// Flush flushes a session and retires its numeric-handle generation. A session
// with an unknown outcome must be reconciled first.
func (a *Authority) Flush(ctx context.Context, s *Session) error {
	if s == nil || s.authority != a {
		return ErrInvalidBinding
	}
	a.lifecycle.Lock()
	defer a.lifecycle.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.phase == StateUnknown {
		return ErrSessionUnknown
	}
	if err := a.check(s); err != nil {
		return err
	}
	resp, err := s.executeLocked(ctx, CCFlushContext, []Handle{s.binding.SessionHandle}, nil, nil)
	if err != nil {
		return err
	}
	if resp.Code != 0 {
		return &TPMError{Code: resp.Code, Operation: "FlushContext"}
	}
	a.mu.Lock()
	a.invalidateLocked(s.binding.SessionHandle)
	a.mu.Unlock()
	s.phase = StateFlushed
	return nil
}

func (a *Authority) flushHandleLocked(ctx context.Context, h Handle) error {
	resp, err := a.execute(ctx, CCFlushContext, []Handle{h}, nil, nil)
	if err != nil {
		return err
	}
	if resp.Code != 0 {
		return &TPMError{Code: resp.Code, Operation: "FlushContext"}
	}
	a.mu.Lock()
	a.invalidateLocked(h)
	a.mu.Unlock()
	return nil
}

// FlushHandle flushes a handle not currently represented by a live Session.
func (a *Authority) FlushHandle(ctx context.Context, h Handle) error {
	a.lifecycle.Lock()
	defer a.lifecycle.Unlock()
	a.mu.Lock()
	if rec := a.records[h]; rec != nil && rec.active && rec.unknown {
		a.mu.Unlock()
		return ErrSessionUnknown
	}
	a.mu.Unlock()
	return a.flushHandleLocked(ctx, h)
}

func (a *Authority) execute(ctx context.Context, code CommandCode, handles []Handle, parameters []byte, auth []AuthValues) (Response, error) {
	raw, err := MarshalCommand(code, handles, parameters, auth)
	if err != nil {
		return Response{}, err
	}
	respRaw, err := a.transport.Execute(ctx, raw)
	if err != nil {
		return Response{}, err
	}
	return ParseResponse(respRaw)
}
