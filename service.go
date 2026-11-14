package netsec

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// SubmitRequest is the trusted result of request parsing. The authenticated
// submitter is passed separately to Submit and is never read from Attributes.
// ID is optional; when empty, the service generates a random ID.
type SubmitRequest struct {
	ID           string
	Queue        string
	DocumentName string
	Content      []byte
	Attributes   map[string]string
}

// PrintResult is returned only after authorization, binding validation and a
// full SHA-256 verification of the stored bytes.
type PrintResult struct {
	Job        Job
	Document   []byte
	Digest     string
	Attributes Attributes
}

// Option configures a Service.
type Option func(*Service)

// WithMaxDocumentBytes overrides the per-job document size limit.
func WithMaxDocumentBytes(max int64) Option {
	return func(s *Service) {
		if max > 0 {
			s.maxDocumentBytes = max
		}
	}
}

// WithClock overrides the clock used for audit timestamps.
func WithClock(now func() time.Time) Option {
	return func(s *Service) {
		if now != nil {
			s.now = now
		}
	}
}

// Service binds jobs to their original submission context and serves print,
// cancel and reprint operations. All security decisions use the stored record;
// request attributes are parsed only through the whitelist and may never
// replace the record's queue, submitter, URI or digest.
type Service struct {
	mu               sync.Mutex
	jobs             map[string]*Job
	store            Storage
	maxDocumentBytes int64
	now              func() time.Time
}

// NewService creates a print job service over store. A nil store is rejected:
// the component fails closed rather than silently printing from nowhere.
func NewService(store Storage, opts ...Option) (*Service, error) {
	if store == nil {
		return nil, fmt.Errorf("netsec: storage is required")
	}
	s := &Service{
		jobs:             make(map[string]*Job),
		store:            store,
		maxDocumentBytes: DefaultMaxDocumentBytes,
		now:              func() time.Time { return time.Now().UTC() },
	}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	if s.maxDocumentBytes <= 0 {
		s.maxDocumentBytes = DefaultMaxDocumentBytes
	}
	return s, nil
}

// NewFileService is a convenience constructor for a filesystem-backed service.
func NewFileService(root string, opts ...Option) (*Service, error) {
	store, err := NewFileStorage(root)
	if err != nil {
		return nil, err
	}
	return NewService(store, opts...)
}

// Submit creates a job whose submitter is the authenticated actor. The queue,
// document name, URI and digest are fixed at submission time. If request
// attributes contain a binding field, it must already equal the server-derived
// value; an attempt to point at another user or queue is rejected.
func (s *Service) Submit(ctx context.Context, submitter string, req SubmitRequest) (Job, error) {
	if err := contextErr(ctx); err != nil {
		return Job{}, err
	}
	if err := validateToken("submitter", submitter); err != nil {
		return Job{}, fmt.Errorf("%w: invalid authenticated submitter", ErrForbidden)
	}
	if err := validateToken("queue", req.Queue); err != nil {
		return Job{}, err
	}
	if err := validateDocumentName(req.DocumentName); err != nil {
		return Job{}, err
	}
	if int64(len(req.Content)) > s.maxDocumentBytes {
		return Job{}, fmt.Errorf("%w: %d bytes", ErrDocumentTooLarge, len(req.Content))
	}
	if req.ID != "" {
		if err := validateToken("job-id", req.ID); err != nil {
			return Job{}, err
		}
	}

	attributes, err := ParseAttributes(req.Attributes)
	if err != nil {
		return Job{}, err
	}
	content := append([]byte(nil), req.Content...)
	sum := sha256.Sum256(content)
	digest := hex.EncodeToString(sum[:])

	s.mu.Lock()
	defer s.mu.Unlock()

	id, err := s.allocateIDLocked(req.ID)
	if err != nil {
		return Job{}, err
	}
	job, err := BindJob(JobSpec{
		ID:           id,
		Queue:        req.Queue,
		Submitter:    submitter,
		DocumentName: req.DocumentName,
		Digest:       digest,
		Attributes:   attributes,
	})
	if err != nil {
		return Job{}, err
	}

	if err := s.store.WriteJob(ctx, job.Scope(), job.DocumentName, content); err != nil {
		return Job{}, err
	}
	now := s.now()
	job.CreatedAt = now
	job.UpdatedAt = now
	s.jobs[job.ID] = &job
	return job.clone(), nil
}

// Print authorizes the actor against the original submitter, validates any
// caller-supplied binding attributes, reads only the stored job's document,
// and verifies its SHA-256 digest before returning bytes to the print pipeline.
// Caller-supplied URIs are never used as storage keys.
func (s *Service) Print(ctx context.Context, actor, jobID string, rawAttributes map[string]string) (PrintResult, error) {
	attributes, err := ParseAttributes(rawAttributes)
	if err != nil {
		return PrintResult{}, err
	}
	return s.print(ctx, actor, jobID, attributes)
}

// PrintByAttributes is the attribute-oriented entry point for a print queue.
// It requires a job-id or a job URI to select the original job, applies the
// same whitelist, and then performs the normal binding check and isolated
// read. A supplied URI is only compared with the stored binding; it is never
// used as the storage key.
func (s *Service) PrintByAttributes(ctx context.Context, actor string, rawAttributes map[string]string) (PrintResult, error) {
	attributes, err := ParseAttributes(rawAttributes)
	if err != nil {
		return PrintResult{}, err
	}
	jobID, err := jobIDFromAttributes(attributes)
	if err != nil {
		return PrintResult{}, err
	}
	return s.print(ctx, actor, jobID, attributes)
}

func (s *Service) print(ctx context.Context, actor, jobID string, attributes Attributes) (PrintResult, error) {
	if err := contextErr(ctx); err != nil {
		return PrintResult{}, err
	}
	if err := validateToken("submitter", actor); err != nil {
		return PrintResult{}, ErrForbidden
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	job, err := s.lookupLocked(jobID)
	if err != nil {
		return PrintResult{}, err
	}
	if err := authorize(actor, job); err != nil {
		return PrintResult{}, err
	}
	switch job.Status {
	case StatusCancelled:
		return PrintResult{}, ErrJobCancelled
	case StatusCompleted:
		return PrintResult{}, ErrJobCompleted
	}
	if err := job.Verify(); err != nil {
		return PrintResult{}, err
	}
	if err := job.CheckAttributes(attributes); err != nil {
		return PrintResult{}, err
	}

	content, err := s.store.ReadJob(ctx, job.Scope(), job.DocumentName)
	if err != nil {
		return PrintResult{}, err
	}
	if int64(len(content)) > s.maxDocumentBytes {
		return PrintResult{}, fmt.Errorf("%w: %d bytes", ErrDocumentTooLarge, len(content))
	}
	gotSum := sha256.Sum256(content)
	gotDigest := hex.EncodeToString(gotSum[:])
	if gotDigest != job.Digest {
		return PrintResult{}, fmt.Errorf("%w: job %s", ErrDigestMismatch, job.ID)
	}

	effective := job.Attributes.Merge(attributes)
	job.Status = StatusCompleted
	job.UpdatedAt = s.now()
	return PrintResult{
		Job:        job.clone(),
		Document:   content,
		Digest:     gotDigest,
		Attributes: effective,
	}, nil
}

// Cancel cancels a job only when the actor is the original submitter.
// Cancellation is idempotent for that actor and does not read or print any
// document.
func (s *Service) Cancel(ctx context.Context, actor, jobID string) (Job, error) {
	return s.cancel(ctx, actor, jobID, nil)
}

// CancelWithAttributes also verifies caller-supplied attributes against the
// original job binding before cancelling. This rejects URI replacement or
// cross-queue references on cancel endpoints.
func (s *Service) CancelWithAttributes(ctx context.Context, actor, jobID string, rawAttributes map[string]string) (Job, error) {
	attributes, err := ParseAttributes(rawAttributes)
	if err != nil {
		return Job{}, err
	}
	return s.cancel(ctx, actor, jobID, &attributes)
}

// CancelByAttributes selects the original job from job-id or job-uri
// attributes, verifies the rest of the binding, and then applies cancel
// authorization against that original record.
func (s *Service) CancelByAttributes(ctx context.Context, actor string, rawAttributes map[string]string) (Job, error) {
	attributes, err := ParseAttributes(rawAttributes)
	if err != nil {
		return Job{}, err
	}
	jobID, err := jobIDFromAttributes(attributes)
	if err != nil {
		return Job{}, err
	}
	return s.cancel(ctx, actor, jobID, &attributes)
}

func (s *Service) cancel(ctx context.Context, actor, jobID string, attributes *Attributes) (Job, error) {
	if err := contextErr(ctx); err != nil {
		return Job{}, err
	}
	if err := validateToken("submitter", actor); err != nil {
		return Job{}, ErrForbidden
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	job, err := s.lookupLocked(jobID)
	if err != nil {
		return Job{}, err
	}
	if err := authorize(actor, job); err != nil {
		return Job{}, err
	}
	if err := job.Verify(); err != nil {
		return Job{}, err
	}
	if attributes != nil {
		if err := job.CheckAttributes(*attributes); err != nil {
			return Job{}, err
		}
	}
	if job.Status != StatusCancelled {
		job.Status = StatusCancelled
		job.UpdatedAt = s.now()
	}
	return job.clone(), nil
}

// Reprint opens a completed job for printing again without allowing any new
// binding. Only the original submitter may reprint, and a cancelled job cannot
// be revived.
func (s *Service) Reprint(ctx context.Context, actor, jobID string) (Job, error) {
	return s.reprint(ctx, actor, jobID, nil)
}

// ReprintWithAttributes rejects any binding replacement attempt before
// reprinting.
func (s *Service) ReprintWithAttributes(ctx context.Context, actor, jobID string, rawAttributes map[string]string) (Job, error) {
	attributes, err := ParseAttributes(rawAttributes)
	if err != nil {
		return Job{}, err
	}
	return s.reprint(ctx, actor, jobID, &attributes)
}

// ReprintByAttributes selects the original job from job-id or job-uri
// attributes, verifies the remaining binding, and applies reprint
// authorization against that original record.
func (s *Service) ReprintByAttributes(ctx context.Context, actor string, rawAttributes map[string]string) (Job, error) {
	attributes, err := ParseAttributes(rawAttributes)
	if err != nil {
		return Job{}, err
	}
	jobID, err := jobIDFromAttributes(attributes)
	if err != nil {
		return Job{}, err
	}
	return s.reprint(ctx, actor, jobID, &attributes)
}

func (s *Service) reprint(ctx context.Context, actor, jobID string, attributes *Attributes) (Job, error) {
	if err := contextErr(ctx); err != nil {
		return Job{}, err
	}
	if err := validateToken("submitter", actor); err != nil {
		return Job{}, ErrForbidden
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	job, err := s.lookupLocked(jobID)
	if err != nil {
		return Job{}, err
	}
	if err := authorize(actor, job); err != nil {
		return Job{}, err
	}
	if job.Status == StatusCancelled {
		return Job{}, ErrJobCancelled
	}
	if err := job.Verify(); err != nil {
		return Job{}, err
	}
	if attributes != nil {
		if err := job.CheckAttributes(*attributes); err != nil {
			return Job{}, err
		}
	}

	// A reprint always uses the original queue, submitter, document URI and
	// digest. It only moves the lifecycle state back to pending so Print can
	// authorize and verify the same record again.
	job.Status = StatusPending
	job.UpdatedAt = s.now()
	return job.clone(), nil
}

// Get returns a copy of an authorized job record. Unauthorized actors learn
// nothing beyond whether the job exists.
func (s *Service) Get(ctx context.Context, actor, jobID string) (Job, error) {
	if err := contextErr(ctx); err != nil {
		return Job{}, err
	}
	if err := validateToken("submitter", actor); err != nil {
		return Job{}, ErrForbidden
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	job, err := s.lookupLocked(jobID)
	if err != nil {
		return Job{}, err
	}
	if err := authorize(actor, job); err != nil {
		return Job{}, err
	}
	if err := job.Verify(); err != nil {
		return Job{}, err
	}
	return job.clone(), nil
}

func (s *Service) lookupLocked(jobID string) (*Job, error) {
	job, ok := s.jobs[jobID]
	if !ok {
		return nil, ErrJobNotFound
	}
	return job, nil
}

func (s *Service) allocateIDLocked(requested string) (string, error) {
	if requested != "" {
		if _, exists := s.jobs[requested]; exists {
			return "", fmt.Errorf("%w: %s", ErrJobExists, requested)
		}
		return requested, nil
	}
	for i := 0; i < 32; i++ {
		var raw [16]byte
		if _, err := rand.Read(raw[:]); err != nil {
			return "", fmt.Errorf("netsec: generate job ID: %w", err)
		}
		id := "job-" + hex.EncodeToString(raw[:])
		if _, exists := s.jobs[id]; !exists {
			return id, nil
		}
	}
	return "", fmt.Errorf("netsec: unable to allocate a unique job ID")
}

func jobIDFromAttributes(attributes Attributes) (string, error) {
	if jobID, ok := attributes.Get(AttrJobID); ok {
		return jobID, nil
	}
	for _, key := range []string{AttrJobURI, AttrDocumentURI} {
		raw, ok := attributes.Get(key)
		if !ok {
			continue
		}
		scope, _, err := ParseJobURI(raw)
		if err != nil {
			return "", err
		}
		return scope.JobID, nil
	}
	return "", fmt.Errorf("%w: job-id or job-uri is required", ErrInvalidAttribute)
}

func authorize(actor string, job *Job) error {
	if job == nil || actor != job.Submitter {
		return ErrForbidden
	}
	return nil
}
