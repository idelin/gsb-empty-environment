package roomdelivery

import (
	"fmt"
	"sort"
	"sync"
)

type memberState struct {
	ref           MembershipRef
	displayName   DisplayName
	realJID       RealJID
	joinedSeq     uint64
	joinedVersion RoomVersion
	leftVersion   RoomVersion
	active        bool
}

type revealState struct {
	id             RevealID
	subject        MembershipRef
	recipient      MembershipRef
	createdVersion RoomVersion
	expiresVersion RoomVersion
	revokedVersion RoomVersion
}

// Host is a concurrency-safe room-routing host with recipient-scoped delivery
// receipts.
type Host struct {
	mu sync.Mutex

	roomID RoomID

	version RoomVersion
	seq     uint64

	joinSeq    uint64
	receiptSeq uint64
	messageSeq uint64
	revealSeq  uint64

	members    map[MembershipInstanceID]*memberState
	active     map[PrincipalID]MembershipInstanceID
	generation map[PrincipalID]SubjectGeneration
	reveals    map[RevealID]*revealState
	messages   map[MessageID][]ReceiptID

	events   []Event
	receipts map[ReceiptID]*Receipt
}

// Room is an alias for Host. Both names describe the same room-routing role.
type Room = Host

// NewHost creates an empty room at version zero.
func NewHost(roomID RoomID) *Host {
	return &Host{
		roomID:     roomID,
		members:    make(map[MembershipInstanceID]*memberState),
		active:     make(map[PrincipalID]MembershipInstanceID),
		generation: make(map[PrincipalID]SubjectGeneration),
		reveals:    make(map[RevealID]*revealState),
		messages:   make(map[MessageID][]ReceiptID),
		receipts:   make(map[ReceiptID]*Receipt),
	}
}

// NewRoom creates an empty room at version zero.
func NewRoom(roomID RoomID) *Room {
	return NewHost(roomID)
}

// JoinRequest begins a new join instance for a principal. An inactive
// principal may use Join to return with an entirely new instance.
type JoinRequest struct {
	Principal       PrincipalID
	DisplayName     DisplayName
	RealJID         RealJID
	InstanceID      MembershipInstanceID
	ExpectedVersion RoomVersion
}

// RejoinRequest begins a fresh join instance after a prior instance ended.
// PreviousInstance, when supplied, must identify that ended instance.
type RejoinRequest struct {
	Principal        PrincipalID
	PreviousInstance MembershipInstanceID
	DisplayName      DisplayName
	RealJID          RealJID
	InstanceID       MembershipInstanceID
	ExpectedVersion  RoomVersion
}

// RenameRequest changes the reusable nickname of one concrete join instance.
type RenameRequest struct {
	Member          MembershipRef
	DisplayName     DisplayName
	ExpectedVersion RoomVersion
}

// KickRequest ends one concrete join instance.
type KickRequest struct {
	Member          MembershipRef
	ExpectedVersion RoomVersion
}

// RevealRequest grants one recipient instance temporary visibility of one
// subject generation's private JID. ExpiresVersion is exclusive; zero means no
// scheduled expiry.
type RevealRequest struct {
	Subject         MembershipRef
	Recipient       MembershipRef
	RevealID        RevealID
	ExpiresVersion  RoomVersion
	ExpectedVersion RoomVersion
}

// RevokeRequest revokes one or more matching reveals. An empty RevealID
// revokes every matching subject/recipient grant.
type RevokeRequest struct {
	Subject         MembershipRef
	Recipient       MembershipRef
	RevealID        RevealID
	ExpectedVersion RoomVersion
}

// SendRequest creates one logical message and one receipt per target. The
// same message id may therefore be used for fan-out without sharing policy.
type SendRequest struct {
	Author          MembershipRef
	Targets         []MembershipRef
	MessageID       MessageID
	MessageType     MessageType
	Body            Body
	Failure         *Failure
	QuoteMessage    MessageID
	QuoteReceipt    ReceiptID
	ExpectedVersion RoomVersion
}

// Dispatch is the result of Send.
type Dispatch struct {
	MessageID MessageID
	Receipts  []Receipt
}

// CorrectRequest applies one correction to the exact receipt scopes named by
// ReceiptIDs or, when omitted, every receipt of MessageID.
type CorrectRequest struct {
	Author          MembershipRef
	ReceiptIDs      []ReceiptID
	MessageID       MessageID
	Body            Body
	ExpectedVersion RoomVersion
}

// RetractRequest retracts the exact receipt scopes named by ReceiptIDs or,
// when omitted, every receipt of MessageID.
type RetractRequest struct {
	Author          MembershipRef
	ReceiptIDs      []ReceiptID
	MessageID       MessageID
	ExpectedVersion RoomVersion
}

// RoomID returns the hosted room identifier.
func (h *Host) RoomID() RoomID {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.roomID
}

// Version returns the current authenticated room version.
func (h *Host) Version() RoomVersion {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.version
}

// Join starts a fresh join instance.
func (h *Host) Join(req JoinRequest) (MembershipRef, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.roomID == "" {
		return MembershipRef{}, ErrEmptyRoomID
	}
	if req.Principal == "" {
		return MembershipRef{}, ErrEmptyPrincipal
	}
	if req.DisplayName == "" {
		return MembershipRef{}, ErrDisplayNameEmpty
	}
	if req.InstanceID != "" {
		if _, exists := h.members[req.InstanceID]; exists {
			return MembershipRef{}, ErrInstanceReused
		}
	}
	if _, ok := h.active[req.Principal]; ok {
		return MembershipRef{}, ErrAlreadyJoined
	}
	if err := h.checkExpectedLocked(req.ExpectedVersion); err != nil {
		return MembershipRef{}, err
	}
	return h.joinLocked(req.Principal, req.DisplayName, req.RealJID, req.InstanceID), nil
}

// Rejoin starts a fresh join instance after the prior instance has ended.
func (h *Host) Rejoin(req RejoinRequest) (MembershipRef, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.roomID == "" {
		return MembershipRef{}, ErrEmptyRoomID
	}
	if req.Principal == "" {
		return MembershipRef{}, ErrEmptyPrincipal
	}
	if req.DisplayName == "" {
		return MembershipRef{}, ErrDisplayNameEmpty
	}
	if req.InstanceID != "" {
		if _, exists := h.members[req.InstanceID]; exists {
			return MembershipRef{}, ErrInstanceReused
		}
	}
	if _, ok := h.active[req.Principal]; ok {
		return MembershipRef{}, ErrAlreadyJoined
	}
	if req.PreviousInstance != "" {
		previous, ok := h.members[req.PreviousInstance]
		if !ok || previous.ref.Principal != req.Principal || previous.active {
			return MembershipRef{}, ErrNotRejoinable
		}
	} else if _, ok := h.generation[req.Principal]; !ok {
		return MembershipRef{}, ErrNotRejoinable
	}
	if err := h.checkExpectedLocked(req.ExpectedVersion); err != nil {
		return MembershipRef{}, err
	}
	return h.joinLocked(req.Principal, req.DisplayName, req.RealJID, req.InstanceID), nil
}

func (h *Host) joinLocked(principal PrincipalID, displayName DisplayName, realJID RealJID, requested MembershipInstanceID) MembershipRef {
	generation := h.generation[principal] + 1
	h.generation[principal] = generation

	h.joinSeq++
	instance := requested
	if instance == "" {
		for {
			candidate := MembershipInstanceID(fmt.Sprintf("join-%d", h.joinSeq))
			if _, exists := h.members[candidate]; !exists {
				instance = candidate
				break
			}
			h.joinSeq++
		}
	}

	ref := MembershipRef{
		RoomID:     h.roomID,
		Instance:   instance,
		Principal:  principal,
		Generation: generation,
	}
	h.version++
	h.seq++
	h.members[instance] = &memberState{
		ref:           ref,
		displayName:   displayName,
		realJID:       realJID,
		joinedSeq:     h.seq,
		joinedVersion: h.version,
		active:        true,
	}
	h.active[principal] = instance
	h.events = append(h.events, Event{
		Seq:         h.seq,
		Version:     h.version,
		Kind:        EventJoined,
		RoomID:      h.roomID,
		Member:      ref,
		DisplayName: displayName,
		RealJID:     realJID,
	})
	return ref
}

// Rename changes a nickname without changing the join instance. The same name
// may be used by any number of instances.
func (h *Host) Rename(req RenameRequest) (MembershipRef, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if req.DisplayName == "" {
		return MembershipRef{}, ErrDisplayNameEmpty
	}
	if err := h.checkExpectedLocked(req.ExpectedVersion); err != nil {
		return MembershipRef{}, err
	}
	member, err := h.resolveActiveLocked(req.Member)
	if err != nil {
		return MembershipRef{}, err
	}

	h.version++
	h.seq++
	member.displayName = req.DisplayName
	h.events = append(h.events, Event{
		Seq:         h.seq,
		Version:     h.version,
		Kind:        EventRenamed,
		RoomID:      h.roomID,
		Member:      member.ref,
		DisplayName: req.DisplayName,
	})
	return member.ref, nil
}

// Kick ends one concrete join instance.
func (h *Host) Kick(req KickRequest) (MembershipRef, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if err := h.checkExpectedLocked(req.ExpectedVersion); err != nil {
		return MembershipRef{}, err
	}
	member, err := h.resolveActiveLocked(req.Member)
	if err != nil {
		return MembershipRef{}, err
	}

	h.version++
	h.seq++
	member.active = false
	member.leftVersion = h.version
	delete(h.active, member.ref.Principal)
	h.events = append(h.events, Event{
		Seq:     h.seq,
		Version: h.version,
		Kind:    EventKicked,
		RoomID:  h.roomID,
		Member:  member.ref,
	})
	return member.ref, nil
}

// Reveal grants one recipient instance temporary visibility of a subject
// generation.
func (h *Host) Reveal(req RevealRequest) (RevealID, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if err := h.checkExpectedLocked(req.ExpectedVersion); err != nil {
		return "", err
	}
	subject, err := h.resolveActiveLocked(req.Subject)
	if err != nil {
		return "", wrap("reveal subject", err)
	}
	recipient, err := h.resolveActiveLocked(req.Recipient)
	if err != nil {
		return "", wrap("reveal recipient", err)
	}

	nextVersion := h.version + 1
	if req.ExpiresVersion != 0 && req.ExpiresVersion <= nextVersion {
		return "", ErrRevealExpired
	}

	id := req.RevealID
	if id == "" {
		for {
			h.revealSeq++
			candidate := RevealID(fmt.Sprintf("reveal-%d", h.revealSeq))
			if _, exists := h.reveals[candidate]; !exists {
				id = candidate
				break
			}
		}
	} else if _, exists := h.reveals[id]; exists {
		return "", wrap("reveal id", ErrRevealIDReused)
	}

	h.version++
	h.seq++
	h.reveals[id] = &revealState{
		id:             id,
		subject:        subject.ref,
		recipient:      recipient.ref,
		createdVersion: h.version,
		expiresVersion: req.ExpiresVersion,
	}
	h.events = append(h.events, Event{
		Seq:            h.seq,
		Version:        h.version,
		Kind:           EventRevealed,
		RoomID:         h.roomID,
		Member:         subject.ref,
		Recipient:      recipient.ref,
		RevealID:       id,
		ExpiresVersion: req.ExpiresVersion,
	})
	return id, nil
}

// Revoke revokes identity visibility. The number of revoked grants is
// returned.
func (h *Host) Revoke(req RevokeRequest) (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if err := h.checkExpectedLocked(req.ExpectedVersion); err != nil {
		return 0, err
	}
	subject, err := h.resolveKnownLocked(req.Subject)
	if err != nil {
		return 0, wrap("revoke subject", err)
	}
	recipient, err := h.resolveKnownLocked(req.Recipient)
	if err != nil {
		return 0, wrap("revoke recipient", err)
	}

	matches := make([]*revealState, 0, 1)
	for _, reveal := range h.reveals {
		if reveal.revokedVersion != 0 {
			continue
		}
		if req.RevealID != "" && reveal.id != req.RevealID {
			continue
		}
		if reveal.subject != subject.ref || reveal.recipient != recipient.ref {
			continue
		}
		matches = append(matches, reveal)
	}
	if len(matches) == 0 {
		if req.RevealID != "" {
			return 0, ErrUnknownReveal
		}
		return 0, ErrUnknownReveal
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].id < matches[j].id })

	h.version++
	h.seq++
	for _, reveal := range matches {
		reveal.revokedVersion = h.version
	}
	eventRevealID := req.RevealID
	revokedIDs := make([]RevealID, 0, len(matches))
	for _, reveal := range matches {
		revokedIDs = append(revokedIDs, reveal.id)
	}
	if len(matches) == 1 {
		eventRevealID = matches[0].id
	}
	h.events = append(h.events, Event{
		Seq:       h.seq,
		Version:   h.version,
		Kind:      EventRevoked,
		RoomID:    h.roomID,
		Member:    subject.ref,
		Recipient: recipient.ref,
		RevealID:  eventRevealID,
		RevealIDs: revokedIDs,
	})
	return len(matches), nil
}

// Send creates one message and one receipt per target.
func (h *Host) Send(req SendRequest) (Dispatch, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if len(req.Targets) == 0 {
		return Dispatch{}, ErrEmptyTargets
	}
	if err := h.checkExpectedLocked(req.ExpectedVersion); err != nil {
		return Dispatch{}, err
	}
	author, err := h.resolveActiveLocked(req.Author)
	if err != nil {
		return Dispatch{}, wrap("send author", err)
	}

	messageType := req.MessageType
	if messageType == "" {
		messageType = MessageTypeBody
	}
	switch messageType {
	case MessageTypeCorrection, MessageTypeRetraction:
		return Dispatch{}, wrap("message type", ErrReceiptScope)
	}
	if messageType == MessageTypeError && req.Failure == nil {
		return Dispatch{}, ErrInvalidFailure
	}

	targets := make([]*memberState, len(req.Targets))
	seen := make(map[MembershipInstanceID]struct{}, len(req.Targets))
	for i, targetRef := range req.Targets {
		target, err := h.resolveActiveLocked(targetRef)
		if err != nil {
			return Dispatch{}, wrap("send target", err)
		}
		if _, exists := seen[target.ref.Instance]; exists {
			return Dispatch{}, ErrDuplicateTarget
		}
		seen[target.ref.Instance] = struct{}{}
		targets[i] = target
	}

	messageID := req.MessageID
	if messageID == "" {
		for {
			h.messageSeq++
			candidate := MessageID(fmt.Sprintf("message-%d", h.messageSeq))
			if _, exists := h.messages[candidate]; !exists {
				messageID = candidate
				break
			}
		}
	} else if _, exists := h.messages[messageID]; exists {
		return Dispatch{}, ErrDuplicateMessage
	}

	quoteReceipts := make([]ReceiptID, len(targets))
	for i, target := range targets {
		quoteReceipt, err := h.resolveQuoteLocked(req, messageID, target)
		if err != nil {
			return Dispatch{}, err
		}
		quoteReceipts[i] = quoteReceipt
	}

	nextVersion := h.version + 1
	receipts := make([]Receipt, len(targets))
	receiptIDs := make([]ReceiptID, len(targets))
	for i, target := range targets {
		h.receiptSeq++
		receiptID := ReceiptID(fmt.Sprintf("receipt-%d", h.receiptSeq))
		receipt := Receipt{
			ID:             receiptID,
			RoomID:         h.roomID,
			MessageID:      messageID,
			MessageType:    messageType,
			Author:         author.ref,
			Target:         target.ref,
			Policy:         h.policyAtLocked(target.ref, nextVersion),
			QuoteReceipt:   quoteReceipts[i],
			CreatedVersion: nextVersion,
			Status:         ReceiptQueued,
		}
		receipts[i] = receipt
		receiptIDs[i] = receiptID
	}

	var participants *ParticipantSummary
	if messageType == MessageTypeParticipantCount {
		summary := h.summaryLocked()
		participants = &summary
	}

	h.version = nextVersion
	h.seq++
	messageSeq := h.seq
	h.events = append(h.events, Event{
		Seq:           h.seq,
		Version:       h.version,
		Kind:          EventMessage,
		RoomID:        h.roomID,
		Member:        author.ref,
		MessageID:     messageID,
		MessageType:   messageType,
		Body:          req.Body.clone(),
		Failure:       req.Failure.clone(),
		Participants:  participants,
		QuoteReceipts: append([]ReceiptID(nil), quoteReceipts...),
		ReceiptIDs:    append([]ReceiptID(nil), receiptIDs...),
	})
	for i := range receipts {
		receipts[i].MessageEventSeq = messageSeq
		stored := receipts[i]
		h.receipts[receipts[i].ID] = &stored
		h.messages[messageID] = append(h.messages[messageID], receipts[i].ID)
	}

	return Dispatch{MessageID: messageID, Receipts: cloneReceipts(receipts)}, nil
}

// SendTo is a single-target convenience wrapper around Send.
func (h *Host) SendTo(req SendRequest, target MembershipRef) (Receipt, error) {
	req.Targets = []MembershipRef{target}
	dispatch, err := h.Send(req)
	if err != nil {
		return Receipt{}, err
	}
	return dispatch.Receipts[0], nil
}

func (h *Host) resolveQuoteLocked(req SendRequest, newMessage MessageID, target *memberState) (ReceiptID, error) {
	if req.QuoteMessage == "" && req.QuoteReceipt == "" {
		return "", nil
	}
	if req.MessageType != "" && req.MessageType != MessageTypeBody {
		return "", ErrQuoteScope
	}
	if req.QuoteReceipt != "" {
		if len(req.Targets) != 1 {
			return "", wrap("quote receipt", ErrQuoteScope)
		}
		receipt, ok := h.receipts[req.QuoteReceipt]
		if !ok {
			return "", ErrUnknownReceipt
		}
		if receipt.Target != target.ref || receipt.MessageID == newMessage || receipt.Status != ReceiptPublished || receipt.MessageType != MessageTypeBody {
			return "", ErrQuoteScope
		}
		if req.QuoteMessage != "" && receipt.MessageID != req.QuoteMessage {
			return "", ErrQuoteScope
		}
		return receipt.ID, nil
	}

	receiptIDs := h.messages[req.QuoteMessage]
	for _, receiptID := range receiptIDs {
		receipt := h.receipts[receiptID]
		if receipt != nil && receipt.Target == target.ref && receipt.Status == ReceiptPublished && receipt.MessageType == MessageTypeBody {
			return receipt.ID, nil
		}
	}
	return "", ErrQuoteScope
}

// Correct applies one authenticated correction event to exact receipt scopes.
func (h *Host) Correct(req CorrectRequest) ([]ReceiptID, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if err := h.checkExpectedLocked(req.ExpectedVersion); err != nil {
		return nil, err
	}
	author, err := h.resolveActiveLocked(req.Author)
	if err != nil {
		return nil, wrap("correct author", err)
	}
	receiptIDs, err := h.resolveScopeLocked(author.ref, req.ReceiptIDs, req.MessageID)
	if err != nil {
		return nil, err
	}
	for _, receiptID := range receiptIDs {
		if h.isRetractedLocked(receiptID) {
			return nil, wrap(string(receiptID), ErrRetracted)
		}
	}

	h.version++
	h.seq++
	h.events = append(h.events, Event{
		Seq:         h.seq,
		Version:     h.version,
		Kind:        EventCorrected,
		RoomID:      h.roomID,
		Member:      author.ref,
		MessageType: MessageTypeCorrection,
		Body:        req.Body.clone(),
		ReceiptIDs:  append([]ReceiptID(nil), receiptIDs...),
	})
	return append([]ReceiptID(nil), receiptIDs...), nil
}

// Retract applies one authenticated retraction event to exact receipt scopes.
func (h *Host) Retract(req RetractRequest) ([]ReceiptID, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if err := h.checkExpectedLocked(req.ExpectedVersion); err != nil {
		return nil, err
	}
	author, err := h.resolveActiveLocked(req.Author)
	if err != nil {
		return nil, wrap("retract author", err)
	}
	receiptIDs, err := h.resolveScopeLocked(author.ref, req.ReceiptIDs, req.MessageID)
	if err != nil {
		return nil, err
	}
	already := true
	for _, receiptID := range receiptIDs {
		if !h.isRetractedLocked(receiptID) {
			already = false
			break
		}
	}
	if already {
		return nil, ErrRetracted
	}

	h.version++
	h.seq++
	h.events = append(h.events, Event{
		Seq:         h.seq,
		Version:     h.version,
		Kind:        EventRetracted,
		RoomID:      h.roomID,
		Member:      author.ref,
		MessageType: MessageTypeRetraction,
		ReceiptIDs:  append([]ReceiptID(nil), receiptIDs...),
	})
	return append([]ReceiptID(nil), receiptIDs...), nil
}

func (h *Host) resolveScopeLocked(author MembershipRef, requested []ReceiptID, messageID MessageID) ([]ReceiptID, error) {
	ids := append([]ReceiptID(nil), requested...)
	if len(ids) == 0 {
		if messageID == "" {
			return nil, ErrUnknownReceipt
		}
		ids = append(ids, h.messages[messageID]...)
		if len(ids) == 0 {
			return nil, ErrUnknownMessage
		}
	}

	seen := make(map[ReceiptID]struct{}, len(ids))
	out := make([]ReceiptID, 0, len(ids))
	for _, id := range ids {
		if _, exists := seen[id]; exists {
			continue
		}
		receipt, ok := h.receipts[id]
		if !ok {
			return nil, wrap(string(id), ErrUnknownReceipt)
		}
		if receipt.Author != author {
			return nil, wrap(string(id), ErrReceiptScope)
		}
		if messageID != "" && receipt.MessageID != messageID {
			return nil, wrap(string(id), ErrReceiptScope)
		}
		if receipt.MessageType == MessageTypeCorrection || receipt.MessageType == MessageTypeRetraction {
			return nil, wrap(string(id), ErrReceiptScope)
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	if len(out) == 0 {
		return nil, ErrUnknownReceipt
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

// Publish releases one queued receipt. Identity-bearing content is checked
// against the live policy so a revoked reveal cannot become effective late.
func (h *Host) Publish(receiptID ReceiptID) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := h.publishLocked(receiptID)
	return err
}

// PublishMessage publishes every receipt of a logical message and returns the
// number published. The first stale identity receipt blocks the whole call.
func (h *Host) PublishMessage(messageID MessageID) (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	ids := append([]ReceiptID(nil), h.messages[messageID]...)
	if len(ids) == 0 {
		return 0, ErrUnknownMessage
	}
	published := 0
	for _, id := range ids {
		ok, err := h.publishLocked(id)
		if err != nil {
			return published, err
		}
		if ok {
			published++
		}
	}
	return published, nil
}

func (h *Host) publishLocked(receiptID ReceiptID) (bool, error) {
	receipt, ok := h.receipts[receiptID]
	if !ok {
		return false, ErrUnknownReceipt
	}
	if receipt.Status == ReceiptBlocked {
		return false, ErrReceiptBlocked
	}
	target, err := h.resolveActiveLocked(receipt.Target)
	if err != nil {
		receipt.Status = ReceiptUndeliverable
		return false, wrap("publish target", ErrTargetGone)
	}
	if target.ref != receipt.Target {
		receipt.Status = ReceiptUndeliverable
		return false, ErrTargetGone
	}

	if !h.isRetractedLocked(receipt.ID) {
		current := h.policyAtLocked(receipt.Target, h.version)
		for key, expected := range h.disclosedGrantsLocked(receipt) {
			if !sharesReveal(current.RevealIDs(key), expected) {
				receipt.Status = ReceiptBlocked
				return false, wrap(string(key.Principal), ErrIdentityRevoked)
			}
		}
	}

	receipt.PublishedVersion = h.version
	receipt.PublishCount++
	receipt.Status = ReceiptPublished
	return true, nil
}

func sharesReveal(current []RevealID, expected map[RevealID]struct{}) bool {
	for _, id := range current {
		if _, ok := expected[id]; ok {
			return true
		}
	}
	return false
}

// Policy returns the current identity-disclosure snapshot for one active
// receiving instance.
func (h *Host) Policy(member MembershipRef) (IdentityPolicy, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	target, err := h.resolveActiveLocked(member)
	if err != nil {
		return IdentityPolicy{}, err
	}
	return h.policyAtLocked(target.ref, h.version), nil
}

// Participants returns a count-only, public-identifier summary.
func (h *Host) Participants() ParticipantSummary {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.summaryLocked()
}

// Receipt returns a copy of one receipt.
func (h *Host) Receipt(receiptID ReceiptID) (Receipt, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	receipt, ok := h.receipts[receiptID]
	if !ok {
		return Receipt{}, false
	}
	return receipt.clone(), true
}

// Receipts returns deterministic copies of all receipts.
func (h *Host) Receipts() []Receipt {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.receiptsLocked()
}

// History returns a deep copy of the shared authenticated history and receipt
// log. Join events contain private RealJID values.
func (h *Host) History() History {
	h.mu.Lock()
	defer h.mu.Unlock()
	history := History{
		RoomID:   h.roomID,
		Events:   make([]Event, len(h.events)),
		Receipts: h.receiptsLocked(),
	}
	for i, event := range h.events {
		history.Events[i] = event.clone()
	}
	return history
}

// RebuildRecipientView reconstructs one recipient's view from the host's
// current shared history.
func (h *Host) RebuildRecipientView(viewer MembershipRef) (RecipientView, error) {
	return RebuildRecipientView(h.History(), viewer)
}

// View is an alias for RebuildRecipientView.
func (h *Host) View(viewer MembershipRef) (RecipientView, error) {
	return h.RebuildRecipientView(viewer)
}

func (h *Host) receiptsLocked() []Receipt {
	ids := make([]ReceiptID, 0, len(h.receipts))
	for id := range h.receipts {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	out := make([]Receipt, len(ids))
	for i, id := range ids {
		out[i] = h.receipts[id].clone()
	}
	return out
}

func (h *Host) summaryLocked() ParticipantSummary {
	summary := ParticipantSummary{JoinedInstances: len(h.members), ActiveInstances: len(h.active)}
	principals := make(map[PrincipalID]struct{}, len(h.active))
	for principal := range h.active {
		principals[principal] = struct{}{}
	}
	summary.DistinctActivePrincipals = len(principals)
	return summary
}

func (h *Host) checkExpectedLocked(expected RoomVersion) error {
	if expected != 0 && expected != h.version {
		return ErrStaleVersion
	}
	return nil
}

func (h *Host) resolveKnownLocked(ref MembershipRef) (*memberState, error) {
	if ref.Instance == "" {
		return nil, ErrEmptyInstance
	}
	member, ok := h.members[ref.Instance]
	if !ok {
		return nil, ErrUnknownMember
	}
	if ref.RoomID != "" && ref.RoomID != h.roomID {
		return nil, ErrUnauthenticated
	}
	if ref.Principal != "" && ref.Principal != member.ref.Principal {
		return nil, ErrUnauthenticated
	}
	if ref.Generation != 0 && ref.Generation != member.ref.Generation {
		return nil, ErrGenerationMismatch
	}
	return member, nil
}

func (h *Host) resolveActiveLocked(ref MembershipRef) (*memberState, error) {
	member, err := h.resolveKnownLocked(ref)
	if err != nil {
		return nil, err
	}
	if !member.active {
		return nil, ErrNotActive
	}
	return member, nil
}

func (h *Host) policyAtLocked(recipient MembershipRef, at RoomVersion) IdentityPolicy {
	grants := make(map[IdentityKey]map[RevealID]struct{})
	for _, reveal := range h.reveals {
		if reveal.recipient != recipient {
			continue
		}
		if reveal.createdVersion > at {
			continue
		}
		if reveal.expiresVersion != 0 && at >= reveal.expiresVersion {
			continue
		}
		if reveal.revokedVersion != 0 && reveal.revokedVersion <= at {
			continue
		}
		key := reveal.subject.identityKey()
		if grants[key] == nil {
			grants[key] = make(map[RevealID]struct{})
		}
		grants[key][reveal.id] = struct{}{}
	}
	visible := make(map[IdentityKey][]RevealID, len(grants))
	for key, ids := range grants {
		revealIDs := make([]RevealID, 0, len(ids))
		for id := range ids {
			revealIDs = append(revealIDs, id)
		}
		sort.Slice(revealIDs, func(i, j int) bool { return revealIDs[i] < revealIDs[j] })
		visible[key] = revealIDs
	}
	return IdentityPolicy{RoomVersion: at, visible: visible}
}

func (r MembershipRef) identityKey() IdentityKey {
	return IdentityKey{Principal: r.Principal, Generation: r.Generation}
}

func cloneReceipts(receipts []Receipt) []Receipt {
	out := make([]Receipt, len(receipts))
	for i, receipt := range receipts {
		out[i] = receipt.clone()
	}
	return out
}
