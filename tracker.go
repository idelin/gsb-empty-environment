package netsec

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

const defaultMaxAttempts = 3

// Config configures a Tracker. Verifier and at least one System are required.
// MaxAttempts defaults to 3. RetryBackoff defaults to zero, meaning eligible
// retries can run immediately; set a non-zero backoff for production use.
type Config struct {
	Verifier     IdentityVerifier
	Systems      []System
	AlertSink    AlertSink
	MaxAttempts  int
	RetryBackoff time.Duration
	Now          func() time.Time
}

type registeredSystem struct {
	info    SystemInfo
	handler CopyHandler
}

type itemState struct {
	system        registeredSystem
	state         ItemState
	attempts      int
	lastErr       string
	nextAttemptAt time.Time
	completedAt   time.Time
	receipt       *Receipt
}

type alertState struct {
	alert       Alert
	inFlight    bool
	deliveredAt time.Time
	lastError   string
}

type requestRecord struct {
	mu          sync.Mutex
	busy        bool
	req         Request
	items       []*itemState
	state       RequestState
	completedAt time.Time
	alert       *alertState
}

// Tracker coordinates verified data subject requests across every registered
// copy system. It is safe for concurrent use.
type Tracker struct {
	cfg     Config
	now     func() time.Time
	systems []registeredSystem

	mu      sync.RWMutex
	records map[string]*requestRecord
	order   []string
}

// NewTracker validates the configuration and returns a ready Tracker.
func NewTracker(cfg Config) (*Tracker, error) {
	if cfg.Verifier == nil {
		return nil, fmt.Errorf("%w: identity verifier is required", ErrInvalidConfiguration)
	}
	if len(cfg.Systems) == 0 {
		return nil, ErrNoCopySystems
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.MaxAttempts < 1 {
		cfg.MaxAttempts = defaultMaxAttempts
	}
	if cfg.RetryBackoff < 0 {
		cfg.RetryBackoff = 0
	}

	systems := make([]registeredSystem, 0, len(cfg.Systems))
	seen := make(map[string]struct{}, len(cfg.Systems))
	kinds := make(map[SystemKind]struct{}, len(StandardSystemKinds()))
	for _, system := range cfg.Systems {
		system.ID = strings.TrimSpace(system.ID)
		if system.ID == "" {
			return nil, fmt.Errorf("%w: system ID is required", ErrInvalidSystem)
		}
		if _, exists := seen[system.ID]; exists {
			return nil, fmt.Errorf("%w: duplicate system ID %q", ErrInvalidSystem, system.ID)
		}
		if !system.Kind.valid() {
			return nil, fmt.Errorf("%w: system %q has unsupported kind %q", ErrInvalidSystem, system.ID, system.Kind)
		}
		if system.Handler == nil {
			return nil, fmt.Errorf("%w: system %q has no handler", ErrInvalidSystem, system.ID)
		}
		seen[system.ID] = struct{}{}
		kinds[system.Kind] = struct{}{}
		systems = append(systems, registeredSystem{
			info:    SystemInfo{ID: system.ID, Kind: system.Kind},
			handler: system.Handler,
		})
	}
	missingKinds := make([]string, 0)
	for _, kind := range StandardSystemKinds() {
		if _, ok := kinds[kind]; !ok {
			missingKinds = append(missingKinds, string(kind))
		}
	}
	if len(missingKinds) > 0 {
		return nil, fmt.Errorf("%w: missing copy role(s): %s", ErrIncompleteInventory, strings.Join(missingKinds, ", "))
	}
	cfg.Systems = nil

	return &Tracker{
		cfg:     cfg,
		now:     cfg.Now,
		systems: systems,
		records: make(map[string]*requestRecord),
	}, nil
}

// Register verifies the identity proof and records a request for every
// registered copy system. No state is created when verification fails.
func (t *Tracker) Register(ctx context.Context, registration Registration) (Request, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	subjectID := strings.TrimSpace(registration.Subject.ID)
	if subjectID == "" {
		return Request{}, invalidRequestf("subject ID is required")
	}
	if !registration.Kind.valid() {
		return Request{}, invalidRequestf("unsupported request kind %q", registration.Kind)
	}
	scope, err := normalizeScope(registration.Scope)
	if err != nil {
		return Request{}, err
	}
	if registration.Deadline.IsZero() {
		return Request{}, invalidRequestf("deadline is required")
	}

	proof := registration.Proof
	proof.SubjectID = strings.TrimSpace(proof.SubjectID)
	if proof.SubjectID != subjectID {
		return Request{}, ErrIdentityVerificationFailed
	}
	if err := t.cfg.Verifier.Verify(ctx, Subject{ID: subjectID}, proof); err != nil {
		// Do not surface verifier detail here: a difference between "unknown
		// subject" and "wrong secret" would permit subject enumeration.
		return Request{}, ErrIdentityVerificationFailed
	}

	now := t.now()
	req := Request{
		Subject:    Subject{ID: subjectID},
		Kind:       registration.Kind,
		Scope:      scope,
		Deadline:   registration.Deadline,
		CreatedAt:  now,
		VerifiedAt: now,
	}

	record := &requestRecord{
		req:   req,
		items: make([]*itemState, 0, len(t.systems)),
		state: RequestPending,
	}
	for _, system := range t.systems {
		item := &itemState{system: system, state: ItemPending}
		record.items = append(record.items, item)
	}

	reqID, err := t.uniqueRequestID()
	if err != nil {
		return Request{}, err
	}
	req.ID = reqID
	record.req = req

	t.mu.Lock()
	t.records[req.ID] = record
	t.order = append(t.order, req.ID)
	t.mu.Unlock()

	// A request may be imported with a deadline that has already passed.
	// Make the overdue alert observable immediately; Tick delivers it.
	record.mu.Lock()
	t.ensureOverdueAlertLocked(record, now)
	record.mu.Unlock()

	return cloneRequest(req), nil
}

// Process attempts every outstanding copy system for a request. Failed systems
// are retried according to the configured attempt budget and backoff. Handler
// errors are reflected in the returned status rather than returned directly.
func (t *Tracker) Process(ctx context.Context, requestID string) (RequestStatus, error) {
	return t.process(ctx, requestID, false)
}

// Retry is like Process but ignores retry backoff. The attempt budget still
// applies, so a system that already exhausted its budget is not retried.
func (t *Tracker) Retry(ctx context.Context, requestID string) (RequestStatus, error) {
	return t.process(ctx, requestID, true)
}

func (t *Tracker) process(ctx context.Context, requestID string, force bool) (RequestStatus, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	record, err := t.lookup(requestID)
	if err != nil {
		return RequestStatus{}, err
	}

	record.mu.Lock()
	if record.busy {
		record.mu.Unlock()
		status, statusErr := t.statusFor(record)
		if statusErr != nil {
			return RequestStatus{}, statusErr
		}
		return status, ErrRequestBusy
	}
	record.busy = true
	record.mu.Unlock()
	defer func() {
		record.mu.Lock()
		record.busy = false
		record.mu.Unlock()
	}()

	var processErr error
	if err := t.ensureAndDeliverOverdueAlert(ctx, record); err != nil {
		processErr = errors.Join(processErr, err)
	}

	for {
		if err := ctx.Err(); err != nil {
			processErr = errors.Join(processErr, err)
			break
		}

		record.mu.Lock()
		var (
			item  *itemState
			task  CopyTask
			found bool
		)
		now := t.now()
		for _, candidate := range record.items {
			if candidate.state == ItemCompleted || candidate.state == ItemFailed {
				continue
			}
			if !force && candidate.attempts > 0 && now.Before(candidate.nextAttemptAt) {
				continue
			}
			if candidate.attempts >= t.cfg.MaxAttempts {
				candidate.state = ItemFailed
				candidate.lastErr = fmt.Sprintf("retry budget exhausted after %d attempts: %s", candidate.attempts, candidate.lastErr)
				candidate.nextAttemptAt = time.Time{}
				t.recomputeStateLocked(record, now)
				continue
			}

			candidate.attempts++
			item = candidate
			found = true
			task = CopyTask{
				RequestID:  record.req.ID,
				Subject:    record.req.Subject,
				Action:     record.req.Kind,
				Scope:      record.req.Scope.clone(),
				Deadline:   record.req.Deadline,
				SystemID:   candidate.system.info.ID,
				SystemKind: candidate.system.info.Kind,
				Attempt:    candidate.attempts,
			}
			break
		}
		record.mu.Unlock()
		if !found {
			break
		}

		receipt, handlerErr := invokeHandler(ctx, item.system.handler, task)
		completedAt := t.now()

		record.mu.Lock()
		if handlerErr != nil {
			item.lastErr = handlerErr.Error()
			if item.attempts >= t.cfg.MaxAttempts {
				item.state = ItemFailed
				item.lastErr = fmt.Sprintf("retry budget exhausted after %d attempts: %s", item.attempts, handlerErr)
				item.nextAttemptAt = time.Time{}
			} else {
				item.state = ItemRetrying
				item.nextAttemptAt = completedAt.Add(t.cfg.RetryBackoff)
			}
		} else if receiptErr := validateReceipt(receipt, task); receiptErr != nil {
			item.lastErr = receiptErr.Error()
			if item.attempts >= t.cfg.MaxAttempts {
				item.state = ItemFailed
				item.lastErr = fmt.Sprintf("retry budget exhausted after %d attempts: %s", item.attempts, receiptErr)
				item.nextAttemptAt = time.Time{}
			} else {
				item.state = ItemRetrying
				item.nextAttemptAt = completedAt.Add(t.cfg.RetryBackoff)
			}
		} else {
			if receipt.CompletedAt.IsZero() {
				receipt.CompletedAt = completedAt
			}
			accepted := receipt
			item.receipt = &accepted
			item.completedAt = accepted.CompletedAt
			item.state = ItemCompleted
			item.lastErr = ""
			item.nextAttemptAt = time.Time{}
		}
		t.recomputeStateLocked(record, completedAt)
		record.mu.Unlock()
	}

	if err := t.ensureAndDeliverOverdueAlert(ctx, record); err != nil {
		processErr = errors.Join(processErr, err)
	}

	status, err := t.statusFor(record)
	if err != nil {
		return RequestStatus{}, err
	}
	return status, processErr
}

// ProcessAll processes every registered request. Requests already being
// processed by another goroutine are skipped.
func (t *Tracker) ProcessAll(ctx context.Context) ([]RequestStatus, error) {
	ids := t.requestIDs()
	statuses := make([]RequestStatus, 0, len(ids))
	var errs []error
	for _, id := range ids {
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				errs = append(errs, err)
				break
			}
		}
		status, err := t.Process(ctx, id)
		if err != nil {
			if !errors.Is(err, ErrRequestBusy) {
				errs = append(errs, err)
			}
			if status.Request.ID == "" {
				continue
			}
		}
		statuses = append(statuses, status)
	}
	return statuses, errors.Join(errs...)
}

// Tick raises overdue alerts and retries delivery of any alerts that were not
// accepted by the sink. It returns alerts first raised during this call.
func (t *Tracker) Tick(ctx context.Context) ([]Alert, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ids := t.requestIDs()
	var raised []Alert
	var errs []error

	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			errs = append(errs, err)
			break
		}
		record, err := t.lookup(id)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		record.mu.Lock()
		now := t.now()
		created := t.ensureOverdueAlertLocked(record, now)
		record.mu.Unlock()
		if created != nil {
			raised = append(raised, created.clone())
		}
		if _, err := t.deliverOverdueAlert(ctx, record); err != nil {
			errs = append(errs, fmt.Errorf("deliver alert for request %q: %w", id, err))
		}
	}

	return raised, errors.Join(errs...)
}

// Status returns a snapshot of a request. It never completes a request: only
// accepted receipts from every registered system can do that.
func (t *Tracker) Status(requestID string) (RequestStatus, error) {
	record, err := t.lookup(requestID)
	if err != nil {
		return RequestStatus{}, err
	}
	return t.statusFor(record)
}

// Receipts returns accepted receipts in system registration order. Pending
// systems simply have no receipt; use VerifyComplete to require full coverage.
func (t *Tracker) Receipts(requestID string) ([]Receipt, error) {
	record, err := t.lookup(requestID)
	if err != nil {
		return nil, err
	}

	record.mu.Lock()
	defer record.mu.Unlock()
	receipts := make([]Receipt, 0, len(record.items))
	for _, item := range record.items {
		if item.state == ItemCompleted && item.receipt != nil {
			receipts = append(receipts, *item.receipt)
		}
	}
	return receipts, nil
}

// VerifyComplete returns nil only when every registered copy system has a
// valid receipt. Otherwise it returns an *IncompleteError.
func (t *Tracker) VerifyComplete(requestID string) error {
	status, err := t.Status(requestID)
	if err != nil {
		return err
	}
	if status.Complete {
		return nil
	}

	blocked := make([]string, 0)
	for _, item := range status.Items {
		if item.State == ItemFailed {
			blocked = append(blocked, item.SystemID)
		}
	}
	return &IncompleteError{
		RequestID: status.Request.ID,
		Missing:   append([]string(nil), status.MissingSystems...),
		Blocked:   blocked,
	}
}

// Alerts returns the overdue alert record for a request, if one was raised.
func (t *Tracker) Alerts(requestID string) ([]AlertRecord, error) {
	record, err := t.lookup(requestID)
	if err != nil {
		return nil, err
	}
	record.mu.Lock()
	defer record.mu.Unlock()
	if record.alert == nil {
		return nil, nil
	}
	return []AlertRecord{{
		Alert:       record.alert.alert.clone(),
		DeliveredAt: record.alert.deliveredAt,
		LastError:   record.alert.lastError,
	}}, nil
}

// Requests returns all registered requests in registration order.
func (t *Tracker) Requests() []Request {
	ids := t.requestIDs()
	requests := make([]Request, 0, len(ids))
	for _, id := range ids {
		record, err := t.lookup(id)
		if err != nil {
			continue
		}
		record.mu.Lock()
		requests = append(requests, cloneRequest(record.req))
		record.mu.Unlock()
	}
	return requests
}

// RequestCount returns the number of registered requests.
func (t *Tracker) RequestCount() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.order)
}

// Systems returns the configured copy systems in deterministic order.
func (t *Tracker) Systems() []SystemInfo {
	systems := make([]SystemInfo, len(t.systems))
	for i, system := range t.systems {
		systems[i] = system.info
	}
	return systems
}

func (t *Tracker) statusFor(record *requestRecord) (RequestStatus, error) {
	record.mu.Lock()
	defer record.mu.Unlock()

	now := t.now()
	status := RequestStatus{
		Request:     cloneRequest(record.req),
		State:       record.state,
		CompletedAt: record.completedAt,
		Items:       make([]ItemStatus, 0, len(record.items)),
	}

	for _, item := range record.items {
		itemStatus := ItemStatus{
			SystemID:      item.system.info.ID,
			SystemKind:    item.system.info.Kind,
			State:         item.state,
			Attempts:      item.attempts,
			LastError:     item.lastErr,
			NextAttemptAt: item.nextAttemptAt,
			CompletedAt:   item.completedAt,
		}
		if item.receipt != nil {
			receipt := *item.receipt
			itemStatus.Receipt = &receipt
		}
		status.Items = append(status.Items, itemStatus)
		if item.state != ItemCompleted {
			status.MissingSystems = append(status.MissingSystems, item.system.info.ID)
		}
	}

	status.Complete = record.state == RequestCompleted && len(status.MissingSystems) == 0
	if !record.req.Deadline.IsZero() {
		switch {
		case record.completedAt.IsZero():
			status.Overdue = now.After(record.req.Deadline)
			if status.Overdue {
				status.OverdueBy = now.Sub(record.req.Deadline)
			}
		case record.completedAt.After(record.req.Deadline):
			status.Overdue = true
			status.OverdueBy = record.completedAt.Sub(record.req.Deadline)
		}
	}
	if record.alert != nil {
		status.OverdueAlertedAt = record.alert.alert.CreatedAt
	}
	return status, nil
}

func (t *Tracker) recomputeStateLocked(record *requestRecord, now time.Time) {
	allCompleted := true
	anyFailed := false
	for _, item := range record.items {
		switch item.state {
		case ItemCompleted:
		case ItemFailed:
			allCompleted = false
			anyFailed = true
		default:
			allCompleted = false
		}
	}

	switch {
	case allCompleted:
		record.state = RequestCompleted
		if record.completedAt.IsZero() {
			record.completedAt = now
		}
	case anyFailed:
		record.state = RequestBlocked
	default:
		record.state = RequestPending
	}
}

func (t *Tracker) ensureOverdueAlertLocked(record *requestRecord, now time.Time) *Alert {
	if record.alert != nil || record.req.Deadline.IsZero() {
		return nil
	}
	if !now.After(record.req.Deadline) {
		return nil
	}
	if !record.completedAt.IsZero() && !record.completedAt.After(record.req.Deadline) {
		return nil
	}

	overdueBy := now.Sub(record.req.Deadline)
	if !record.completedAt.IsZero() {
		overdueBy = record.completedAt.Sub(record.req.Deadline)
	}
	missing := make([]string, 0)
	for _, item := range record.items {
		if item.state != ItemCompleted {
			missing = append(missing, item.system.info.ID)
		}
	}
	alert := Alert{
		ID:             "alert_overdue_" + record.req.ID,
		Kind:           AlertOverdue,
		RequestID:      record.req.ID,
		SubjectID:      record.req.Subject.ID,
		Action:         record.req.Kind,
		Deadline:       record.req.Deadline,
		CreatedAt:      now,
		OverdueBy:      overdueBy,
		MissingSystems: missing,
	}
	alert.Message = fmt.Sprintf(
		"data subject %s request %s is overdue by %s; %d copy system(s) incomplete",
		record.req.Kind, record.req.ID, overdueBy.Round(time.Second), len(missing),
	)
	record.alert = &alertState{alert: alert}
	return &alert
}

func (t *Tracker) deliverOverdueAlert(ctx context.Context, record *requestRecord) ([]Alert, error) {
	if t.cfg.AlertSink == nil {
		return nil, nil
	}

	record.mu.Lock()
	alert := record.alert
	if alert == nil || alert.inFlight || !alert.deliveredAt.IsZero() {
		record.mu.Unlock()
		return nil, nil
	}
	alert.inFlight = true
	toSend := alert.alert.clone()
	record.mu.Unlock()

	err := invokeAlertSink(ctx, t.cfg.AlertSink, toSend)
	record.mu.Lock()
	alert.inFlight = false
	if err != nil {
		alert.lastError = err.Error()
	} else {
		alert.lastError = ""
		alert.deliveredAt = t.now()
	}
	record.mu.Unlock()

	if err != nil {
		return []Alert{toSend}, err
	}
	return []Alert{toSend}, nil
}

func (t *Tracker) ensureAndDeliverOverdueAlert(ctx context.Context, record *requestRecord) error {
	record.mu.Lock()
	t.ensureOverdueAlertLocked(record, t.now())
	record.mu.Unlock()
	_, err := t.deliverOverdueAlert(ctx, record)
	return err
}

func (t *Tracker) lookup(requestID string) (*requestRecord, error) {
	t.mu.RLock()
	record := t.records[requestID]
	t.mu.RUnlock()
	if record == nil {
		return nil, fmt.Errorf("%w: %q", ErrRequestNotFound, requestID)
	}
	return record, nil
}

func (t *Tracker) requestIDs() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return append([]string(nil), t.order...)
}

func (t *Tracker) uniqueRequestID() (string, error) {
	for range 8 {
		var raw [16]byte
		if _, err := rand.Read(raw[:]); err != nil {
			return "", fmt.Errorf("%w: generate request ID: %v", ErrInvalidConfiguration, err)
		}
		id := "dsr_" + hex.EncodeToString(raw[:])
		t.mu.RLock()
		_, exists := t.records[id]
		t.mu.RUnlock()
		if !exists {
			return id, nil
		}
	}
	return "", fmt.Errorf("%w: could not allocate a unique request ID", ErrInvalidConfiguration)
}

func invokeHandler(ctx context.Context, handler CopyHandler, task CopyTask) (receipt Receipt, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("copy handler panicked: %v", recovered)
		}
	}()
	return handler.Process(ctx, task)
}

func invokeAlertSink(ctx context.Context, sink AlertSink, alert Alert) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("alert sink panicked: %v", recovered)
		}
	}()
	return sink.Send(ctx, alert)
}

func validateReceipt(receipt Receipt, task CopyTask) error {
	switch {
	case receipt.RequestID != task.RequestID:
		return invalidReceiptf("request ID %q does not match task %q", receipt.RequestID, task.RequestID)
	case receipt.SystemID != task.SystemID:
		return invalidReceiptf("system ID %q does not match task %q", receipt.SystemID, task.SystemID)
	case receipt.SubjectID != task.Subject.ID:
		return invalidReceiptf("subject ID does not match task")
	case receipt.Action != task.Action:
		return invalidReceiptf("action %q does not match task %q", receipt.Action, task.Action)
	case receipt.RecordsAffected < 0:
		return invalidReceiptf("records affected must not be negative")
	default:
		return nil
	}
}

func cloneRequest(req Request) Request {
	req.Scope = req.Scope.clone()
	return req
}
