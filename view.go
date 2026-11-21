package roomdelivery

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"regexp"
	"sort"
	"strings"
)

// VisibleIdentity is the only identity shape a recipient view may contain.
// JID is populated only when the relevant receipt policy discloses it.
type VisibleIdentity struct {
	DisplayName DisplayName          `json:"display_name,omitempty"`
	Principal   PrincipalID          `json:"principal"`
	Generation  SubjectGeneration    `json:"generation"`
	Instance    MembershipInstanceID `json:"instance,omitempty"`
	Alias       string               `json:"alias"`
	JID         RealJID              `json:"jid,omitempty"`
}

// VisibleAttribute preserves an attribute without exposing an undisclosed real
// JID in its value.
type VisibleAttribute struct {
	Name     string           `json:"name"`
	Value    string           `json:"value"`
	Identity *VisibleIdentity `json:"identity,omitempty"`
}

// VisibleBody is a policy-rendered message body.
type VisibleBody struct {
	Text       string             `json:"text,omitempty"`
	Attributes []VisibleAttribute `json:"attributes,omitempty"`
}

// VisibleFailure is a policy-rendered error receipt payload.
type VisibleFailure struct {
	Code    string           `json:"code,omitempty"`
	Text    string           `json:"text,omitempty"`
	Subject *VisibleIdentity `json:"subject,omitempty"`
}

// VisibleQuote is a frozen quote relationship. It is unavailable when the
// quoted receipt was not published to this viewer or belongs to another
// receive instance.
type VisibleQuote struct {
	ReceiptID ReceiptID        `json:"receipt_id"`
	MessageID MessageID        `json:"message_id,omitempty"`
	Available bool             `json:"available"`
	Retracted bool             `json:"retracted,omitempty"`
	Author    *VisibleIdentity `json:"author,omitempty"`
	Body      *VisibleBody     `json:"body,omitempty"`
	Failure   *VisibleFailure  `json:"failure,omitempty"`
	Quote     *VisibleQuote    `json:"quote,omitempty"`
}

// ViewEntry is one published receipt as seen by its exact target instance.
type ViewEntry struct {
	ReceiptID        ReceiptID           `json:"receipt_id"`
	MessageID        MessageID           `json:"message_id"`
	MessageType      MessageType         `json:"message_type"`
	Author           VisibleIdentity     `json:"author"`
	Body             VisibleBody         `json:"body"`
	Failure          *VisibleFailure     `json:"failure,omitempty"`
	Quote            *VisibleQuote       `json:"quote,omitempty"`
	Participants     *ParticipantSummary `json:"participants,omitempty"`
	Retracted        bool                `json:"retracted,omitempty"`
	Corrected        bool                `json:"corrected,omitempty"`
	OrderSeq         uint64              `json:"order_seq"`
	CreatedVersion   RoomVersion         `json:"created_version"`
	PublishedVersion RoomVersion         `json:"published_version"`
}

// RecipientView is a deterministic, recipient-specific projection of shared
// history. It contains no hidden room state.
type RecipientView struct {
	RoomID       RoomID             `json:"room_id"`
	Viewer       VisibleIdentity    `json:"viewer"`
	Entries      []ViewEntry        `json:"entries"`
	Participants ParticipantSummary `json:"participants"`
}

// CanonicalJSON encodes a view for byte-for-byte output comparison.
func CanonicalJSON(view RecipientView) ([]byte, error) {
	return json.Marshal(view)
}

type displaySnapshot struct {
	seq  uint64
	name DisplayName
}

type historyMember struct {
	ref     MembershipRef
	realJID RealJID
	names   []displaySnapshot
	active  bool
}

type historyState struct {
	roomID RoomID

	events       []Event
	eventsBySeq  map[uint64]Event
	eventOrder   map[ReceiptID][]Event
	receipts     map[ReceiptID]Receipt
	members      map[MembershipInstanceID]*historyMember
	membersByKey map[IdentityKey]*historyMember
	active       map[PrincipalID]MembershipInstanceID

	maxVersion RoomVersion
	maxSeq     uint64
}

// RebuildRecipientView is the independent recipient-view reconstruction entry
// point.
func RebuildRecipientView(history History, viewer MembershipRef) (RecipientView, error) {
	state, err := buildHistoryState(history)
	if err != nil {
		return RecipientView{}, err
	}
	viewerMember, err := state.resolveViewer(viewer)
	if err != nil {
		return RecipientView{}, err
	}

	view := RecipientView{
		RoomID:       state.roomID,
		Viewer:       state.visibleMember(viewerMember.ref, state.maxSeq, IdentityPolicy{}),
		Participants: state.participants(),
	}
	view.Viewer.JID = ""

	ids := make([]ReceiptID, 0, len(state.receipts))
	for id, receipt := range state.receipts {
		if receipt.Target != viewerMember.ref || receipt.Status != ReceiptPublished {
			continue
		}
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		left := state.receipts[ids[i]]
		right := state.receipts[ids[j]]
		if left.MessageEventSeq != right.MessageEventSeq {
			return left.MessageEventSeq < right.MessageEventSeq
		}
		return left.ID < right.ID
	})

	view.Entries = make([]ViewEntry, 0, len(ids))
	for _, id := range ids {
		entry, err := state.renderReceipt(state.receipts[id], viewerMember.ref, make(map[ReceiptID]bool))
		if err != nil {
			return RecipientView{}, err
		}
		view.Entries = append(view.Entries, entry)
	}
	return view, nil
}

// ReconstructRecipientView is an alias for RebuildRecipientView.
func ReconstructRecipientView(history History, viewer MembershipRef) (RecipientView, error) {
	return RebuildRecipientView(history, viewer)
}

// BuildRecipientView is an alias for RebuildRecipientView.
func BuildRecipientView(history History, viewer MembershipRef) (RecipientView, error) {
	return RebuildRecipientView(history, viewer)
}

func buildHistoryState(history History) (*historyState, error) {
	state := &historyState{
		roomID:       history.RoomID,
		events:       make([]Event, 0, len(history.Events)),
		eventsBySeq:  make(map[uint64]Event, len(history.Events)),
		eventOrder:   make(map[ReceiptID][]Event),
		receipts:     make(map[ReceiptID]Receipt, len(history.Receipts)),
		members:      make(map[MembershipInstanceID]*historyMember),
		membersByKey: make(map[IdentityKey]*historyMember),
		active:       make(map[PrincipalID]MembershipInstanceID),
	}

	for _, original := range history.Events {
		event := original.clone()
		if event.Seq == 0 {
			return nil, wrap("event sequence", ErrMalformedHistory)
		}
		if event.Seq <= state.maxSeq {
			return nil, wrap("event sequence is not increasing", ErrMalformedHistory)
		}
		state.maxSeq = event.Seq
		if event.RoomID != "" && history.RoomID != "" && event.RoomID != history.RoomID {
			return nil, wrap("event room mismatch", ErrMalformedHistory)
		}
		if event.Version < state.maxVersion {
			return nil, wrap("event version moved backwards", ErrMalformedHistory)
		}
		state.maxVersion = event.Version
		state.events = append(state.events, event)
		state.eventsBySeq[event.Seq] = event

		switch event.Kind {
		case EventJoined:
			if event.Member.Instance == "" {
				return nil, wrap("join instance", ErrMalformedHistory)
			}
			if _, exists := state.members[event.Member.Instance]; exists {
				return nil, wrap("join instance reused", ErrMalformedHistory)
			}
			member := &historyMember{
				ref:     event.Member,
				realJID: event.RealJID,
				active:  true,
				names:   []displaySnapshot{{seq: event.Seq, name: event.DisplayName}},
			}
			state.members[event.Member.Instance] = member
			state.membersByKey[event.Member.identityKey()] = member
			state.active[event.Member.Principal] = event.Member.Instance
		case EventRenamed:
			member, err := state.member(event.Member)
			if err != nil {
				return nil, err
			}
			member.names = append(member.names, displaySnapshot{seq: event.Seq, name: event.DisplayName})
		case EventKicked:
			member, err := state.member(event.Member)
			if err != nil {
				return nil, err
			}
			member.active = false
			if state.active[member.ref.Principal] == member.ref.Instance {
				delete(state.active, member.ref.Principal)
			}
		case EventMessage:
			if event.MessageID == "" {
				return nil, wrap("message id", ErrMalformedHistory)
			}
		case EventCorrected, EventRetracted:
			for _, receiptID := range event.ReceiptIDs {
				if receiptID == "" {
					return nil, wrap("receipt id", ErrMalformedHistory)
				}
				state.eventOrder[receiptID] = append(state.eventOrder[receiptID], event)
			}
		case EventRevealed, EventRevoked:
		default:
			return nil, wrap(fmt.Sprintf("unknown event kind %q", event.Kind), ErrMalformedHistory)
		}
	}

	for _, original := range history.Receipts {
		receipt := original.clone()
		if receipt.ID == "" {
			return nil, wrap("receipt id", ErrMalformedHistory)
		}
		if _, exists := state.receipts[receipt.ID]; exists {
			return nil, wrap("duplicate receipt", ErrMalformedHistory)
		}
		if receipt.RoomID != "" && history.RoomID != "" && receipt.RoomID != history.RoomID {
			return nil, wrap("receipt room mismatch", ErrMalformedHistory)
		}
		state.receipts[receipt.ID] = receipt
		if _, err := state.messageEvent(receipt); err != nil {
			return nil, err
		}
	}
	return state, nil
}

func (s *historyState) resolveViewer(viewer MembershipRef) (*historyMember, error) {
	if viewer.RoomID != "" && s.roomID != "" && viewer.RoomID != s.roomID {
		return nil, ErrUnauthenticated
	}
	var member *historyMember
	if viewer.Instance != "" {
		member = s.members[viewer.Instance]
	} else if viewer.Principal != "" && viewer.Generation != 0 {
		member = s.membersByKey[viewer.identityKey()]
	}
	if member == nil {
		return nil, ErrUnknownMember
	}
	if viewer.Principal != "" && viewer.Principal != member.ref.Principal {
		return nil, ErrUnauthenticated
	}
	if viewer.Generation != 0 && viewer.Generation != member.ref.Generation {
		return nil, ErrGenerationMismatch
	}
	return member, nil
}

func (s *historyState) member(ref MembershipRef) (*historyMember, error) {
	if ref.Instance == "" {
		return nil, ErrEmptyInstance
	}
	member, ok := s.members[ref.Instance]
	if !ok {
		return nil, ErrUnknownMember
	}
	if ref.Principal != "" && ref.Principal != member.ref.Principal {
		return nil, ErrUnauthenticated
	}
	if ref.Generation != 0 && ref.Generation != member.ref.Generation {
		return nil, ErrGenerationMismatch
	}
	return member, nil
}

func (s *historyState) messageEvent(receipt Receipt) (Event, error) {
	if receipt.MessageEventSeq != 0 {
		event, ok := s.eventsBySeq[receipt.MessageEventSeq]
		if !ok || event.Kind != EventMessage || event.MessageID != receipt.MessageID {
			return Event{}, wrap(string(receipt.ID), ErrMalformedHistory)
		}
		return event, nil
	}
	for _, event := range s.events {
		if event.Kind != EventMessage || event.MessageID != receipt.MessageID {
			continue
		}
		for _, id := range event.ReceiptIDs {
			if id == receipt.ID {
				return event, nil
			}
		}
	}
	return Event{}, wrap(string(receipt.ID), ErrMalformedHistory)
}

func (s *historyState) renderReceipt(receipt Receipt, viewer MembershipRef, seen map[ReceiptID]bool) (ViewEntry, error) {
	root, err := s.messageEvent(receipt)
	if err != nil {
		return ViewEntry{}, err
	}
	author, err := s.member(receipt.Author)
	if err != nil {
		return ViewEntry{}, err
	}
	authorNameSeq := root.Seq
	if receipt.MessageEventSeq != 0 {
		authorNameSeq = receipt.MessageEventSeq
	}
	retracted := s.retracted(receipt.ID, receipt.Author)
	policy := receipt.Policy
	if retracted {
		policy = IdentityPolicy{}
	}

	entry := ViewEntry{
		ReceiptID:        receipt.ID,
		MessageID:        receipt.MessageID,
		MessageType:      receipt.MessageType,
		Author:           s.visibleMember(author.ref, authorNameSeq, policy),
		OrderSeq:         root.Seq,
		CreatedVersion:   receipt.CreatedVersion,
		PublishedVersion: receipt.PublishedVersion,
		Participants:     cloneSummary(root.Participants),
	}

	corrections := s.receiptEvents(receipt.ID, receipt.Author, EventCorrected)
	if len(corrections) > 0 {
		entry.Corrected = true
	}
	if retracted {
		entry.Retracted = true
		return entry, nil
	}

	body := root.Body
	if len(corrections) > 0 {
		body = corrections[len(corrections)-1].Body
	}
	entry.Body = s.renderBody(body, receipt.Policy, authorNameSeq)
	if receipt.MessageType == MessageTypeError {
		entry.Failure = s.renderFailure(root.Failure, receipt.Policy, authorNameSeq)
	}
	if receipt.QuoteReceipt != "" {
		seen[receipt.ID] = true
		entry.Quote = s.renderQuote(receipt.QuoteReceipt, viewer, seen)
		delete(seen, receipt.ID)
	}
	return entry, nil
}

func (s *historyState) renderQuote(receiptID ReceiptID, viewer MembershipRef, seen map[ReceiptID]bool) *VisibleQuote {
	quote := &VisibleQuote{ReceiptID: receiptID}
	receipt, ok := s.receipts[receiptID]
	if !ok {
		return quote
	}
	quote.MessageID = receipt.MessageID
	if receipt.Target != viewer || receipt.Status != ReceiptPublished || seen[receiptID] {
		return quote
	}
	root, err := s.messageEvent(receipt)
	if err != nil {
		return quote
	}
	author, err := s.member(receipt.Author)
	if err != nil {
		return quote
	}
	quote.Available = true
	nameSeq := root.Seq
	if receipt.MessageEventSeq != 0 {
		nameSeq = receipt.MessageEventSeq
	}
	retracted := s.retracted(receipt.ID, receipt.Author)
	policy := receipt.Policy
	if retracted {
		policy = IdentityPolicy{}
	}
	visibleAuthor := s.visibleMember(author.ref, nameSeq, policy)
	quote.Author = &visibleAuthor

	if retracted {
		quote.Retracted = true
		return quote
	}
	body := root.Body
	corrections := s.receiptEvents(receipt.ID, receipt.Author, EventCorrected)
	if len(corrections) > 0 {
		body = corrections[len(corrections)-1].Body
	}
	renderedBody := s.renderBody(body, receipt.Policy, nameSeq)
	quote.Body = &renderedBody
	quote.Failure = s.renderFailure(root.Failure, receipt.Policy, nameSeq)
	if receipt.QuoteReceipt != "" {
		seen[receipt.ID] = true
		quote.Quote = s.renderQuote(receipt.QuoteReceipt, viewer, seen)
		delete(seen, receipt.ID)
	}
	return quote
}

func (s *historyState) receiptEvents(receiptID ReceiptID, author MembershipRef, kind EventKind) []Event {
	var out []Event
	for _, event := range s.eventOrder[receiptID] {
		if event.Kind != kind || event.Member != author {
			continue
		}
		out = append(out, event)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out
}

func (s *historyState) retracted(receiptID ReceiptID, author MembershipRef) bool {
	return len(s.receiptEvents(receiptID, author, EventRetracted)) > 0
}

func (s *historyState) renderBody(body Body, policy IdentityPolicy, displaySeq uint64) VisibleBody {
	out := VisibleBody{Text: s.scrubText(body.Text, policy)}
	if len(body.Attributes) == 0 {
		return out
	}
	attributes := make([]VisibleAttribute, 0, len(body.Attributes))
	for _, attribute := range body.Attributes {
		visible := VisibleAttribute{Name: attribute.Name}
		if attribute.Identity != nil {
			identity := s.visibleIdentityRef(*attribute.Identity, displaySeq, policy)
			visible.Identity = &identity
			visible.Value = visibleIdentityText(identity)
		} else {
			visible.Value = s.scrubText(attribute.Value, policy)
		}
		attributes = append(attributes, visible)
	}
	sort.SliceStable(attributes, func(i, j int) bool {
		if attributes[i].Name != attributes[j].Name {
			return attributes[i].Name < attributes[j].Name
		}
		if attributes[i].Value != attributes[j].Value {
			return attributes[i].Value < attributes[j].Value
		}
		return identitySortKey(attributes[i].Identity) < identitySortKey(attributes[j].Identity)
	})
	out.Attributes = attributes
	return out
}

func (s *historyState) renderFailure(failure *Failure, policy IdentityPolicy, displaySeq uint64) *VisibleFailure {
	if failure == nil {
		return nil
	}
	out := &VisibleFailure{Code: failure.Code, Text: s.scrubText(failure.Text, policy)}
	if failure.Subject != nil {
		subject := s.visibleIdentityRef(*failure.Subject, displaySeq, policy)
		out.Subject = &subject
	}
	return out
}

func (s *historyState) scrubText(text string, policy IdentityPolicy) string {
	if text == "" {
		return ""
	}
	allowed := make(map[string]struct{})
	unauthorized := make([]string, 0)
	for _, member := range s.members {
		if member.realJID == "" {
			continue
		}
		if policy.AllowsKey(member.ref.identityKey()) {
			allowed[string(member.realJID)] = struct{}{}
		} else {
			unauthorized = append(unauthorized, string(member.realJID))
		}
	}
	sort.Slice(unauthorized, func(i, j int) bool {
		if len(unauthorized[i]) != len(unauthorized[j]) {
			return len(unauthorized[i]) > len(unauthorized[j])
		}
		return unauthorized[i] < unauthorized[j]
	})
	for _, jid := range unauthorized {
		text = strings.ReplaceAll(text, jid, redactedJID)
	}
	return jidPattern.ReplaceAllStringFunc(text, func(candidate string) string {
		if _, ok := allowed[candidate]; ok {
			return candidate
		}
		return redactedJID
	})
}

func (s *historyState) visibleMember(ref MembershipRef, displaySeq uint64, policy IdentityPolicy) VisibleIdentity {
	member := s.members[ref.Instance]
	canonical := ref
	name := DisplayName("")
	realJID := RealJID("")
	if member != nil {
		canonical = member.ref
		name = DisplayName(s.scrubText(string(s.displayName(member, displaySeq)), policy))
		if policy.AllowsKey(canonical.identityKey()) {
			realJID = member.realJID
		}
	}
	return VisibleIdentity{
		DisplayName: name,
		Principal:   canonical.Principal,
		Generation:  canonical.Generation,
		Instance:    canonical.Instance,
		Alias:       aliasFor(canonical, s.roomID),
		JID:         realJID,
	}
}

func (s *historyState) visibleIdentityRef(ref IdentityRef, displaySeq uint64, policy IdentityPolicy) VisibleIdentity {
	if ref.Instance != "" {
		if member, ok := s.members[ref.Instance]; ok {
			return s.visibleMember(member.ref, displaySeq, policy)
		}
	}
	if ref.Principal != "" && ref.Generation != 0 {
		if member, ok := s.membersByKey[ref.Key()]; ok {
			return s.visibleMember(member.ref, displaySeq, policy)
		}
	}
	canonical := MembershipRef{
		RoomID:     s.roomID,
		Instance:   ref.Instance,
		Principal:  ref.Principal,
		Generation: ref.Generation,
	}
	return VisibleIdentity{
		Principal:  canonical.Principal,
		Generation: canonical.Generation,
		Instance:   canonical.Instance,
		Alias:      aliasFor(canonical, s.roomID),
	}
}

func (s *historyState) displayName(member *historyMember, seq uint64) DisplayName {
	name := DisplayName("")
	for _, snapshot := range member.names {
		if snapshot.seq > seq {
			break
		}
		name = snapshot.name
	}
	if name == "" && len(member.names) > 0 {
		name = member.names[0].name
	}
	return name
}

func (s *historyState) participants() ParticipantSummary {
	summary := ParticipantSummary{
		JoinedInstances:          len(s.members),
		ActiveInstances:          len(s.active),
		DistinctActivePrincipals: len(s.active),
	}
	return summary
}

func visibleIdentityText(identity VisibleIdentity) string {
	if identity.JID != "" {
		return string(identity.JID)
	}
	return identity.Alias
}

func identitySortKey(identity *VisibleIdentity) string {
	if identity == nil {
		return ""
	}
	return fmt.Sprintf("%s\x00%020d\x00%s", identity.Principal, identity.Generation, identity.Instance)
}

func aliasFor(ref MembershipRef, roomID RoomID) string {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(roomID))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(string(ref.Principal)))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(string(ref.Instance)))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(fmt.Sprintf("%d", ref.Generation)))
	return fmt.Sprintf("@anon-%016x", hash.Sum64())
}

var (
	jidPattern  = regexp.MustCompile(`[A-Za-z0-9.!#$%&'*+/=?^_` + "`" + `{|}~-]+@[A-Za-z0-9](?:[A-Za-z0-9.-]*[A-Za-z0-9])?(?:/[A-Za-z0-9._~-]+)?`)
	redactedJID = "[redacted-jid]"
)

func cloneSummary(summary *ParticipantSummary) *ParticipantSummary {
	if summary == nil {
		return nil
	}
	out := *summary
	return &out
}
