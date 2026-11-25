package reuseauth

import (
	"encoding/json"
	"fmt"
	"sort"
	"sync"
)

// CallbackKind identifies a late download callback.
type CallbackKind string

const (
	CallbackComplete CallbackKind = "complete"
	CallbackError    CallbackKind = "error"
	CallbackLength   CallbackKind = "length"
)

// Callback is a host-side late download notification. Its Public flag is
// untrusted input and must equal the flag bound to the signed response.
type Callback struct {
	DownloadID DownloadID   `json:"download_id"`
	Kind       CallbackKind `json:"kind"`
	Public     bool         `json:"public"`
	Length     int64        `json:"length,omitempty"`
	Code       string       `json:"code,omitempty"`
	At         int64        `json:"at,omitempty"`
}

// InboundCallback is a callback addressed to one navigation receiver.
type InboundCallback struct {
	Callback
	NavID           NavID     `json:"nav_id"`
	RequestID       RequestID `json:"request_id"`
	RecipientOrigin Origin    `json:"recipient_origin"`
	// Epoch may carry the epoch known when the download began. If non-zero it
	// must match the fenced waiter/current navigation epoch.
	Epoch Epoch `json:"presented_epoch,omitempty"`
}

// DeliveryReceipt is trusted-host delivery metadata. It is never placed in a
// navigation-visible ChangeSet.
type DeliveryReceipt struct {
	NavID     NavID     `json:"nav_id"`
	RequestID RequestID `json:"request_id"`
	Delivered bool      `json:"delivered"`
	Reason    string    `json:"reason,omitempty"`
}

// AuditEntry is internal host audit data and may contain private correlation.
type AuditEntry struct {
	At         int64      `json:"at"`
	NavID      NavID      `json:"nav_id,omitempty"`
	DownloadID DownloadID `json:"download_id,omitempty"`
	RequestID  RequestID  `json:"request_id,omitempty"`
	Operation  string     `json:"operation"`
	Delivered  bool       `json:"delivered"`
	Reason     string     `json:"reason,omitempty"`
}

type objectRecord struct {
	id           DownloadID
	chain        SignedRedirectChain
	response     SignedResponse
	manifest     Manifest
	responseHash Hash
	public       bool
	audience     Origin
	length       int64
	complete     bool
	failed       bool
}

// PublishedObject is verified by Broker before it enters the cache index.
type PublishedObject struct {
	ID       DownloadID
	Chain    SignedRedirectChain
	Response SignedResponse
	Manifest Manifest
}

type WaiterKey struct {
	NavID     NavID
	RequestID RequestID
}

type waiter struct {
	key       WaiterKey
	download  DownloadID
	recipient Origin
	epoch     Epoch
	proof     SealedProof
	request   ReuseRequest
}

// PendingHit is a one-time authorized candidate that has not mutated state.
type PendingHit struct {
	navID      NavID
	downloadID DownloadID
	request    ReuseRequest
	proof      SealedProof
	shared     []RangeDigest
	done       bool
}

// Broker is the concurrency-safe reuse authentication and delivery boundary.
type Broker struct {
	mu      sync.Mutex
	auth    *Authority
	navs    map[NavID]*Navigation
	objects map[DownloadID]*objectRecord
	waiters map[WaiterKey]*waiter
	views   map[NavID]map[RequestID]*VisibleState
	inbox   map[NavID][]ChangeSet
	changes map[NavID][]ChangeSet
	audit   []AuditEntry
}

// NewBroker creates an empty broker using auth for proof verification.
func NewBroker(auth *Authority) *Broker {
	return &Broker{
		auth:    auth,
		navs:    make(map[NavID]*Navigation),
		objects: make(map[DownloadID]*objectRecord),
		waiters: make(map[WaiterKey]*waiter),
		views:   make(map[NavID]map[RequestID]*VisibleState),
		inbox:   make(map[NavID][]ChangeSet),
		changes: make(map[NavID][]ChangeSet),
	}
}

// RegisterNavigation adds a navigation under Broker's mutex.
func (b *Broker) RegisterNavigation(nav *Navigation) error {
	if nav == nil || nav.ID == "" {
		return fmt.Errorf("%w: navigation", ErrInvalidArgument)
	}
	if !nav.CredentialMode.Valid() || nav.Recipient.IsZero() || nav.Epoch == 0 {
		return fmt.Errorf("%w: navigation context incomplete", ErrInvalidArgument)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, exists := b.navs[nav.ID]; exists {
		return fmt.Errorf("%w: navigation %s already registered", ErrInvalidArgument, nav.ID)
	}
	cloned := nav.Clone()
	b.navs[nav.ID] = &cloned
	return nil
}

// Navigation returns a cloned navigation snapshot.
func (b *Broker) Navigation(id NavID) (Navigation, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	nav, ok := b.navs[id]
	if !ok {
		return Navigation{}, false
	}
	return nav.Clone(), true
}

// Allow appends an allow policy decision.
func (b *Broker) Allow(id NavID, decision PolicyDecision) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	nav, ok := b.navs[id]
	if !ok {
		return fmt.Errorf("%w: navigation %s", ErrNotFound, id)
	}
	return nav.Allow(decision)
}

// Deny appends a deny policy decision.
func (b *Broker) Deny(id NavID, decision PolicyDecision) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	nav, ok := b.navs[id]
	if !ok {
		return fmt.Errorf("%w: navigation %s", ErrNotFound, id)
	}
	return nav.Deny(decision)
}

// PublishObject verifies signed evidence and inserts a cache record.
func (b *Broker) PublishObject(object PublishedObject, now int64) error {
	if object.ID == "" {
		return fmt.Errorf("%w: download id required", ErrInvalidArgument)
	}
	if b.auth == nil {
		return fmt.Errorf("%w: authority required", ErrInvalidArgument)
	}
	if err := object.Manifest.Validate(); err != nil {
		return err
	}
	responseHash, err := object.Response.Verify(b.auth.trust, object.Chain, now)
	if err != nil {
		return err
	}
	resp := object.Response.Response
	if resp.ManifestDigest != object.Manifest.Digest() {
		return fmt.Errorf("%w: published manifest is not bound by response", ErrDigestMismatch)
	}
	length := resp.Length
	if length < 0 {
		length = object.Manifest.ObjectLength
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if _, exists := b.objects[object.ID]; exists {
		return fmt.Errorf("%w: download %s already published", ErrInvalidArgument, object.ID)
	}
	b.objects[object.ID] = &objectRecord{
		id:           object.ID,
		chain:        object.Chain,
		response:     object.Response,
		manifest:     object.Manifest,
		responseHash: responseHash,
		public:       resp.Public,
		audience:     resp.Recipient,
		length:       length,
	}
	b.auditLocked(AuditEntry{At: now, DownloadID: object.ID, Operation: "publish", Delivered: true})
	return nil
}

// PrepareHit authenticates a candidate without making it visible.
func (b *Broker) PrepareHit(navID NavID, downloadID DownloadID, req ReuseRequest) (*PendingHit, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	nav, object, err := b.navObjectLocked(navID, downloadID)
	if err != nil {
		b.auditLocked(AuditEntry{At: req.Now, NavID: navID, DownloadID: downloadID, RequestID: req.RequestID, Operation: "prepare", Reason: err.Error()})
		return nil, err
	}
	proof, shared, err := b.auth.AuthorizeReuse(nav.Clone(), req)
	if err != nil {
		b.auditLocked(AuditEntry{At: req.Now, NavID: navID, DownloadID: downloadID, RequestID: req.RequestID, Operation: "prepare", Reason: err.Error()})
		return nil, invisibleHitError(err)
	}
	if proof.Proof.FinalResponse != object.responseHash {
		err := fmt.Errorf("%w: request evidence does not identify published object", ErrDigestMismatch)
		return nil, invisibleHitError(err)
	}
	return &PendingHit{navID: navID, downloadID: downloadID, request: req, proof: proof, shared: shared}, nil
}

func (b *Broker) navObjectLocked(navID NavID, downloadID DownloadID) (*Navigation, *objectRecord, error) {
	nav, ok := b.navs[navID]
	if !ok {
		return nil, nil, ErrNotFound
	}
	object, ok := b.objects[downloadID]
	if !ok {
		return nil, nil, ErrNotFound
	}
	return nav, object, nil
}

// CommitHit re-authorizes at the exact cache-hit point and applies the minimal
// visible change set. Policy revocation between prepare and commit is denied.
func (b *Broker) CommitHit(hit *PendingHit) (ChangeSet, error) {
	if hit == nil {
		return ChangeSet{}, fmt.Errorf("%w: nil pending hit", ErrInvalidArgument)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if hit.done {
		return ChangeSet{NavigationID: hit.navID, RequestID: hit.request.RequestID}, ErrConsumed
	}
	hit.done = true

	nav, object, err := b.navObjectLocked(hit.navID, hit.downloadID)
	if err != nil {
		return ChangeSet{NavigationID: hit.navID, RequestID: hit.request.RequestID}, err
	}
	if _, err := b.auth.VerifyProof(nav.Clone(), hit.proof, hit.request, hit.request.Now); err != nil {
		b.auditLocked(AuditEntry{NavID: hit.navID, DownloadID: hit.downloadID, RequestID: hit.request.RequestID, Operation: "commit", Reason: err.Error()})
		return ChangeSet{NavigationID: hit.navID, RequestID: hit.request.RequestID, Epoch: nav.Epoch}, invisibleHitError(err)
	}
	if hit.proof.Proof.FinalResponse != object.responseHash {
		err := fmt.Errorf("%w: published response changed", ErrDigestMismatch)
		return ChangeSet{NavigationID: hit.navID, RequestID: hit.request.RequestID, Epoch: nav.Epoch}, invisibleHitError(err)
	}
	cs := b.applySharedLocked(nav.ID, hit.request.RequestID, nav.Epoch, hit.shared)
	b.auditLocked(AuditEntry{NavID: nav.ID, DownloadID: object.id, RequestID: hit.request.RequestID, Operation: "commit", Delivered: true})
	return cs, nil
}

func (b *Broker) applySharedLocked(navID NavID, requestID RequestID, epoch Epoch, shared []RangeDigest) ChangeSet {
	byRequest, ok := b.views[navID]
	if !ok {
		byRequest = make(map[RequestID]*VisibleState)
		b.views[navID] = byRequest
	}
	before := VisibleState{}
	if existing, ok := byRequest[requestID]; ok {
		before = cloneVisibleState(*existing)
	}
	after := cloneVisibleState(before)
	after.Epoch = epoch
	after.Ranges = unionRangeDigests(before.Ranges, shared)
	byRequest[requestID] = &after

	cs := DiffState(navID, requestID, epoch, before, after)
	if !cs.Empty() {
		b.appendChangeLocked(navID, cs)
	}
	return cs
}

// Wait authorizes/currently applies a hit and then fences a waiter for late
// callbacks. Authorization failures are collapsed to ErrNotFound.
func (b *Broker) Wait(navID NavID, downloadID DownloadID, req ReuseRequest) (ChangeSet, error) {
	if req.RequestID == "" {
		return ChangeSet{}, fmt.Errorf("%w: request id required", ErrInvalidArgument)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	nav, object, err := b.navObjectLocked(navID, downloadID)
	if err != nil {
		return ChangeSet{}, err
	}
	proof, shared, err := b.auth.AuthorizeReuse(nav.Clone(), req)
	if err != nil {
		b.auditLocked(AuditEntry{At: req.Now, NavID: navID, DownloadID: downloadID, RequestID: req.RequestID, Operation: "wait", Reason: err.Error()})
		return ChangeSet{}, invisibleHitError(err)
	}
	if proof.Proof.FinalResponse != object.responseHash {
		err := fmt.Errorf("%w: request evidence does not identify published object", ErrDigestMismatch)
		return ChangeSet{}, invisibleHitError(err)
	}

	key := WaiterKey{NavID: navID, RequestID: req.RequestID}
	if existing, ok := b.waiters[key]; ok && existing.download != downloadID {
		return ChangeSet{}, fmt.Errorf("%w: request %s already waits on %s", ErrInvalidArgument, req.RequestID, existing.download)
	}
	cs := b.applySharedLocked(navID, req.RequestID, nav.Epoch, shared)
	b.waiters[key] = &waiter{
		key:       key,
		download:  downloadID,
		recipient: nav.Recipient,
		epoch:     nav.Epoch,
		proof:     proof,
		request:   req,
	}
	b.auditLocked(AuditEntry{At: req.Now, NavID: navID, DownloadID: downloadID, RequestID: req.RequestID, Operation: "wait", Delivered: true})
	return cs, nil
}

func (b *Broker) resetNavigationLocked(navID NavID, epoch Epoch) []ChangeSet {
	byRequest := b.views[navID]
	requestIDs := make([]RequestID, 0, len(byRequest))
	for requestID := range byRequest {
		requestIDs = append(requestIDs, requestID)
	}
	sort.Slice(requestIDs, func(i, j int) bool { return requestIDs[i] < requestIDs[j] })

	var changes []ChangeSet
	for _, requestID := range requestIDs {
		view := byRequest[requestID]
		removed := mergeRanges([]ByteRange(nil))
		for _, rd := range view.Ranges {
			removed = append(removed, rd.Range)
		}
		cs := ChangeSet{
			NavigationID: navID,
			RequestID:    requestID,
			Epoch:        epoch,
			Removed:      mergeRanges(removed),
			Reset:        true,
		}
		changes = append(changes, cs)
		b.appendChangeLocked(navID, cs)
	}
	delete(b.views, navID)
	return changes
}

// RevokeNavigation revokes and fences all of navID's waiters and views.
func (b *Broker) RevokeNavigation(navID NavID, at int64) ([]ChangeSet, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	nav, ok := b.navs[navID]
	if !ok {
		return nil, fmt.Errorf("%w: navigation %s", ErrNotFound, navID)
	}
	if err := nav.Revoke(at); err != nil {
		return nil, err
	}
	changes := b.resetNavigationLocked(navID, nav.Epoch)
	b.auditLocked(AuditEntry{At: at, NavID: navID, Operation: "revoke", Delivered: true})
	return changes, nil
}

// SwitchTopLevel changes site and atomically resets navigation-visible state.
func (b *Broker) SwitchTopLevel(navID NavID, site string, at int64) ([]ChangeSet, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	nav, ok := b.navs[navID]
	if !ok {
		return nil, fmt.Errorf("%w: navigation %s", ErrNotFound, navID)
	}
	if err := nav.SwitchTopLevel(site, at); err != nil {
		return nil, err
	}
	changes := b.resetNavigationLocked(navID, nav.Epoch)
	b.auditLocked(AuditEntry{At: at, NavID: navID, Operation: "switch-top-level", Reason: site, Delivered: true})
	return changes, nil
}

// DeliverTo strictly validates and delivers one callback to one receiver.
func (b *Broker) DeliverTo(in InboundCallback) (ChangeSet, error) {
	if in.NavID == "" || in.RequestID == "" || in.RecipientOrigin.IsZero() || in.Kind == "" {
		return ChangeSet{}, fmt.Errorf("%w: addressed callback fields missing", ErrInvalidArgument)
	}
	if in.Kind != CallbackComplete && in.Kind != CallbackError && in.Kind != CallbackLength {
		return ChangeSet{}, fmt.Errorf("%w: unknown callback kind %q", ErrInvalidArgument, in.Kind)
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	nav, ok := b.navs[in.NavID]
	if !ok {
		return b.rejectCallbackLocked(in, "unknown navigation", ErrNotFound)
	}
	key := WaiterKey{NavID: in.NavID, RequestID: in.RequestID}
	waiter, ok := b.waiters[key]
	if !ok || waiter.download != in.DownloadID {
		return b.rejectCallbackLocked(in, "unknown waiter", ErrNotFound)
	}
	if !in.RecipientOrigin.Equal(waiter.recipient) || !in.RecipientOrigin.Equal(nav.Recipient) {
		return b.rejectCallbackLocked(in, "recipient origin mismatch", ErrOrigin)
	}
	if in.Epoch != 0 && in.Epoch != waiter.epoch {
		return b.rejectCallbackLocked(in, "presented epoch stale", fmt.Errorf("%w: presented %d waiter %d", ErrEpoch, in.Epoch, waiter.epoch))
	}
	if waiter.epoch != nav.Epoch || nav.Revoked {
		return b.rejectCallbackLocked(in, "waiter generation fenced", fmt.Errorf("%w/%w: waiter %d current %d revoked=%v", ErrRevoked, ErrEpoch, waiter.epoch, nav.Epoch, nav.Revoked))
	}
	object, ok := b.objects[in.DownloadID]
	if !ok {
		return b.rejectCallbackLocked(in, "unknown object", ErrNotFound)
	}
	if in.Public != object.public {
		return b.rejectCallbackLocked(in, "public flag mismatch", fmt.Errorf("%w: callback=%v record=%v", ErrPublicFlag, in.Public, object.public))
	}
	if !object.public {
		if object.audience.IsZero() || !object.audience.Equal(nav.Recipient) {
			return b.rejectCallbackLocked(in, "private audience mismatch", ErrOrigin)
		}
	} else if !object.audience.IsZero() && !object.audience.IsAny() && !object.audience.Equal(nav.Recipient) {
		return b.rejectCallbackLocked(in, "signed public audience mismatch", ErrOrigin)
	}

	action := ActionRead
	switch in.Kind {
	case CallbackComplete:
		action = ActionComplete
	case CallbackError:
		action = ActionError
	case CallbackLength:
		action = ActionLength
		if in.Length < 0 {
			return b.rejectCallbackLocked(in, "negative length", ErrLength)
		}
		if object.length >= 0 && in.Length != object.length {
			return b.rejectCallbackLocked(in, "conflicting length", fmt.Errorf("%w: callback %d record %d", ErrLength, in.Length, object.length))
		}
	}
	if err := nav.Check(PolicyQuery{Action: action, Source: nav.Recipient, Target: object.response.Response.Responder, Public: object.public}); err != nil {
		return b.rejectCallbackLocked(in, "policy denied", err)
	}
	if _, err := b.auth.VerifyProof(nav.Clone(), waiter.proof, waiter.request, in.At); err != nil {
		return b.rejectCallbackLocked(in, "proof revalidation failed", err)
	}

	byRequest := b.views[in.NavID]
	if byRequest == nil {
		byRequest = make(map[RequestID]*VisibleState)
		b.views[in.NavID] = byRequest
	}
	before := VisibleState{}
	if existing, ok := byRequest[in.RequestID]; ok {
		before = cloneVisibleState(*existing)
	}
	after := cloneVisibleState(before)
	after.Epoch = nav.Epoch

	switch in.Kind {
	case CallbackLength:
		length := in.Length
		after.Length = &length
		if object.length < 0 {
			object.length = length
		}
	case CallbackComplete:
		after.Completed = true
		object.complete = true
	case CallbackError:
		code := in.Code
		if code == "" {
			code = "download-error"
		}
		after.Failed = true
		after.FailureCode = code
		object.failed = true
	}
	byRequest[in.RequestID] = &after
	cs := DiffState(in.NavID, in.RequestID, nav.Epoch, before, after)
	if cs.Failed {
		cs.Code = after.FailureCode
	}
	if !cs.Empty() {
		b.appendChangeLocked(in.NavID, cs)
	}
	b.auditLocked(AuditEntry{At: in.At, NavID: in.NavID, DownloadID: in.DownloadID, RequestID: in.RequestID, Operation: "callback-" + string(in.Kind), Delivered: true})
	return cs, nil
}

func (b *Broker) rejectCallbackLocked(in InboundCallback, reason string, err error) (ChangeSet, error) {
	b.auditLocked(AuditEntry{
		At:         in.At,
		NavID:      in.NavID,
		DownloadID: in.DownloadID,
		RequestID:  in.RequestID,
		Operation:  "callback-" + string(in.Kind),
		Reason:     reason + ": " + err.Error(),
	})
	return ChangeSet{NavigationID: in.NavID, RequestID: in.RequestID}, err
}

// Deliver fans a callback out deterministically to all current waiters. Each
// receiver is independently origin, flag, epoch, and policy validated.
func (b *Broker) Deliver(cb Callback) []DeliveryReceipt {
	b.mu.Lock()
	targets := make([]InboundCallback, 0)
	for key, waiter := range b.waiters {
		if waiter.download != cb.DownloadID {
			continue
		}
		targets = append(targets, InboundCallback{
			Callback:        cb,
			NavID:           key.NavID,
			RequestID:       key.RequestID,
			RecipientOrigin: waiter.recipient,
			Epoch:           waiter.epoch,
		})
	}
	b.mu.Unlock()

	sort.Slice(targets, func(i, j int) bool {
		if targets[i].NavID != targets[j].NavID {
			return targets[i].NavID < targets[j].NavID
		}
		return targets[i].RequestID < targets[j].RequestID
	})
	receipts := make([]DeliveryReceipt, 0, len(targets))
	for _, target := range targets {
		_, err := b.DeliverTo(target)
		receipt := DeliveryReceipt{NavID: target.NavID, RequestID: target.RequestID, Delivered: err == nil}
		if err != nil {
			receipt.Reason = err.Error()
		}
		receipts = append(receipts, receipt)
	}
	return receipts
}

func (b *Broker) appendChangeLocked(navID NavID, cs ChangeSet) {
	b.changes[navID] = append(b.changes[navID], cs)
	b.inbox[navID] = append(b.inbox[navID], cs)
}

func (b *Broker) auditLocked(entry AuditEntry) { b.audit = append(b.audit, entry) }

// Drain returns and clears navigationID's pending visible change sets.
func (b *Broker) Drain(navigationID NavID) []ChangeSet {
	b.mu.Lock()
	defer b.mu.Unlock()
	source := b.inbox[navigationID]
	out := make([]ChangeSet, len(source))
	for i, change := range source {
		out[i] = cloneChangeSet(change)
	}
	b.inbox[navigationID] = nil
	return out
}

// Changes returns the complete deterministic visible replay for a navigation.
func (b *Broker) Changes(navigationID NavID) []ChangeSet {
	b.mu.Lock()
	defer b.mu.Unlock()
	source := b.changes[navigationID]
	out := make([]ChangeSet, len(source))
	for i, change := range source {
		out[i] = cloneChangeSet(change)
	}
	return out
}

// ChangesJSON returns canonical JSON for differential replay comparison.
func (b *Broker) ChangesJSON(navigationID NavID) ([]byte, error) {
	return json.Marshal(b.Changes(navigationID))
}

// TraceHash hashes the complete navigation-visible replay.
func (b *Broker) TraceHash(navigationID NavID) Hash {
	raw, _ := b.ChangesJSON(navigationID)
	return hashString("reuseauth.navigation-trace.v1", string(raw))
}

// View returns a clone of the current visible state for one request.
func (b *Broker) View(navigationID NavID, requestID RequestID) (VisibleState, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	byRequest, ok := b.views[navigationID]
	if !ok {
		return VisibleState{}, false
	}
	view, ok := byRequest[requestID]
	if !ok {
		return VisibleState{}, false
	}
	return cloneVisibleState(*view), true
}

// Audit returns a copy of internal trusted-host audit records.
func (b *Broker) Audit() []AuditEntry {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]AuditEntry(nil), b.audit...)
}
