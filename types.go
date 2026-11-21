package roomdelivery

import (
	"errors"
	"fmt"
	"sort"
)

// RoomID identifies one routed room.
type RoomID string

// MembershipInstanceID identifies one concrete join. It is never reused, even
// when the same principal or display name returns to the room.
type MembershipInstanceID string

// PrincipalID is the public, room-scoped account identifier. It must not be
// used as the account's private real JID.
type PrincipalID string

// RealJID is the private account address that is disclosed only by an explicit
// identity reveal.
type RealJID string

// DisplayName is the reusable nickname shown inside the room.
type DisplayName string

// MessageID identifies the logical chat message shared by its delivery
// receipts.
type MessageID string

// ReceiptID identifies one recipient-scoped delivery receipt.
type ReceiptID string

// RevealID identifies one temporary identity grant.
type RevealID string

// RoomVersion is an authenticated, monotonically increasing room version.
type RoomVersion uint64

// SubjectGeneration is the authenticated generation of one principal. A
// rejoin allocates a new generation.
type SubjectGeneration uint64

// MembershipRef is the authenticated identity of one join instance. All
// mutations carry it so stale principals and stale joins cannot be confused.
type MembershipRef struct {
	RoomID     RoomID
	Instance   MembershipInstanceID
	Principal  PrincipalID
	Generation SubjectGeneration
}

// IsZero reports whether the reference has no useful content.
func (r MembershipRef) IsZero() bool {
	return r.RoomID == "" && r.Instance == "" && r.Principal == "" && r.Generation == 0
}

// IdentityKey identifies a principal incarnation for identity-disclosure
// decisions.
type IdentityKey struct {
	Principal  PrincipalID
	Generation SubjectGeneration
}

// IdentityRef points at an identity without requiring a live room lookup.
type IdentityRef struct {
	Principal  PrincipalID
	Generation SubjectGeneration
	Instance   MembershipInstanceID
}

// Key returns the generation-bound identity key.
func (r IdentityRef) Key() IdentityKey {
	return IdentityKey{Principal: r.Principal, Generation: r.Generation}
}

// IdentityPolicy is an immutable snapshot of what one recipient was allowed
// to see when a delivery receipt was created.
type IdentityPolicy struct {
	RoomVersion RoomVersion
	visible     map[IdentityKey][]RevealID
}

// Empty reports whether the snapshot grants no identity disclosure.
func (p IdentityPolicy) Empty() bool {
	return len(p.visible) == 0
}

// Allows reports whether ref was visible under this policy snapshot.
func (p IdentityPolicy) Allows(ref IdentityRef) bool {
	if ref.Principal == "" || ref.Generation == 0 {
		return false
	}
	_, ok := p.visible[ref.Key()]
	return ok
}

// AllowsKey reports whether key was visible under this policy snapshot.
func (p IdentityPolicy) AllowsKey(key IdentityKey) bool {
	return len(p.visible[key]) > 0
}

// RevealIDs returns the frozen grant identifiers for key in deterministic
// order.
func (p IdentityPolicy) RevealIDs(key IdentityKey) []RevealID {
	return append([]RevealID(nil), p.visible[key]...)
}

// Keys returns the disclosed identity keys in deterministic order.
func (p IdentityPolicy) Keys() []IdentityKey {
	keys := make([]IdentityKey, 0, len(p.visible))
	for key := range p.visible {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Principal != keys[j].Principal {
			return keys[i].Principal < keys[j].Principal
		}
		return keys[i].Generation < keys[j].Generation
	})
	return keys
}

func (p IdentityPolicy) clone() IdentityPolicy {
	visible := make(map[IdentityKey][]RevealID, len(p.visible))
	for key, revealIDs := range p.visible {
		visible[key] = append([]RevealID(nil), revealIDs...)
	}
	return IdentityPolicy{RoomVersion: p.RoomVersion, visible: visible}
}

// Attribute is a key/value message attribute. Identity, when set, is rendered
// through the receipt's policy instead of trusting Value.
type Attribute struct {
	Name     string
	Value    string
	Identity *IdentityRef
}

// Body is the structured message body. Text and attribute values are scrubbed
// of private real JIDs that the receipt cannot disclose.
type Body struct {
	Text       string
	Attributes []Attribute
}

// TextBody builds a body with public text only.
func TextBody(text string) Body {
	return Body{Text: text}
}

// WithAttribute appends a public attribute and returns the new body.
func (b Body) WithAttribute(name, value string) Body {
	out := b.clone()
	out.Attributes = append(out.Attributes, Attribute{Name: name, Value: value})
	return out
}

// WithIdentity appends an identity-bearing attribute and returns the new body.
func (b Body) WithIdentity(name string, ref IdentityRef) Body {
	identity := ref
	out := b.clone()
	out.Attributes = append(out.Attributes, Attribute{Name: name, Identity: &identity})
	return out
}

func (b Body) clone() Body {
	out := Body{Text: b.Text, Attributes: make([]Attribute, len(b.Attributes))}
	copy(out.Attributes, b.Attributes)
	for i := range out.Attributes {
		if out.Attributes[i].Identity != nil {
			identity := *out.Attributes[i].Identity
			out.Attributes[i].Identity = &identity
		}
	}
	return out
}

// Failure is an error-receipt payload. Subject is rendered under the same
// policy as the rest of the receipt.
type Failure struct {
	Code    string
	Text    string
	Subject *IdentityRef
}

func (f *Failure) clone() *Failure {
	if f == nil {
		return nil
	}
	out := *f
	if f.Subject != nil {
		subject := *f.Subject
		out.Subject = &subject
	}
	return &out
}

// ParticipantSummary is deliberately built only from public membership
// incarnations. It never derives counts from real JIDs.
type ParticipantSummary struct {
	JoinedInstances          int
	ActiveInstances          int
	DistinctActivePrincipals int
}

// MessageType is part of a delivery receipt's scope.
type MessageType string

const (
	MessageTypeBody             MessageType = "body"
	MessageTypeError            MessageType = "error"
	MessageTypeParticipantCount MessageType = "participant-count"
	MessageTypeSystem           MessageType = "system"
	MessageTypeCorrection       MessageType = "correction"
	MessageTypeRetraction       MessageType = "retraction"
)

func (m MessageType) String() string {
	if m == "" {
		return string(MessageTypeBody)
	}
	return string(m)
}

// EventKind names one authenticated public-history operation.
type EventKind string

const (
	EventJoined    EventKind = "joined"
	EventRenamed   EventKind = "renamed"
	EventKicked    EventKind = "kicked"
	EventRevealed  EventKind = "revealed"
	EventRevoked   EventKind = "revoked"
	EventMessage   EventKind = "message"
	EventCorrected EventKind = "corrected"
	EventRetracted EventKind = "retracted"
)

// Event is one authenticated room-history entry. RealJID is private room data
// and must not be copied into a recipient view except through rendering.
type Event struct {
	Seq            uint64
	Version        RoomVersion
	Kind           EventKind
	RoomID         RoomID
	Member         MembershipRef
	Recipient      MembershipRef
	DisplayName    DisplayName
	RealJID        RealJID
	RevealID       RevealID
	RevealIDs      []RevealID
	ExpiresVersion RoomVersion
	MessageID      MessageID
	MessageType    MessageType
	Body           Body
	Failure        *Failure
	Participants   *ParticipantSummary
	QuoteReceipts  []ReceiptID
	ReceiptIDs     []ReceiptID
}

func (e Event) clone() Event {
	out := e
	out.Body = e.Body.clone()
	out.Failure = e.Failure.clone()
	if e.Participants != nil {
		participants := *e.Participants
		out.Participants = &participants
	}
	out.ReceiptIDs = append([]ReceiptID(nil), e.ReceiptIDs...)
	out.QuoteReceipts = append([]ReceiptID(nil), e.QuoteReceipts...)
	out.RevealIDs = append([]RevealID(nil), e.RevealIDs...)
	return out
}

// ReceiptStatus is the publication state of one recipient-scoped receipt.
type ReceiptStatus string

const (
	ReceiptQueued        ReceiptStatus = "queued"
	ReceiptPublished     ReceiptStatus = "published"
	ReceiptBlocked       ReceiptStatus = "blocked"
	ReceiptUndeliverable ReceiptStatus = "undeliverable"
)

// Receipt binds one logical message to one concrete receiving instance, its
// message type, and the identity policy that existed at creation time.
type Receipt struct {
	ID               ReceiptID
	RoomID           RoomID
	MessageID        MessageID
	MessageType      MessageType
	Author           MembershipRef
	Target           MembershipRef
	Policy           IdentityPolicy
	QuoteReceipt     ReceiptID
	MessageEventSeq  uint64
	CreatedVersion   RoomVersion
	PublishedVersion RoomVersion
	PublishCount     int
	Status           ReceiptStatus
}

func (r Receipt) clone() Receipt {
	out := r
	out.Policy = r.Policy.clone()
	return out
}

// History is the shared authenticated room history plus the delivery-receipt
// log. RealJID values occur only in private join events.
type History struct {
	RoomID   RoomID
	Events   []Event
	Receipts []Receipt
}

func (h History) clone() History {
	out := History{RoomID: h.RoomID}
	out.Events = make([]Event, len(h.Events))
	for i, event := range h.Events {
		out.Events[i] = event.clone()
	}
	out.Receipts = make([]Receipt, len(h.Receipts))
	for i, receipt := range h.Receipts {
		out.Receipts[i] = receipt.clone()
	}
	return out
}

// WithRealJID returns a copy of the history with one private join address
// replaced. It is useful for checking that unauthorized address changes do not
// perturb a recipient's visible output.
func (h History) WithRealJID(instance MembershipInstanceID, realJID RealJID) History {
	out := h.clone()
	for i := range out.Events {
		if out.Events[i].Kind == EventJoined && out.Events[i].Member.Instance == instance {
			out.Events[i].RealJID = realJID
		}
	}
	return out
}

var (
	ErrEmptyRoomID        = errors.New("roomdelivery: room id is required")
	ErrEmptyPrincipal     = errors.New("roomdelivery: principal id is required")
	ErrEmptyInstance      = errors.New("roomdelivery: membership instance is required")
	ErrInstanceReused     = errors.New("roomdelivery: membership instance was already used")
	ErrUnknownMember      = errors.New("roomdelivery: unknown membership instance")
	ErrGenerationMismatch = errors.New("roomdelivery: subject generation mismatch")
	ErrStaleVersion       = errors.New("roomdelivery: stale room version")
	ErrUnauthenticated    = errors.New("roomdelivery: membership reference is not authenticated")
	ErrAlreadyJoined      = errors.New("roomdelivery: principal already has an active join")
	ErrNotActive          = errors.New("roomdelivery: membership is not active")
	ErrNotRejoinable      = errors.New("roomdelivery: membership cannot rejoin")
	ErrDisplayNameEmpty   = errors.New("roomdelivery: display name is required")
	ErrUnknownReceipt     = errors.New("roomdelivery: unknown receipt")
	ErrReceiptScope       = errors.New("roomdelivery: operation is outside the receipt scope")
	ErrUnknownMessage     = errors.New("roomdelivery: unknown message")
	ErrDuplicateMessage   = errors.New("roomdelivery: message id already exists")
	ErrTargetGone         = errors.New("roomdelivery: target join instance is no longer active")
	ErrIdentityRevoked    = errors.New("roomdelivery: queued identity disclosure was revoked")
	ErrReceiptBlocked     = errors.New("roomdelivery: receipt is blocked")
	ErrRetracted          = errors.New("roomdelivery: receipt was retracted")
	ErrUnknownReveal      = errors.New("roomdelivery: unknown identity reveal")
	ErrRevealIDReused     = errors.New("roomdelivery: reveal id was already used")
	ErrRevealExpired      = errors.New("roomdelivery: identity reveal expired")
	ErrQuoteScope         = errors.New("roomdelivery: quoted message has no receipt for this target")
	ErrInvalidFailure     = errors.New("roomdelivery: error receipt needs a failure payload")
	ErrEmptyTargets       = errors.New("roomdelivery: at least one target is required")
	ErrDuplicateTarget    = errors.New("roomdelivery: duplicate target")
	ErrMalformedHistory   = errors.New("roomdelivery: malformed history")
)

func wrap(prefix string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", prefix, err)
}
