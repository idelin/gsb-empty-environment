package netsec

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/csv"
	"encoding/hex"
	"io"
	"time"
)

// defaultTaskTTL is how long a pending task may wait before execution.
const defaultTaskTTL = 15 * time.Minute

// ServiceOption customizes a Service at construction.
type ServiceOption func(*Service)

// WithTaskTTL overrides the default task expiry duration.
func WithTaskTTL(ttl time.Duration) ServiceOption {
	return func(s *Service) {
		if ttl > 0 {
			s.ttl = ttl
		}
	}
}

// WithRandomSource injects a random source, primarily for deterministic
// testing. Production callers leave this unset and use crypto/rand.
func WithRandomSource(r io.Reader) ServiceOption {
	return func(s *Service) {
		if r != nil {
			s.rand = r
		}
	}
}

// Service is the importable task-scope component used by the Go export
// service. It is safe for concurrent use when its dependencies are.
type Service struct {
	store    TaskStore
	resolver PermissionResolver
	source   DataSource
	master   []byte
	ttl      time.Duration
	rand     io.Reader
}

// NewService constructs a Service. master must be a 32-byte secret.
func NewService(store TaskStore, resolver PermissionResolver, source DataSource, master []byte, opts ...ServiceOption) (*Service, error) {
	if len(master) != keySize {
		return nil, ErrInvalidKey
	}
	if store == nil || resolver == nil || source == nil {
		return nil, ErrInvalidKey
	}
	svc := &Service{
		store:    store,
		resolver: resolver,
		source:   source,
		master:   append([]byte(nil), master...),
		ttl:      defaultTaskTTL,
		rand:     rand.Reader,
	}
	for _, opt := range opts {
		opt(svc)
	}
	return svc, nil
}

// CreateTask binds a new single-use export task to the principal, tenant,
// dataset and the permission-reconciled filter set. Requested conditions that
// contradict mandatory constraints are rejected rather than silently widened.
func (s *Service) CreateTask(ctx context.Context, p Principal, dataset string, requested Filters) (*Task, error) {
	if p.ID == "" || p.TenantID == "" || dataset == "" {
		return nil, ErrPermissionDenied
	}

	perms, err := s.resolver.PermissionsFor(ctx, p)
	if err != nil {
		return nil, err
	}
	perm, ok := findPermission(perms, p.TenantID, dataset)
	if !ok {
		return nil, ErrPermissionDenied
	}

	effective, err := reconcileScope(requested, perm)
	if err != nil {
		return nil, err
	}

	id, err := s.newTaskID()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	task := &Task{
		ID:          id,
		PrincipalID: p.ID,
		TenantID:    p.TenantID,
		Dataset:     dataset,
		Filters:     effective,
		Status:      StatusPending,
		CreatedAt:   now,
		ExpiresAt:   now.Add(s.ttl),
	}
	task.binding = computeBinding(s.master, task)
	if err := s.store.Put(ctx, task); err != nil {
		return nil, err
	}
	return task.Clone(), nil
}

// ExportResult bundles the encrypted export and its provenance watermark.
type ExportResult struct {
	Envelope  []byte
	Watermark Watermark
	Rows      int
}

// ExecuteTask claims and runs a task. The scope is recomputed against current
// permissions before any data is read; cross-tenant access, foreign-owner
// reuse, task replay and condition tampering are all rejected here.
func (s *Service) ExecuteTask(ctx context.Context, p Principal, taskID string) (*ExportResult, error) {
	task, err := s.store.Claim(ctx, taskID)
	if err != nil {
		return nil, err
	}

	completed := false
	defer func() {
		if !completed {
			_ = s.store.ReleaseClaim(ctx, taskID)
		}
	}()

	if task.PrincipalID != p.ID {
		if task.TenantID != p.TenantID {
			return nil, ErrCrossTenant
		}
		return nil, ErrTaskNotOwned
	}
	if task.TenantID != p.TenantID {
		return nil, ErrCrossTenant
	}
	if time.Now().UTC().After(task.ExpiresAt) {
		return nil, ErrTaskExpired
	}
	if !verifyBinding(s.master, task) {
		return nil, ErrFilterTampered
	}

	perms, err := s.resolver.PermissionsFor(ctx, p)
	if err != nil {
		return nil, err
	}
	perm, ok := findPermission(perms, task.TenantID, task.Dataset)
	if !ok {
		return nil, ErrPermissionDenied
	}
	effective, err := reconcileScope(task.Filters, perm)
	if err != nil {
		return nil, err
	}
	if !filtersEqual(effective, task.Filters) {
		return nil, ErrScopeExceeded
	}

	columns, rows, err := s.source.Query(ctx, task.TenantID, task.Dataset, task.Filters)
	if err != nil {
		return nil, err
	}

	wm := buildWatermark(s.master, task)
	plaintext, err := renderExportCSV(wm, columns, rows)
	if err != nil {
		return nil, err
	}
	envelope, err := Seal(s.rand, s.master, plaintext)
	if err != nil {
		return nil, err
	}

	if err := s.store.MarkConsumed(ctx, taskID); err != nil {
		return nil, err
	}
	completed = true
	return &ExportResult{Envelope: envelope, Watermark: wm, Rows: len(rows)}, nil
}

// DecryptExport opens an envelope for verification or downstream delivery. It
// is exported so the service layer can hand plaintext to authorized channels
// without duplicating key handling.
func (s *Service) DecryptExport(envelope []byte) ([]byte, error) {
	return Open(s.master, envelope)
}

// VerifyExport decrypts an envelope and validates its watermark against the
// task that produced it.
func (s *Service) VerifyExport(envelope []byte, task *Task) ([]byte, error) {
	plaintext, err := Open(s.master, envelope)
	if err != nil {
		return nil, err
	}
	if err := verifyWatermark(s.master, plaintext, task); err != nil {
		return nil, err
	}
	return plaintext, nil
}

// newTaskID returns 128 bits of randomness as a hex string.
func (s *Service) newTaskID() (string, error) {
	raw := make([]byte, 16)
	if _, err := io.ReadFull(s.rand, raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

// renderExportCSV builds the plaintext payload: two watermark comment lines
// followed by the column header and matching data rows.
func renderExportCSV(wm Watermark, columns []string, rows []Row) ([]byte, error) {
	var buf bytes.Buffer
	buf.Write(wm.header())

	writer := csv.NewWriter(&buf)
	if err := writer.Write(columns); err != nil {
		return nil, err
	}
	for _, row := range rows {
		record := make([]string, len(columns))
		for i, col := range columns {
			record[i] = row[col]
		}
		if err := writer.Write(record); err != nil {
			return nil, err
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
