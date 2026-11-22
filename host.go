package takeover

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"
	"time"
)

// HostOptions configures a session host. A shared HMAC key is convenient for
// a set of cooperating hosts; an explicit Signer/Verifier pair can be used
// with an external key distribution mechanism.
type HostOptions struct {
	Now                   func() time.Time
	Signer                Signer
	Verifier              SignatureVerifier
	SharedHMACKey         []byte
	KeyID                 string
	AllowUnsignedReceipts bool
}

// HostOption mutates HostOptions.
type HostOption func(*HostOptions)

// WithNow overrides the clock.
func WithNow(now func() time.Time) HostOption {
	return func(options *HostOptions) { options.Now = now }
}

// WithSigner sets the receipt signer.
func WithSigner(signer Signer) HostOption {
	return func(options *HostOptions) { options.Signer = signer }
}

// WithVerifier sets the receipt verifier.
func WithVerifier(verifier SignatureVerifier) HostOption {
	return func(options *HostOptions) { options.Verifier = verifier }
}

// WithSharedHMACKey configures both signing and verification with a shared
// HMAC-SHA256 key.
func WithSharedHMACKey(keyID string, key []byte) HostOption {
	return func(options *HostOptions) {
		options.KeyID = keyID
		options.SharedHMACKey = append([]byte(nil), key...)
	}
}

// WithUnsignedReceipts allows structurally valid receipts without a signature
// check. It is intended for tests and for hosts that authenticate the return
// at a lower transport layer.
func WithUnsignedReceipts() HostOption {
	return func(options *HostOptions) { options.AllowUnsignedReceipts = true }
}

type nodeStatus uint8

const (
	nodeActive nodeStatus = iota
	nodeReturned
)

type grantNode struct {
	grant        Grant
	status       nodeStatus
	consumed     SeqSet
	delegated    SeqSet
	sealed       SeqSet
	returnedSeq  SeqSet
	returnedDims DimensionSet
	children     map[GrantID]struct{}
	receipts     map[string]EffectReceipt
	receiptBySeq map[uint64]string
}

func (n *grantNode) activeDimensions() DimensionSet {
	return n.grant.Dimensions.Subtract(n.returnedDims)
}

// activeScope includes consumed and delegated members. Those members are no
// longer usable by this grant, but they still belong to its accounting scope
// until explicitly returned or sealed.
func (n *grantNode) activeScope() Scope {
	return Scope{
		Dimensions: n.activeDimensions(),
		Sequences:  n.grant.Sequences.Subtract(n.returnedSeq).Subtract(n.sealed),
	}
}

func (n *grantNode) availableSequences() SeqSet {
	return n.activeScope().Sequences.Subtract(n.consumed).Subtract(n.delegated)
}

// Host is a stateful authority for a grant tree. It is safe for concurrent
// use.
type Host struct {
	mu sync.RWMutex

	rootID     GrantID
	nodes      map[GrantID]*grantNode
	operations map[string]GrantID

	now           func() time.Time
	signer        Signer
	verifier      SignatureVerifier
	allowUnsigned bool
	nonceCounter  uint64
}

// NewHost constructs a host from a local root grant. Parent may be non-empty
// when this host imported a grant delegated by another host and is the root
// of its own local subtree.
func NewHost(root Grant, options ...HostOption) (*Host, error) {
	var config HostOptions
	for _, option := range options {
		if option != nil {
			option(&config)
		}
	}
	return NewHostWithOptions(root, config)
}

// NewHostWithOptions constructs a host from a root grant and a configuration
// value.
func NewHostWithOptions(root Grant, config HostOptions) (*Host, error) {
	if err := root.Validate(); err != nil {
		return nil, err
	}
	if config.Now == nil {
		config.Now = time.Now
	}

	var signer Signer = config.Signer
	var verifier SignatureVerifier = config.Verifier
	if signer == nil {
		keyID := config.KeyID
		if keyID == "" {
			keyID = "host:" + string(root.ID)
		}
		key := append([]byte(nil), config.SharedHMACKey...)
		if len(key) == 0 {
			key = make([]byte, 32)
			if _, err := rand.Read(key); err != nil {
				return nil, fmt.Errorf("takeover: generate host key: %w", err)
			}
		}
		hmacSigner := NewHMACSigner(keyID, key)
		signer = hmacSigner
		if verifier == nil {
			verifier = NewHMACVerifier(map[string][]byte{keyID: key})
		}
	} else if verifier == nil {
		if hmacSigner, ok := signer.(*HMACSigner); ok {
			verifier = NewHMACVerifier(map[string][]byte{hmacSigner.keyID: hmacSigner.key})
		}
	}
	if verifier == nil && !config.AllowUnsignedReceipts {
		return nil, fmt.Errorf("takeover: a receipt verifier is required")
	}

	node := &grantNode{
		grant:        root,
		status:       nodeActive,
		children:     make(map[GrantID]struct{}),
		receipts:     make(map[string]EffectReceipt),
		receiptBySeq: make(map[uint64]string),
	}
	return &Host{
		rootID:        root.ID,
		nodes:         map[GrantID]*grantNode{root.ID: node},
		operations:    make(map[string]GrantID),
		now:           config.Now,
		signer:        signer,
		verifier:      verifier,
		allowUnsigned: config.AllowUnsignedReceipts,
	}, nil
}

// RootID returns the root grant identifier.
func (h *Host) RootID() GrantID {
	return h.rootID
}

// Signer returns the configured signer.
func (h *Host) Signer() Signer {
	return h.signer
}

// Verifier returns the configured verifier.
func (h *Host) Verifier() SignatureVerifier {
	return h.verifier
}

// Lookup returns the immutable grant value currently recorded for id.
func (h *Host) Lookup(id GrantID) (Grant, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	node, ok := h.nodes[id]
	if !ok {
		return Grant{}, false
	}
	return node.grant, true
}

// ScopeOf returns the residual accounting scope of a grant.
func (h *Host) ScopeOf(id GrantID) (Scope, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	node, ok := h.nodes[id]
	if !ok {
		return Scope{}, false
	}
	return node.activeScope(), true
}

// AvailableScope returns the dimensions and sequences this grant can still
// use directly. Sequences delegated to an active child are excluded.
func (h *Host) AvailableScope(id GrantID) (Scope, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	node, ok := h.nodes[id]
	if !ok || node.status != nodeActive {
		return Scope{}, false
	}
	return Scope{
		Dimensions: h.availableDimensions(node),
		Sequences:  node.availableSequences(),
	}, true
}

func (h *Host) availableDimensions(node *grantNode) DimensionSet {
	out := node.activeDimensions()
	for childID := range node.children {
		child := h.nodes[childID]
		if child == nil || child.status != nodeActive {
			continue
		}
		out = out.Subtract(child.activeDimensions())
	}
	return out
}

// Delegate creates a child grant. The child must have the same session root,
// a subset of the parent's signals, an exactly held subset of dimensions, and
// still-unused sequences. Sibling dimensions must not overlap.
func (h *Host) Delegate(parentID GrantID, request DelegateRequest) (Grant, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	parent, ok := h.nodes[parentID]
	if !ok {
		return Grant{}, ErrGrantNotFound
	}
	if parent.status != nodeActive {
		return Grant{}, ErrGrantInactive
	}
	if request.GrantID == "" {
		return Grant{}, &ValidationError{Field: "grant_id", Err: ErrInvalidGrant}
	}
	if _, exists := h.nodes[request.GrantID]; exists {
		return Grant{}, ErrGrantExists
	}
	if !request.ProcessStartID.Valid() {
		return Grant{}, &ValidationError{Field: "process_start_id", Err: ErrInvalidGrant}
	}
	if request.SessionRoot != parent.grant.SessionRoot {
		return Grant{}, ErrSessionMismatch
	}
	if request.Signals.IsEmpty() || !request.Signals.SubsetOf(parent.grant.Signals) {
		return Grant{}, ErrSignalNotAllowed
	}
	if request.Dimensions.IsEmpty() {
		return Grant{}, &ValidationError{Field: "dimensions", Err: ErrInvalidGrant}
	}
	if err := validateDimensionSet(request.Dimensions); err != nil {
		return Grant{}, err
	}

	availableDims := parent.activeDimensions()
	for dimension := range request.Dimensions {
		if !availableDims.Contains(dimension) {
			return Grant{}, ErrDimensionNotOwned
		}
	}
	for childID := range parent.children {
		child := h.nodes[childID]
		if child == nil || child.status != nodeActive {
			continue
		}
		if child.activeDimensions().Intersects(request.Dimensions) {
			return Grant{}, &DimensionConflictError{
				A: firstDimension(child.activeDimensions()),
				B: firstDimension(request.Dimensions),
			}
		}
	}

	if request.Sequences.IsEmpty() || !request.Sequences.SubsetOf(parent.availableSequences()) {
		return Grant{}, ErrSequenceNotOwned
	}

	childGrant := Grant{
		ID:             request.GrantID,
		Parent:         parent.grant.ID,
		ProcessStartID: request.ProcessStartID,
		SessionRoot:    request.SessionRoot,
		Signals:        request.Signals.Clone(),
		Dimensions:     request.Dimensions.Clone(),
		Sequences:      request.Sequences.Clone(),
		Epoch:          parent.grant.Epoch + 1,
		IssuedAt:       h.now(),
	}
	child := &grantNode{
		grant:        childGrant,
		status:       nodeActive,
		children:     make(map[GrantID]struct{}),
		receipts:     make(map[string]EffectReceipt),
		receiptBySeq: make(map[uint64]string),
	}
	parent.delegated = parent.delegated.Union(request.Sequences)
	parent.children[childGrant.ID] = struct{}{}
	h.nodes[childGrant.ID] = child
	return childGrant, nil
}

func firstDimension(set DimensionSet) Dimension {
	for dimension := range set {
		return dimension
	}
	return Dimension{}
}

// Return returns a full or partial scope. Every active descendant that
// overlaps the returned scope must be covered by a signed receipt or by an
// ancestor seal for every unconsumed sequence and every affected dimension in
// that descendant's slice.
func (h *Host) Return(request ReturnRequest) (ReturnResult, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	node, ok := h.nodes[request.GrantID]
	if !ok {
		return ReturnResult{}, ErrGrantNotFound
	}
	if node.status != nodeActive {
		return ReturnResult{}, ErrGrantInactive
	}
	scope, err := h.returnScope(node, request)
	if err != nil {
		return ReturnResult{}, err
	}
	if scope.IsEmpty() {
		return ReturnResult{}, &ValidationError{Field: "scope", Err: ErrInvalidGrant}
	}

	receiptScopes, err := h.receiptEvidence(request.Receipts)
	if err != nil {
		return ReturnResult{}, err
	}
	sealScopes, err := h.sealEvidence(request.Seals)
	if err != nil {
		return ReturnResult{}, err
	}

	affected := h.affectedDescendants(request.GrantID, scope)
	var unsettled []GrantID
	for _, descendant := range affected {
		required := descendant.activeScope().Intersect(scope)
		if required.IsEmpty() {
			continue
		}
		neededSequences := required.Sequences.Subtract(descendant.consumed)
		coveredSequences := receiptScopes[descendant.grant.ID].Sequences.Union(sealScopes[descendant.grant.ID].Sequences)
		// A signal needs both a dimension and an unused sequence. The return
		// withdraws the dimensional slice, so fencing every unconsumed
		// sequence is sufficient to settle that descendant slice.
		if !neededSequences.SubsetOf(coveredSequences) {
			unsettled = append(unsettled, descendant.grant.ID)
		}
	}
	if len(unsettled) > 0 {
		sort.Slice(unsettled, func(i, j int) bool { return unsettled[i] < unsettled[j] })
		return ReturnResult{}, &UnsettledError{GrantIDs: unsettled}
	}

	sealed := scope.Sequences.Subtract(node.consumed)
	retained := node.activeScope().Subtract(scope)
	closed := retained.IsEmpty()
	receipt := ReturnReceipt{
		GrantID:        node.grant.ID,
		ProcessStartID: node.grant.ProcessStartID,
		SessionRoot:    node.grant.SessionRoot,
		Epoch:          node.grant.Epoch,
		Returned:       scope,
		Retained:       retained,
		Final:          closed,
		Sealed:         sealed,
		IssuedAt:       h.now(),
		Nonce:          h.nextNonce(),
	}
	if h.signer != nil {
		if err := receipt.Sign(h.signer); err != nil {
			return ReturnResult{}, err
		}
	}

	for _, descendant := range affected {
		slice := descendant.activeScope().Intersect(scope)
		descendant.sealed = descendant.sealed.Union(slice.Sequences.Subtract(descendant.consumed))
		descendant.returnedSeq = descendant.returnedSeq.Union(slice.Sequences)
		descendant.returnedDims = descendant.returnedDims.Union(slice.Dimensions)
		if descendant.activeScope().IsEmpty() {
			descendant.status = nodeReturned
		}
	}
	node.sealed = node.sealed.Union(sealed)
	node.returnedSeq = node.returnedSeq.Union(scope.Sequences)
	node.returnedDims = node.returnedDims.Union(scope.Dimensions)
	if closed {
		node.status = nodeReturned
	}

	settled := make([]GrantID, 0, len(affected))
	for _, descendant := range affected {
		settled = append(settled, descendant.grant.ID)
	}
	sort.Slice(settled, func(i, j int) bool { return settled[i] < settled[j] })
	return ReturnResult{
		GrantID:  node.grant.ID,
		Returned: scope,
		Retained: retained,
		Sealed:   sealed,
		Closed:   closed,
		Receipt:  receipt,
		Settled:  settled,
	}, nil
}

func (h *Host) returnScope(node *grantNode, request ReturnRequest) (Scope, error) {
	active := node.activeScope()
	partial := request.Partial || !request.Dimensions.IsEmpty() || !request.Sequences.IsEmpty()
	if !partial {
		return active, nil
	}
	scope := Scope{
		Dimensions: request.Dimensions.Clone(),
		Sequences:  request.Sequences.Clone(),
	}
	if scope.IsEmpty() {
		return Scope{}, nil
	}
	if err := validateDimensionSet(scope.Dimensions); err != nil {
		return Scope{}, err
	}
	for dimension := range scope.Dimensions {
		if !active.Dimensions.Contains(dimension) {
			return Scope{}, ErrSeqDimensionScope
		}
	}
	if !scope.Sequences.SubsetOf(active.Sequences) {
		return Scope{}, ErrSeqDimensionScope
	}
	return scope, nil
}

func (h *Host) receiptEvidence(receipts []ReturnReceipt) (map[GrantID]Scope, error) {
	out := make(map[GrantID]Scope, len(receipts))
	for _, receipt := range receipts {
		node, ok := h.nodes[receipt.GrantID]
		if !ok {
			return nil, fmt.Errorf("%w: unknown grant %s", ErrInvalidReceipt, receipt.GrantID)
		}
		if node.grant.ID == h.rootID {
			return nil, fmt.Errorf("%w: root is not a descendant", ErrInvalidReceipt)
		}
		if receipt.ProcessStartID != node.grant.ProcessStartID ||
			receipt.SessionRoot != node.grant.SessionRoot ||
			receipt.Epoch != node.grant.Epoch {
			return nil, fmt.Errorf("%w: fence mismatch for %s", ErrInvalidReceipt, receipt.GrantID)
		}
		if !receipt.Returned.Dimensions.SubsetOf(node.activeScope().Dimensions) ||
			!receipt.Returned.Sequences.SubsetOf(node.activeScope().Sequences) {
			return nil, fmt.Errorf("%w: receipt scope exceeds grant %s", ErrInvalidReceipt, receipt.GrantID)
		}
		if !h.allowUnsigned {
			if err := receipt.Verify(h.verifier); err != nil {
				return nil, err
			}
		}
		existing := out[receipt.GrantID]
		out[receipt.GrantID] = existing.Union(receipt.Returned)
	}
	return out, nil
}

func (h *Host) sealEvidence(seals []DescendantSeal) (map[GrantID]Scope, error) {
	out := make(map[GrantID]Scope, len(seals))
	for _, seal := range seals {
		node, ok := h.nodes[seal.GrantID]
		if !ok {
			return nil, fmt.Errorf("%w: unknown seal grant %s", ErrInvalidReceipt, seal.GrantID)
		}
		if node.grant.ID == h.rootID {
			return nil, fmt.Errorf("%w: root cannot be sealed as a descendant", ErrInvalidReceipt)
		}
		active := node.activeScope()
		for dimension := range seal.Dimensions {
			if !active.Dimensions.Contains(dimension) {
				return nil, fmt.Errorf("%w: seal dimension exceeds grant %s", ErrInvalidReceipt, seal.GrantID)
			}
		}
		if !seal.Sequences.SubsetOf(active.Sequences) {
			return nil, fmt.Errorf("%w: seal sequence exceeds grant %s", ErrInvalidReceipt, seal.GrantID)
		}
		existing := out[seal.GrantID]
		out[seal.GrantID] = existing.Union(Scope{
			Dimensions: seal.Dimensions,
			Sequences:  seal.Sequences,
		})
	}
	return out, nil
}

func (h *Host) affectedDescendants(root GrantID, scope Scope) []*grantNode {
	out := make([]*grantNode, 0)
	for id, node := range h.nodes {
		if id == root || node.status != nodeActive {
			continue
		}
		if !h.isDescendant(id, root) {
			continue
		}
		if node.activeScope().Intersect(scope).IsEmpty() {
			continue
		}
		out = append(out, node)
	}
	return out
}

func (h *Host) isDescendant(id, ancestor GrantID) bool {
	seen := make(map[GrantID]struct{})
	for id != "" {
		if _, ok := seen[id]; ok {
			return false
		}
		seen[id] = struct{}{}
		if id == ancestor {
			return true
		}
		node, ok := h.nodes[id]
		if !ok {
			return false
		}
		if node.grant.Parent == "" {
			return false
		}
		id = node.grant.Parent
	}
	return false
}

// ControlRequest is one terminal signal attempt. OperationID is the
// idempotency key used for effect-receipt lookup and crash recovery.
type ControlRequest struct {
	GrantID        GrantID        `json:"grant_id"`
	ProcessStartID ProcessStartID `json:"process_start_id"`
	SessionRoot    SessionRoot    `json:"session_root"`
	Sequence       uint64         `json:"sequence"`
	Signal         Signal         `json:"signal"`
	Dimensions     DimensionSet   `json:"dimensions,omitempty"`
	OperationID    string         `json:"operation_id"`
}

// EffectStatus is the receiver's recorded outcome.
type EffectStatus string

const (
	EffectPending    EffectStatus = "pending"
	EffectApplied    EffectStatus = "applied"
	EffectRejected   EffectStatus = "rejected"
	EffectNotApplied EffectStatus = "not_applied"
	EffectUnknown    EffectStatus = "unknown"
)

// EffectReceipt is an idempotent receipt for one control attempt.
type EffectReceipt struct {
	OperationID    string         `json:"operation_id"`
	GrantID        GrantID        `json:"grant_id"`
	ProcessStartID ProcessStartID `json:"process_start_id"`
	SessionRoot    SessionRoot    `json:"session_root"`
	Sequence       uint64         `json:"sequence"`
	Signal         Signal         `json:"signal"`
	Dimensions     DimensionSet   `json:"dimensions,omitempty"`
	Status         EffectStatus   `json:"status"`
	Detail         string         `json:"detail,omitempty"`
	AcceptedAt     time.Time      `json:"accepted_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
}

// Final reports whether the effect outcome is settled.
func (r EffectReceipt) Final() bool {
	return r.Status == EffectApplied || r.Status == EffectRejected || r.Status == EffectNotApplied
}

// ValidateControl checks a control message without consuming its sequence.
func (h *Host) ValidateControl(request ControlRequest) error {
	h.mu.RLock()
	defer h.mu.RUnlock()
	_, err := h.validateControlLocked(request)
	return err
}

func (h *Host) validateControlLocked(request ControlRequest) (EffectReceipt, error) {
	if request.OperationID == "" {
		return EffectReceipt{}, &ValidationError{Field: "operation_id", Err: ErrInvalidGrant}
	}
	node, ok := h.nodes[request.GrantID]
	if !ok {
		return EffectReceipt{}, ErrGrantNotFound
	}
	if node.status != nodeActive {
		return EffectReceipt{}, ErrGrantInactive
	}
	if request.ProcessStartID != node.grant.ProcessStartID {
		return EffectReceipt{}, ErrNotHolder
	}
	if request.SessionRoot != node.grant.SessionRoot {
		return EffectReceipt{}, ErrSessionMismatch
	}
	if !node.grant.Signals.Contains(request.Signal) {
		return EffectReceipt{}, ErrSignalNotAllowed
	}
	if !node.availableSequences().Contains(request.Sequence) {
		if node.activeScope().Sequences.Contains(request.Sequence) {
			if node.sealed.Contains(request.Sequence) {
				return EffectReceipt{}, ErrSequenceSealed
			}
			return EffectReceipt{}, ErrSequenceUsed
		}
		return EffectReceipt{}, ErrSequenceNotOwned
	}
	dimensions := request.Dimensions
	if dimensions.IsEmpty() {
		dimensions = h.availableDimensions(node)
	}
	if dimensions.IsEmpty() {
		return EffectReceipt{}, ErrDimensionNotOwned
	}
	availableDims := h.availableDimensions(node)
	for dimension := range dimensions {
		if !availableDims.Contains(dimension) {
			return EffectReceipt{}, ErrDimensionNotOwned
		}
	}
	now := h.now()
	return EffectReceipt{
		OperationID:    request.OperationID,
		GrantID:        request.GrantID,
		ProcessStartID: request.ProcessStartID,
		SessionRoot:    request.SessionRoot,
		Sequence:       request.Sequence,
		Signal:         request.Signal,
		Dimensions:     dimensions.Clone(),
		Status:         EffectPending,
		AcceptedAt:     now,
		UpdatedAt:      now,
	}, nil
}

// AuthorizeControl atomically validates and consumes one sequence. Repeating
// the same OperationID returns the existing receipt without consuming another
// sequence.
func (h *Host) AuthorizeControl(request ControlRequest) (EffectReceipt, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	node, ok := h.nodes[request.GrantID]
	if !ok {
		return EffectReceipt{}, ErrGrantNotFound
	}
	if operationGrant, ok := h.operations[request.OperationID]; ok && operationGrant != request.GrantID {
		return EffectReceipt{}, ErrReceiptConflict
	}
	if existing, ok := node.receipts[request.OperationID]; ok {
		dimensionsMatch := request.Dimensions.IsEmpty() || existing.Dimensions.Equal(request.Dimensions)
		if existing.GrantID != request.GrantID ||
			existing.ProcessStartID != request.ProcessStartID ||
			existing.SessionRoot != request.SessionRoot ||
			existing.Sequence != request.Sequence ||
			existing.Signal != request.Signal ||
			!dimensionsMatch {
			return EffectReceipt{}, ErrReceiptConflict
		}
		return existing, nil
	}
	if existingID, ok := node.receiptBySeq[request.Sequence]; ok && existingID != request.OperationID {
		return EffectReceipt{}, ErrSequenceUsed
	}
	receipt, err := h.validateControlLocked(request)
	if err != nil {
		return EffectReceipt{}, err
	}
	node.consumed = node.consumed.Union(SeqRangeSet(request.Sequence, request.Sequence))
	node.receipts[request.OperationID] = receipt
	node.receiptBySeq[request.Sequence] = request.OperationID
	h.operations[request.OperationID] = request.GrantID
	return receipt, nil
}

// RecordEffect updates the receiver outcome for an authorized operation.
func (h *Host) RecordEffect(receipt EffectReceipt) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	node, ok := h.nodes[receipt.GrantID]
	if !ok {
		return ErrGrantNotFound
	}
	existing, ok := node.receipts[receipt.OperationID]
	if !ok {
		return ErrReceiptConflict
	}
	if existing.ProcessStartID != receipt.ProcessStartID ||
		existing.SessionRoot != receipt.SessionRoot ||
		existing.Sequence != receipt.Sequence ||
		existing.Signal != receipt.Signal ||
		!existing.Dimensions.Equal(receipt.Dimensions) {
		return ErrReceiptConflict
	}
	if !validEffectTransition(existing.Status, receipt.Status) {
		return ErrEffectTransition
	}
	if receipt.Status == existing.Status && receipt.Detail == existing.Detail {
		return nil
	}
	existing.Status = receipt.Status
	existing.Detail = receipt.Detail
	existing.UpdatedAt = h.now()
	node.receipts[receipt.OperationID] = existing
	return nil
}

func validEffectTransition(from, to EffectStatus) bool {
	if from == to {
		return true
	}
	switch from {
	case EffectPending, EffectUnknown:
		return to == EffectApplied || to == EffectRejected || to == EffectNotApplied || to == EffectUnknown
	default:
		return false
	}
}

// Receipt queries an effect receipt by operation ID.
func (h *Host) Receipt(operationID string) (EffectReceipt, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	grantID, ok := h.operations[operationID]
	if !ok {
		return EffectReceipt{}, false
	}
	node, ok := h.nodes[grantID]
	if !ok {
		return EffectReceipt{}, false
	}
	receipt, ok := node.receipts[operationID]
	return cloneEffectReceipt(receipt), ok
}

// ReceiptForSequence queries the receipt attached to a consumed sequence.
func (h *Host) ReceiptForSequence(grantID GrantID, sequence uint64) (EffectReceipt, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	node, ok := h.nodes[grantID]
	if !ok {
		return EffectReceipt{}, false
	}
	operationID, ok := node.receiptBySeq[sequence]
	if !ok {
		return EffectReceipt{}, false
	}
	receipt, ok := node.receipts[operationID]
	return cloneEffectReceipt(receipt), ok
}

// ReceiptsForGrant returns all receipts for a grant in no particular order.
func (h *Host) ReceiptsForGrant(grantID GrantID) []EffectReceipt {
	h.mu.RLock()
	defer h.mu.RUnlock()
	node, ok := h.nodes[grantID]
	if !ok {
		return nil
	}
	out := make([]EffectReceipt, 0, len(node.receipts))
	for _, receipt := range node.receipts {
		out = append(out, cloneEffectReceipt(receipt))
	}
	return out
}

func cloneEffectReceipt(receipt EffectReceipt) EffectReceipt {
	receipt.Dimensions = receipt.Dimensions.Clone()
	return receipt
}

func (h *Host) nextNonce() string {
	h.nonceCounter++
	var raw [8]byte
	value := h.nonceCounter
	for i := range raw {
		raw[i] = byte(value)
		value >>= 8
	}
	return hex.EncodeToString(raw[:])
}

// Coverage returns the current host-state partition of universe. Active,
// unused sequences are controlled; consumed, returned, or sealed sequences are
// definitely invalid; every other sequence is still awaiting proof.
func (h *Host) Coverage(universe SeqSet) Partition {
	h.mu.RLock()
	defer h.mu.RUnlock()

	var controlled, invalid SeqSet
	for _, node := range h.nodes {
		active := node.activeScope()
		held := active.Sequences.Subtract(node.consumed).Subtract(node.sealed)
		controlled = controlled.Union(held)
		invalid = invalid.Union(node.consumed).
			Union(node.sealed).
			Union(node.returnedSeq)
	}
	controlled = controlled.Intersect(universe).Subtract(invalid)
	invalid = invalid.Intersect(universe)
	pending := universe.Subtract(controlled).Subtract(invalid)
	return Partition{
		Universe:   universe.Clone(),
		Controlled: controlled,
		Invalid:    invalid,
		Pending:    pending,
	}
}
