package netsec

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// Classification is the confidentiality partition of a dictionary.
type Classification string

// TenantID is the identity key for a dictionary partition.
type TenantID = string

const (
	// Public dictionaries may be reused by public messages from one tenant.
	Public Classification = "public"
	// Confidential dictionaries may be reused only by confidential messages
	// from one tenant.
	Confidential Classification = "confidential"
)

// Label and Class are aliases retained for callers that prefer those names.
type Label = Classification
type Class = Classification

const (
	ClassPublic          = Public
	ClassConfidential    = Confidential
	LabelPublic          = Public
	LabelConfidential    = Confidential
	PublicLabel          = Public
	ConfidentialLabel    = Confidential
	PublicClassification = Public
	SecretClassification = Confidential
)

var (
	// ErrInvalidTenant reports an empty tenant identifier.
	ErrInvalidTenant = errors.New("netsec: tenant ID is required")
	// ErrInvalidClassification reports a classification other than public or
	// confidential.
	ErrInvalidClassification = errors.New("netsec: invalid classification")
	// ErrStreamClosed reports an operation on a stream that has already been
	// submitted, cancelled, or reset.
	ErrStreamClosed = errors.New("netsec: stream is closed")
	// ErrStreamCancelled reports a stream whose context was cancelled.
	ErrStreamCancelled = errors.New("netsec: stream cancelled")
	// ErrStaleDictionary reports an attempt to publish a stream after its
	// tenant partition was reset or released.
	ErrStaleDictionary = errors.New("netsec: dictionary partition is stale")
	// ErrNilResult reports a nil compression result.
	ErrNilResult = errors.New("netsec: nil compression result")
)

// Scope identifies the only streams that may share a dictionary.
type Scope struct {
	TenantID string
	Class    Classification
}

// NewScope validates and constructs a dictionary scope.
func NewScope(tenantID string, class Classification) (Scope, error) {
	scope := Scope{TenantID: tenantID, Class: class}
	if err := scope.validate(); err != nil {
		return Scope{}, err
	}
	return scope, nil
}

func (s Scope) validate() error {
	if s.TenantID == "" {
		return ErrInvalidTenant
	}
	switch s.Class {
	case Public, Confidential:
		return nil
	default:
		return fmt.Errorf("%w %q", ErrInvalidClassification, s.Class)
	}
}

func (s Scope) String() string {
	return s.TenantID + "/" + string(s.Class)
}

// Result is a successfully submitted compressed stream. Dictionary is the
// exact history snapshot against which Data was encoded, so decoding does not
// depend on later dictionary changes.
type Result struct {
	Data       []byte
	Dictionary []byte
	Scope      Scope
	// Revision is the partition revision visible when the stream started.
	Revision uint64
}

// Decode decodes a submitted result using its immutable dictionary snapshot.
func (r *Result) Decode() ([]byte, error) {
	return r.DecodeContext(context.Background())
}

// DecodeContext is Decode with cancellation support.
func (r *Result) DecodeContext(ctx context.Context) ([]byte, error) {
	if r == nil {
		return nil, ErrNilResult
	}
	return DecompressContext(ctx, r.Dictionary, r.Data)
}

type partition struct {
	mu         sync.Mutex
	dictionary []byte
	revision   uint64
	active     bool
}

// Service owns tenant/classification dictionary partitions. It supports zero
// value use.
type Service struct {
	mu         sync.Mutex
	partitions map[Scope]*partition
}

// Compressor is an alias for Service.
type Compressor = Service

// NewService creates an empty multi-tenant compression service.
func NewService() *Service {
	return &Service{partitions: make(map[Scope]*partition)}
}

// New is an alias for NewService.
func New() *Service {
	return NewService()
}

// NewCompressor is an alias for NewService.
func NewCompressor() *Service {
	return NewService()
}

// CompressResult starts a stream, compresses data, and publishes the stream's
// dictionary only when submission succeeds.
func (s *Service) CompressResult(tenantID string, class Classification, data []byte) (*Result, error) {
	return s.CompressResultContext(context.Background(), tenantID, class, data)
}

// CompressResultContext is CompressResult with cancellation support.
func (s *Service) CompressResultContext(ctx context.Context, tenantID string, class Classification, data []byte) (*Result, error) {
	stream, err := s.NewStream(ctx, tenantID, class)
	if err != nil {
		return nil, err
	}
	if _, err := stream.Write(data); err != nil {
		stream.Cancel()
		return nil, err
	}
	result, err := stream.Submit()
	if err != nil {
		stream.Cancel()
		return nil, err
	}
	return result, nil
}

// Compress returns the compressed payload. To decode it later, use
// CompressResult and Result.Decode so the exact dictionary snapshot is
// retained. Compress is suitable for transport paths that maintain their own
// revision association.
func (s *Service) Compress(tenantID string, class Classification, data []byte) ([]byte, error) {
	return s.CompressContext(context.Background(), tenantID, class, data)
}

// CompressContext is Compress with cancellation support.
func (s *Service) CompressContext(ctx context.Context, tenantID string, class Classification, data []byte) ([]byte, error) {
	result, err := s.CompressResultContext(ctx, tenantID, class, data)
	if err != nil {
		return nil, err
	}
	return result.Data, nil
}

// DecompressResult decodes a submitted result with its own dictionary
// snapshot.
func (s *Service) DecompressResult(ctx context.Context, result *Result) ([]byte, error) {
	if result == nil {
		return nil, ErrNilResult
	}
	return result.DecodeContext(ctx)
}

// Decompress decodes encoded using the partition's current committed
// dictionary. It is intended for sequential protocols that decode at the same
// revision at which the payload was produced. Concurrent or delayed decoding
// should use DecompressResult.
func (s *Service) Decompress(tenantID string, class Classification, encoded []byte) ([]byte, error) {
	return s.DecompressContext(context.Background(), tenantID, class, encoded)
}

// DecompressContext is Decompress with cancellation support.
func (s *Service) DecompressContext(ctx context.Context, tenantID string, class Classification, encoded []byte) ([]byte, error) {
	dictionary, _, err := s.Dictionary(tenantID, class)
	if err != nil {
		return nil, err
	}
	return DecompressContext(ctx, dictionary, encoded)
}

// Decode is an alias for Decompress.
func (s *Service) Decode(tenantID string, class Classification, encoded []byte) ([]byte, error) {
	return s.Decompress(tenantID, class, encoded)
}

// Dictionary returns a copy of the currently committed dictionary and its
// revision.
func (s *Service) Dictionary(tenantID string, class Classification) ([]byte, uint64, error) {
	scope, err := NewScope(tenantID, class)
	if err != nil {
		return nil, 0, err
	}

	s.mu.Lock()
	p := s.partitions[scope]
	s.mu.Unlock()
	if p == nil {
		return nil, 0, nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.active {
		return nil, 0, ErrStaleDictionary
	}
	return append([]byte(nil), p.dictionary...), p.revision, nil
}

// DictionarySnapshot is an alias for Dictionary.
func (s *Service) DictionarySnapshot(tenantID string, class Classification) ([]byte, uint64, error) {
	return s.Dictionary(tenantID, class)
}

// ResetScope atomically retires a partition and clears its dictionary. Streams
// that started before the reset cannot publish into the replacement partition.
func (s *Service) ResetScope(tenantID string, class Classification) error {
	scope, err := NewScope(tenantID, class)
	if err != nil {
		return err
	}
	s.resetScopes(func(candidate Scope) bool { return candidate == scope })
	return nil
}

// Reset retires the partition identified by scope.
func (s *Service) Reset(scope Scope) error {
	if err := scope.validate(); err != nil {
		return err
	}
	s.resetScopes(func(candidate Scope) bool { return candidate == scope })
	return nil
}

// ReleaseTenant retires every public and confidential partition for tenantID.
// A later stream with the same textual tenant ID starts with an empty
// dictionary and cannot publish a stream created under the old tenant lease.
func (s *Service) ReleaseTenant(tenantID string) error {
	if tenantID == "" {
		return ErrInvalidTenant
	}
	s.resetScopes(func(candidate Scope) bool { return candidate.TenantID == tenantID })
	return nil
}

// ResetTenant is an alias for ReleaseTenant.
func (s *Service) ResetTenant(tenantID string) error {
	return s.ReleaseTenant(tenantID)
}

// Clear retires all partitions.
func (s *Service) Clear() {
	s.resetScopes(func(Scope) bool { return true })
}

func (s *Service) resetScopes(match func(Scope) bool) {
	s.mu.Lock()
	var retired []*partition
	for scope, p := range s.partitions {
		if match(scope) {
			delete(s.partitions, scope)
			retired = append(retired, p)
		}
	}
	s.mu.Unlock()

	for _, p := range retired {
		p.mu.Lock()
		p.active = false
		clear(p.dictionary)
		p.dictionary = nil
		p.revision++
		p.mu.Unlock()
	}
}

// NewStream snapshots the committed dictionary visible at this instant. The
// snapshot is private to the stream; no lock is held while compression runs.
func (s *Service) NewStream(ctx context.Context, tenantID string, class Classification) (*Stream, error) {
	scope, err := NewScope(tenantID, class)
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	p := s.partition(scope)
	p.mu.Lock()
	if !p.active {
		p.mu.Unlock()
		return nil, ErrStaleDictionary
	}
	dictionary := append([]byte(nil), p.dictionary...)
	revision := p.revision
	p.mu.Unlock()

	streamCtx, cancel := context.WithCancel(ctx)
	return &Stream{
		service:   s,
		ctx:       streamCtx,
		cancel:    cancel,
		scope:     scope,
		partition: p,
		revision:  revision,
		snapshot:  append([]byte(nil), dictionary...),
	}, nil
}

func (s *Service) partition(scope Scope) *partition {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.partitions == nil {
		s.partitions = make(map[Scope]*partition)
	}
	if p := s.partitions[scope]; p != nil {
		return p
	}
	p := &partition{active: true}
	s.partitions[scope] = p
	return p
}

type streamState uint8

const (
	streamActive streamState = iota
	streamSubmitting
	streamSubmitted
	streamCancelled
	streamFailed
)

// Stream is an isolated compression stream. It buffers input privately and
// never mutates shared partition state while encoding. Submit is the commit
// point; before Submit returns, other streams cannot observe this stream's
// bytes. A successful Submit publishes the stream's last 4096 input bytes for
// later streams in the same Scope.
type Stream struct {
	mu sync.Mutex

	service   *Service
	ctx       context.Context
	cancel    context.CancelFunc
	scope     Scope
	partition *partition
	revision  uint64

	input    []byte
	snapshot []byte

	state streamState
	err   error
}

// Scope returns the stream's dictionary scope.
func (s *Stream) Scope() Scope {
	return s.scope
}

// Revision returns the committed partition revision visible at stream start.
func (s *Stream) Revision() uint64 {
	return s.revision
}

// Dictionary returns a copy of the dictionary snapshot used by this stream.
func (s *Stream) Dictionary() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.snapshot...)
}

// Write buffers p for this stream. Encoding happens at Submit so token choices
// do not depend on Write chunk boundaries. The Stream mutex serializes
// concurrent Write calls, not the service or other streams.
func (s *Stream) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.activeErrorLocked(); err != nil {
		return 0, err
	}
	if err := s.ctx.Err(); err != nil {
		s.failLocked(err)
		return 0, err
	}
	if len(p) == 0 {
		return 0, nil
	}

	s.input = append(s.input, p...)
	return len(p), nil
}

// Submit finishes the stream and atomically publishes its input tail in the
// stream's partition. The returned Result retains the exact dictionary
// snapshot needed to decode Data.
func (s *Stream) Submit() (*Result, error) {
	s.mu.Lock()
	if s.state != streamActive {
		err := s.activeErrorLocked()
		s.mu.Unlock()
		return nil, err
	}
	if err := s.ctx.Err(); err != nil {
		s.failLocked(err)
		s.mu.Unlock()
		return nil, err
	}
	s.state = streamSubmitting
	input := s.input
	snapshot := s.snapshot
	s.mu.Unlock()

	// Check before doing work, then check again at the commit point. A reset
	// after encoding prevents publication.
	s.partition.mu.Lock()
	active := s.partition.active
	s.partition.mu.Unlock()
	if !active {
		s.mu.Lock()
		err := s.abortSubmittingLocked(ErrStaleDictionary)
		s.mu.Unlock()
		return nil, err
	}

	encoded, err := compressContext(s.ctx, snapshot, input)
	if err != nil {
		s.mu.Lock()
		err = s.abortSubmittingLocked(err)
		s.mu.Unlock()
		return nil, err
	}
	if err := s.ctx.Err(); err != nil {
		wipe(encoded)
		s.mu.Lock()
		err = s.abortSubmittingLocked(err)
		s.mu.Unlock()
		return nil, err
	}

	s.mu.Lock()
	if s.state != streamSubmitting {
		err := s.activeErrorLocked()
		wipe(encoded)
		err = s.abortSubmittingLocked(err)
		s.mu.Unlock()
		return nil, err
	}

	s.partition.mu.Lock()
	if !s.partition.active {
		s.partition.mu.Unlock()
		wipe(encoded)
		err := s.abortSubmittingLocked(ErrStaleDictionary)
		s.mu.Unlock()
		return nil, err
	}
	s.partition.dictionary = appendWindow(s.partition.dictionary, tailWindow(input, WindowSize))
	s.partition.revision++
	s.partition.mu.Unlock()

	s.state = streamSubmitted
	result := &Result{
		Data:       encoded,
		Dictionary: s.snapshot,
		Scope:      s.scope,
		Revision:   s.revision,
	}
	s.snapshot = nil
	s.input = nil
	s.stopContextLocked()
	s.mu.Unlock()
	return result, nil
}

// Finish is an alias for Submit.
func (s *Stream) Finish() (*Result, error) {
	return s.Submit()
}

// Close commits the stream like Submit. It implements io.Closer.
func (s *Stream) Close() error {
	s.mu.Lock()
	state := s.state
	s.mu.Unlock()
	switch state {
	case streamSubmitted:
		return nil
	case streamActive:
		_, err := s.Submit()
		return err
	case streamCancelled:
		return ErrStreamCancelled
	default:
		return ErrStreamClosed
	}
}

// Cancel aborts the stream and wipes all uncommitted input and output state.
// Cancelling an already-closed stream is a no-op.
func (s *Stream) Cancel() {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch s.state {
	case streamActive:
		s.cancelLocked(ErrStreamCancelled)
	case streamSubmitting:
		// Submit is reading input/snapshot without the stream mutex. Mark the
		// stream cancelled, abort its context, and let Submit discard the
		// encoded result at its final checkpoint.
		s.state = streamCancelled
		s.err = ErrStreamCancelled
		s.stopContextLocked()
	}
}

// Reset aborts the stream and discards its working dictionary. It cannot be
// used to continue a partially compressed message.
func (s *Stream) Reset() {
	s.Cancel()
}

// Err returns the terminal stream error, if any.
func (s *Stream) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *Stream) activeErrorLocked() error {
	switch s.state {
	case streamActive:
		return nil
	case streamCancelled:
		if s.err != nil {
			return s.err
		}
		return ErrStreamCancelled
	default:
		if s.err != nil {
			return s.err
		}
		return ErrStreamClosed
	}
}

func (s *Stream) failLocked(err error) {
	if s.state != streamActive {
		return
	}
	s.state = streamFailed
	s.err = err
	wipe(s.input)
	wipe(s.snapshot)
	s.input = nil
	s.snapshot = nil
	s.stopContextLocked()
}

func (s *Stream) abortSubmittingLocked(err error) error {
	switch s.state {
	case streamSubmitting:
		s.state = streamFailed
		s.err = err
	case streamCancelled:
		if s.err != nil {
			err = s.err
		} else {
			err = ErrStreamCancelled
		}
	case streamFailed:
		if s.err != nil {
			err = s.err
		}
	}
	s.releaseBuffersLocked()
	s.stopContextLocked()
	return err
}

func (s *Stream) cancelLocked(err error) {
	s.state = streamCancelled
	if err == nil {
		err = ErrStreamCancelled
	}
	s.err = err
	wipe(s.input)
	wipe(s.snapshot)
	s.input = nil
	s.snapshot = nil
	s.stopContextLocked()
}

func (s *Stream) releaseBuffersLocked() {
	wipe(s.input)
	wipe(s.snapshot)
	s.input = nil
	s.snapshot = nil
}

func (s *Stream) stopContextLocked() {
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
}

func appendWindow(window, data []byte) []byte {
	if len(data) == 0 {
		return window
	}
	if len(data) >= WindowSize {
		out := make([]byte, WindowSize)
		copy(out, data[len(data)-WindowSize:])
		return out
	}
	if len(window)+len(data) <= WindowSize {
		return append(window, data...)
	}
	keep := WindowSize - len(data)
	out := make([]byte, 0, WindowSize)
	out = append(out, window[len(window)-keep:]...)
	out = append(out, data...)
	return out
}

func wipe(data []byte) {
	clear(data)
}
