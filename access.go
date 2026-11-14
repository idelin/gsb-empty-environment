package netsec

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"io"
	"sync"
	"time"
)

const (
	defaultJobIDBytes    = 32
	defaultTTL           = 10 * time.Minute
	defaultMaxTTL        = 24 * time.Hour
	defaultFailureLimit  = 20
	defaultFailureWindow = time.Minute
)

// Subject is the authenticated caller identity supplied by the scan service's
// authentication middleware. It must never be taken from an untrusted request
// parameter.
type Subject struct {
	UserID   string
	DeviceID string
}

func (s Subject) valid() bool {
	return s.UserID != "" && s.DeviceID != ""
}

// JobSpec describes one new scan job. ExpiresAt is the absolute end of the
// job's usable lifetime; a zero value selects the Access default TTL.
type JobSpec struct {
	Subject     Subject
	Image       []byte
	ContentType string
	ExpiresAt   time.Time
}

// JobInfo is non-sensitive metadata about a created job. It intentionally does
// not contain the image bytes.
type JobInfo struct {
	ID          string
	UserID      string
	DeviceID    string
	CreatedAt   time.Time
	ExpiresAt   time.Time
	ContentType string
	Format      ImageFormat
	Size        int
}

// Scan is a sanitized image returned to its owning subject.
type Scan struct {
	JobID       string
	Subject     Subject
	CreatedAt   time.Time
	ExpiresAt   time.Time
	ContentType string
	Format      ImageFormat
	Data        []byte
}

// Bytes returns a private copy of the image.
func (s *Scan) Bytes() []byte {
	if s == nil {
		return nil
	}
	return append([]byte(nil), s.Data...)
}

type jobRecord struct {
	info JobInfo
}

type failureState struct {
	windowStart  time.Time
	count        int
	blockedUntil time.Time
}

// Access enforces authenticated ownership and expiry before loading a scan.
// It is safe for concurrent use.
type Access struct {
	store ScanStore

	mu       sync.Mutex
	jobs     map[string]*jobRecord
	failures map[string]*failureState

	now           func() time.Time
	random        io.Reader
	idBytes       int
	defaultTTL    time.Duration
	maxTTL        time.Duration
	failureLimit  int
	failureWindow time.Duration
}

// Option configures an Access instance.
type Option func(*Access)

// WithClock injects a clock, primarily for deterministic expiry tests.
func WithClock(now func() time.Time) Option {
	return func(a *Access) {
		if now != nil {
			a.now = now
		}
	}
}

// WithDefaultTTL sets the lifetime used when JobSpec.ExpiresAt is zero.
func WithDefaultTTL(ttl time.Duration) Option {
	return func(a *Access) {
		if ttl > 0 {
			a.defaultTTL = ttl
		}
	}
}

// WithMaxTTL caps the requested job lifetime.
func WithMaxTTL(ttl time.Duration) Option {
	return func(a *Access) {
		if ttl > 0 {
			a.maxTTL = ttl
		}
	}
}

// WithFailureLimit configures enumeration throttling. After limit failed
// lookups by one authenticated user inside window, further lookups are
// rejected for one window. A non-positive limit disables throttling.
func WithFailureLimit(limit int, window time.Duration) Option {
	return func(a *Access) {
		if window > 0 {
			a.failureWindow = window
		}
		a.failureLimit = limit
	}
}

// WithRateLimit is an alias for WithFailureLimit.
func WithRateLimit(limit int, window time.Duration) Option {
	return WithFailureLimit(limit, window)
}

// WithJobIDBytes changes the random identifier size. Values below 16 bytes are
// raised to 16 to retain adequate unpredictability.
func WithJobIDBytes(size int) Option {
	return func(a *Access) {
		if size < 16 {
			size = 16
		}
		if size > 64 {
			size = 64
		}
		a.idBytes = size
	}
}

// WithRandomSource injects entropy, primarily for deterministic tests. It
// should only be used with a cryptographically secure reader in production.
func WithRandomSource(reader io.Reader) Option {
	return func(a *Access) {
		if reader != nil {
			a.random = reader
		}
	}
}

// WithStore uses a caller-provided image store. CreateJob sanitizes images
// before passing them to the store, and Read re-sanitizes before returning.
func WithStore(store ScanStore) Option {
	return func(a *Access) {
		if store != nil {
			a.store = store
		}
	}
}

func newAccess(opts []Option) *Access {
	a := &Access{
		store:         NewMemoryStore(),
		jobs:          make(map[string]*jobRecord),
		failures:      make(map[string]*failureState),
		now:           func() time.Time { return time.Now().UTC() },
		random:        rand.Reader,
		idBytes:       defaultJobIDBytes,
		defaultTTL:    defaultTTL,
		maxTTL:        defaultMaxTTL,
		failureLimit:  defaultFailureLimit,
		failureWindow: defaultFailureWindow,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(a)
		}
	}
	if a.maxTTL > 0 && a.defaultTTL > a.maxTTL {
		a.defaultTTL = a.maxTTL
	}
	return a
}

// NewAccess creates an Access backed by an in-memory store.
func NewAccess(opts ...Option) *Access {
	return newAccess(opts)
}

// New is an alias for NewAccess.
func New(opts ...Option) *Access {
	return newAccess(opts)
}

// NewMemoryAccess is an alias for NewAccess.
func NewMemoryAccess(opts ...Option) *Access {
	return newAccess(opts)
}

// NewWithStore creates an Access with a custom ScanStore.
func NewWithStore(store ScanStore, opts ...Option) *Access {
	return newAccess(append([]Option{WithStore(store)}, opts...))
}

// CreateJob sanitizes the image, creates an unguessable identifier and binds
// the job to the subject and absolute expiry time.
func (a *Access) CreateJob(ctx context.Context, spec JobSpec) (JobInfo, error) {
	if err := contextError(ctx); err != nil {
		return JobInfo{}, err
	}
	if !spec.Subject.valid() {
		return JobInfo{}, ErrUnauthenticated
	}
	if len(spec.Image) == 0 {
		return JobInfo{}, ErrInvalidImage
	}

	now := a.now().UTC()
	expires := spec.ExpiresAt
	if expires.IsZero() {
		expires = now.Add(a.defaultTTL)
	} else {
		expires = expires.UTC()
	}
	if !expires.After(now) {
		return JobInfo{}, &AccessError{Code: CodeInvalidExpiry, Message: "expiry must be in the future"}
	}
	if a.maxTTL > 0 && expires.Sub(now) > a.maxTTL {
		return JobInfo{}, &AccessError{Code: CodeInvalidExpiry, Message: "expiry exceeds maximum job lifetime"}
	}

	clean, err := SanitizeImage(spec.Image, spec.ContentType)
	if err != nil {
		return JobInfo{}, err
	}
	if err := contextError(ctx); err != nil {
		return JobInfo{}, err
	}

	jobID, err := a.newUniqueJobID()
	if err != nil {
		return JobInfo{}, err
	}
	if err := a.store.Put(jobID, clean.Data); err != nil {
		return JobInfo{}, ErrInternal
	}

	info := JobInfo{
		ID:          jobID,
		UserID:      spec.Subject.UserID,
		DeviceID:    spec.Subject.DeviceID,
		CreatedAt:   now,
		ExpiresAt:   expires,
		ContentType: clean.ContentType,
		Format:      clean.Format,
		Size:        len(clean.Data),
	}
	a.mu.Lock()
	a.jobs[jobID] = &jobRecord{info: info}
	a.mu.Unlock()
	return info, nil
}

// CreateScan is a convenience wrapper for CreateJob.
func (a *Access) CreateScan(ctx context.Context, subject Subject, image []byte, contentType string, expiresAt time.Time) (JobInfo, error) {
	return a.CreateJob(ctx, JobSpec{
		Subject:     subject,
		Image:       image,
		ContentType: contentType,
		ExpiresAt:   expiresAt,
	})
}

// Create is an alias for CreateJob.
func (a *Access) Create(ctx context.Context, spec JobSpec) (JobInfo, error) {
	return a.CreateJob(ctx, spec)
}

func (a *Access) newUniqueJobID() (string, error) {
	for attempt := 0; attempt < 8; attempt++ {
		id, err := randomJobID(a.random, a.idBytes)
		if err != nil {
			return "", err
		}
		a.mu.Lock()
		_, exists := a.jobs[id]
		a.mu.Unlock()
		if !exists {
			return id, nil
		}
	}
	return "", ErrInternal
}

// Read returns a sanitized scan only when the caller owns the job and it has
// not expired. Unknown, malformed, cross-user and cross-device requests all
// return an error that matches both ErrNotFound and ErrForbidden.
func (a *Access) Read(ctx context.Context, subject Subject, jobID string) (*Scan, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if !subject.valid() {
		return nil, ErrUnauthenticated
	}
	now := a.now().UTC()
	if a.rateLimited(subject.UserID, now) {
		return nil, ErrRateLimited
	}
	if !a.validJobID(jobID) {
		a.recordFailure(subject.UserID, now)
		return nil, ErrInvalidJobID
	}

	a.mu.Lock()
	record, ok := a.jobs[jobID]
	a.mu.Unlock()
	if !ok {
		a.recordFailure(subject.UserID, now)
		return nil, notAccessible
	}

	info := record.info
	if !secureEqual(info.UserID, subject.UserID) || !secureEqual(info.DeviceID, subject.DeviceID) {
		a.recordFailure(subject.UserID, now)
		return nil, notAccessible
	}
	if !now.Before(info.ExpiresAt) {
		return nil, ErrExpired
	}

	data, err := a.store.Get(jobID)
	if err != nil {
		if err == ErrStoreNotFound {
			a.recordFailure(subject.UserID, now)
			return nil, notAccessible
		}
		return nil, ErrInternal
	}
	clean, err := SanitizeImage(data, info.ContentType)
	if err != nil {
		return nil, ErrInternal
	}
	if err := contextError(ctx); err != nil {
		return nil, err
	}

	return &Scan{
		JobID:       info.ID,
		Subject:     Subject{UserID: info.UserID, DeviceID: info.DeviceID},
		CreatedAt:   info.CreatedAt,
		ExpiresAt:   info.ExpiresAt,
		ContentType: clean.ContentType,
		Format:      clean.Format,
		Data:        clean.Data,
	}, nil
}

// ReadScan is an alias for Read.
func (a *Access) ReadScan(ctx context.Context, subject Subject, jobID string) (*Scan, error) {
	return a.Read(ctx, subject, jobID)
}

// GetScan is an alias for Read.
func (a *Access) GetScan(ctx context.Context, subject Subject, jobID string) (*Scan, error) {
	return a.Read(ctx, subject, jobID)
}

// ReadImage returns only the sanitized image bytes for the owning subject.
func (a *Access) ReadImage(ctx context.Context, subject Subject, jobID string) ([]byte, error) {
	scan, err := a.Read(ctx, subject, jobID)
	if err != nil {
		return nil, err
	}
	return scan.Bytes(), nil
}

// Revoke removes a job owned by subject.
func (a *Access) Revoke(ctx context.Context, subject Subject, jobID string) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if !subject.valid() {
		return ErrUnauthenticated
	}
	if !a.validJobID(jobID) {
		return notAccessible
	}
	a.mu.Lock()
	record, ok := a.jobs[jobID]
	if !ok || !secureEqual(record.info.UserID, subject.UserID) || !secureEqual(record.info.DeviceID, subject.DeviceID) {
		a.mu.Unlock()
		return notAccessible
	}
	delete(a.jobs, jobID)
	a.mu.Unlock()
	if err := a.store.Delete(jobID); err != nil {
		return ErrInternal
	}
	return nil
}

// PurgeExpired removes expired job records and images. It is safe to call from
// a periodic maintenance goroutine.
func (a *Access) PurgeExpired(ctx context.Context) (int, error) {
	if err := contextError(ctx); err != nil {
		return 0, err
	}
	now := a.now().UTC()
	var expired []string
	a.mu.Lock()
	for id, record := range a.jobs {
		if !now.Before(record.info.ExpiresAt) {
			expired = append(expired, id)
			delete(a.jobs, id)
		}
	}
	a.mu.Unlock()

	for _, id := range expired {
		if err := a.store.Delete(id); err != nil {
			return len(expired), ErrInternal
		}
	}
	return len(expired), nil
}

func (a *Access) validJobID(jobID string) bool {
	decoded, err := base64.RawURLEncoding.DecodeString(jobID)
	if err != nil || len(decoded) != a.idBytes {
		return false
	}
	return base64.RawURLEncoding.EncodeToString(decoded) == jobID
}

func (a *Access) rateLimited(userID string, now time.Time) bool {
	if a.failureLimit <= 0 {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	state := a.failures[userID]
	if state == nil {
		return false
	}
	if state.blockedUntil.After(now) {
		return true
	}
	if now.Sub(state.windowStart) >= a.failureWindow {
		delete(a.failures, userID)
		return false
	}
	return false
}

func (a *Access) recordFailure(userID string, now time.Time) {
	if a.failureLimit <= 0 {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	state := a.failures[userID]
	if state == nil || now.Sub(state.windowStart) >= a.failureWindow {
		state = &failureState{windowStart: now}
		a.failures[userID] = state
	}
	state.count++
	if state.count >= a.failureLimit {
		state.blockedUntil = now.Add(a.failureWindow)
	}
}

func secureEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}
