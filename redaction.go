package netsec

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
)

const (
	// EventTypeRedaction is the Matrix event type for a redaction.
	EventTypeRedaction = "m.room.redaction"

	// DefaultPendingLimit is the maximum number of pending redactions kept
	// for each room.
	DefaultPendingLimit = 1000

	// defaultRedactPowerLevel is the room default for the redact power level.
	defaultRedactPowerLevel = 50
)

var (
	// ErrInvalidEvent indicates that a PDU is missing a required field.
	ErrInvalidEvent = errors.New("redaction: invalid event")
	// ErrInvalidRedaction indicates that a redaction has no unambiguous target.
	ErrInvalidRedaction = errors.New("redaction: invalid redaction event")
	// ErrRoomMismatch indicates that a redaction and its target are in
	// different rooms.
	ErrRoomMismatch = errors.New("redaction: target is in a different room")
	// ErrTargetMismatch indicates that the supplied event is not the event
	// named by the redaction.
	ErrTargetMismatch = errors.New("redaction: target does not match redacts field")
	// ErrUnauthorized indicates that the redaction does not satisfy either
	// authorization rule.
	ErrUnauthorized = errors.New("redaction: sender is not authorized")
	// ErrPendingLimit indicates that a room's pending redaction table is full.
	ErrPendingLimit = errors.New("redaction: pending redaction limit reached")
)

// Event is the subset of a Matrix room event needed for redaction handling.
//
// StateKey is a pointer because Matrix distinguishes an absent state_key
// (a message event) from a present but empty state_key (a state event).
// Redacts is the pre-room-version-11 location of a redaction's target;
// room version 11 and later put it in Content.
type Event struct {
	EventID  string         `json:"event_id"`
	Type     string         `json:"type"`
	RoomID   string         `json:"room_id"`
	Sender   string         `json:"sender"`
	StateKey *string        `json:"state_key,omitempty"`
	Content  map[string]any `json:"content"`
	Redacts  string         `json:"redacts,omitempty"`
}

// PowerLevels is the content of an m.room.power_levels state event.
//
// The map form is intentional: federation PDUs are commonly decoded from
// JSON, and keeping the original content makes it possible to snapshot the
// exact power-level event that was current when a redaction arrived.
type PowerLevels map[string]any

// RoomState is the part of a room state snapshot used for redaction
// authorization. Callers should pass the state visible at the redaction
// event's position in the room DAG, not the latest state at delivery time.
type RoomState struct {
	PowerLevels PowerLevels
}

// RedactionResult describes the handling of one redaction.
type RedactionResult struct {
	Redaction      Event
	TargetID       string
	Pending        bool
	Authorized     bool
	Applied        bool
	RedactedTarget *Event
}

// TargetResult describes what happened when a target event arrived.
type TargetResult struct {
	// Event is the target, with all authorized pending redactions applied.
	Event Event
	// Applied contains pending redactions that were authorized and applied.
	Applied []RedactionResult
	// Rejected contains pending redactions that failed authorization.
	Rejected []RedactionResult
	// PendingRemoved is the number of table entries consumed by this target.
	PendingRemoved int
}

// IngestResult is returned by Ingest. At most one of Redaction and the
// target redaction slices is populated for a single incoming PDU.
type IngestResult struct {
	// Event is the event that should be stored after ingress. For an ordinary
	// event this may be the redacted form. For a redaction PDU it is the
	// redaction itself.
	Event Event
	// Pending is true when the incoming redaction's target is not known yet.
	Pending bool
	// PendingCount is the number of pending redactions in the event's room.
	PendingCount int
	// Redaction is populated when the incoming PDU is a redaction.
	Redaction *RedactionResult
	// Applied and Rejected are populated when the incoming PDU is a target
	// for previously queued redactions.
	Applied  []RedactionResult
	Rejected []RedactionResult
}

type pendingKey struct {
	roomID   string
	targetID string
}

type eventKey struct {
	roomID  string
	eventID string
}

type pendingRedaction struct {
	event Event
	state RoomState
	seq   uint64
}

// Redactor is a concurrency-safe redaction ingress for one or more rooms.
// A single Redactor can be shared by all room workers.
type Redactor struct {
	mu sync.Mutex

	pendingLimit int
	pending      map[pendingKey][]pendingRedaction
	pendingIDs   map[eventKey]struct{}
	pendingCount map[string]int
	nextSeq      uint64

	// events is the set of events this ingress has accepted. It lets an
	// ordinary redaction be applied immediately when its target was already
	// processed by the same Redactor. Callers with an external event store
	// can use RedactNow instead of relying on this cache.
	events map[eventKey]Event
}

// NewRedactor creates a Redactor with the per-room limit from the federation
// requirement (1000 pending redactions).
func NewRedactor() *Redactor {
	return NewRedactorWithLimit(DefaultPendingLimit)
}

// NewRedactorWithLimit creates a Redactor with an explicit per-room pending
// limit. A zero limit disables queuing; a negative limit is treated as zero.
func NewRedactorWithLimit(limit int) *Redactor {
	if limit < 0 {
		limit = 0
	}
	return &Redactor{
		pendingLimit: limit,
		pending:      make(map[pendingKey][]pendingRedaction),
		pendingIDs:   make(map[eventKey]struct{}),
		pendingCount: make(map[string]int),
		events:       make(map[eventKey]Event),
	}
}

// PendingLimit returns the configured per-room pending limit.
func (r *Redactor) PendingLimit() int {
	return r.pendingLimit
}

// PendingCount returns the number of pending redactions in roomID.
func (r *Redactor) PendingCount(roomID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.pendingCount[roomID]
}

// TotalPending returns the number of pending redactions across all rooms.
func (r *Redactor) TotalPending() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	total := 0
	for _, count := range r.pendingCount {
		total += count
	}
	return total
}

// KnownEvent returns the event previously accepted by this Redactor.
func (r *Redactor) KnownEvent(roomID, eventID string) (Event, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	event, ok := r.events[eventKey{roomID: roomID, eventID: eventID}]
	return event.Clone(), ok
}

// PendingTargets returns the target event IDs currently queued in roomID,
// sorted for deterministic diagnostics.
func (r *Redactor) PendingTargets(roomID string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	seen := make(map[string]struct{})
	for key, entries := range r.pending {
		if key.roomID == roomID && len(entries) != 0 {
			seen[key.targetID] = struct{}{}
		}
	}
	targets := make([]string, 0, len(seen))
	for target := range seen {
		targets = append(targets, target)
	}
	sort.Strings(targets)
	return targets
}

// Ingest is the federation PDU entry point. Pass current state when event is
// an m.room.redaction. The supplied state is snapshotted before a redaction
// is queued.
//
// An event consumes any pending redactions targeting it before it is
// interpreted. This can happen in either order:
//
//	redaction arrives first  -> it is queued, then applied when target arrives
//	target arrives first     -> RedactNow/Ingest applies it immediately
func (r *Redactor) Ingest(event Event, state RoomState) (IngestResult, error) {
	if err := validateEvent(event); err != nil {
		return IngestResult{}, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ingestLocked(event, state)
}

// HandlePDU is an explicit alias for Ingest, suitable for a federation
// transaction handler.
func (r *Redactor) HandlePDU(event Event, state RoomState) (IngestResult, error) {
	return r.Ingest(event, state)
}

func (r *Redactor) ingestLocked(event Event, state RoomState) (IngestResult, error) {
	// Every incoming PDU first consumes redactions that were waiting for it,
	// even if the PDU itself is another redaction.
	arrived := r.applyPending(event)
	effective := arrived.Event

	// Keep a copy of the state at the point where the redaction was observed.
	// This is critical for queued redactions: authorization must not depend
	// on state that arrives after the redaction.
	state = state.snapshot()

	result := IngestResult{
		Event:        effective.Clone(),
		Applied:      arrived.Applied,
		Rejected:     arrived.Rejected,
		PendingCount: r.pendingCount[event.RoomID],
	}
	if effective.Type != EventTypeRedaction {
		r.events[eventKey{roomID: effective.RoomID, eventID: effective.EventID}] = effective.Clone()
		return result, nil
	}

	targetID, err := redactionTarget(effective)
	if err != nil {
		return result, err
	}

	key := eventKey{roomID: effective.RoomID, eventID: targetID}
	if target, ok := r.events[key]; ok {
		redactionResult, err := r.redactNowLocked(effective, target, state)
		result.Redaction = &redactionResult
		if err != nil {
			return result, err
		}
		r.events[key] = *redactionResult.RedactedTarget
		r.events[eventKey{roomID: effective.RoomID, eventID: effective.EventID}] = effective.Clone()
		return result, nil
	}

	redactionResult, err := r.queueRedactionLocked(effective, state)
	result.Redaction = &redactionResult
	if err != nil {
		return result, err
	}
	r.events[eventKey{roomID: effective.RoomID, eventID: effective.EventID}] = effective.Clone()
	result.Pending = redactionResult.Pending
	result.PendingCount = r.pendingCount[effective.RoomID]
	return result, nil
}

// RedactNow applies a redaction when the caller already has the target event.
// It does not consult the internal known-event cache. roomState must be the
// state at the redaction event's position.
func (r *Redactor) RedactNow(redaction, target Event, roomState RoomState) (RedactionResult, error) {
	if err := validateEvent(redaction); err != nil {
		return RedactionResult{}, err
	}
	if redaction.Type != EventTypeRedaction {
		return RedactionResult{}, fmt.Errorf("%w: event type is %q", ErrInvalidRedaction, redaction.Type)
	}
	// Serialize with the pending table. Authorization itself uses only
	// roomState, which is snapshotted here.
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.redactNowLocked(redaction, target, roomState.snapshot())
}

func (r *Redactor) redactNowLocked(redaction, target Event, state RoomState) (RedactionResult, error) {
	targetID, err := redactionTarget(redaction)
	if err != nil {
		return RedactionResult{}, err
	}
	result := RedactionResult{
		Redaction: redaction.Clone(),
		TargetID:  targetID,
	}
	if redaction.RoomID != target.RoomID {
		return result, ErrRoomMismatch
	}
	if target.EventID != targetID {
		return result, ErrTargetMismatch
	}
	if !CanRedact(redaction, target, state) {
		return result, ErrUnauthorized
	}

	redacted := RedactEvent(target)
	result.Authorized = true
	result.Applied = true
	result.RedactedTarget = &redacted
	return result, nil
}

// QueueRedaction explicitly queues a redaction even if the target may be
// known to an external store. Prefer Ingest for normal federation ingress.
func (r *Redactor) QueueRedaction(redaction Event, roomState RoomState) (IngestResult, error) {
	if err := validateEvent(redaction); err != nil {
		return IngestResult{}, err
	}
	if redaction.Type != EventTypeRedaction {
		return IngestResult{}, fmt.Errorf("%w: event type is %q", ErrInvalidRedaction, redaction.Type)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	result, err := r.queueRedactionLocked(redaction, roomState.snapshot())
	return IngestResult{
		Event:        redaction.Clone(),
		Redaction:    &result,
		Pending:      err == nil && result.Pending,
		PendingCount: r.pendingCount[redaction.RoomID],
	}, err
}

func (r *Redactor) queueRedactionLocked(redaction Event, state RoomState) (RedactionResult, error) {
	targetID, err := redactionTarget(redaction)
	if err != nil {
		return RedactionResult{}, err
	}
	result := RedactionResult{
		Redaction: redaction.Clone(),
		TargetID:  targetID,
		Pending:   true,
	}

	// Federation can retransmit the same PDU. Do not let duplicate event IDs
	// consume additional pending slots.
	idKey := eventKey{roomID: redaction.RoomID, eventID: redaction.EventID}
	if _, exists := r.pendingIDs[idKey]; exists {
		return result, nil
	}
	// If this ingress already accepted the redaction after its target
	// arrived, a retransmission is a no-op rather than a new pending entry.
	if _, exists := r.events[idKey]; exists {
		result.Pending = false
		return result, nil
	}

	if r.pendingCount[redaction.RoomID] >= r.pendingLimit {
		return result, fmt.Errorf("%w: room %q has %d pending", ErrPendingLimit, redaction.RoomID, r.pendingCount[redaction.RoomID])
	}

	key := pendingKey{roomID: redaction.RoomID, targetID: targetID}
	r.nextSeq++
	r.pending[key] = append(r.pending[key], pendingRedaction{
		event: redaction.Clone(),
		state: state,
		seq:   r.nextSeq,
	})
	r.pendingIDs[idKey] = struct{}{}
	r.pendingCount[redaction.RoomID]++
	return result, nil
}

// TargetArrived applies all redactions that were waiting for target.EventID.
// Authorization is evaluated using each redaction's snapshotted state.
func (r *Redactor) TargetArrived(target Event) (TargetResult, error) {
	if err := validateEvent(target); err != nil {
		return TargetResult{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	result := r.applyPending(target)
	r.events[eventKey{roomID: target.RoomID, eventID: target.EventID}] = result.Event.Clone()
	return result, nil
}

func (r *Redactor) applyPending(target Event) TargetResult {
	key := pendingKey{roomID: target.RoomID, targetID: target.EventID}
	entries := r.pending[key]
	if len(entries) != 0 {
		delete(r.pending, key)
		r.pendingCount[target.RoomID] -= len(entries)
		for _, entry := range entries {
			delete(r.pendingIDs, eventKey{roomID: entry.event.RoomID, eventID: entry.event.EventID})
		}
	}

	// Entries are appended in arrival order. Sorting makes the behavior
	// explicit even if a future change stops using slices.
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].seq < entries[j].seq })

	result := TargetResult{
		Event:          target.Clone(),
		PendingRemoved: len(entries),
	}
	for _, entry := range entries {
		rred, err := r.redactNowLocked(entry.event, result.Event, entry.state)
		if err != nil {
			// At target-arrival time the target is present, so malformed
			// entries or a denial are attributed to the redaction. The target
			// itself is still accepted; only the redaction is rejected.
			rred.Redaction = entry.event.Clone()
			rred.TargetID = target.EventID
			result.Rejected = append(result.Rejected, rred)
			continue
		}
		result.Event = *rred.RedactedTarget
		result.Applied = append(result.Applied, rred)
	}

	return result
}

// RedactionTarget returns the target event ID from either the room-version-11
// content location or the legacy top-level redacts field. An ambiguous PDU
// with different values in both locations is rejected.
func RedactionTarget(event Event) (string, error) {
	return redactionTarget(event)
}

func redactionTarget(event Event) (string, error) {
	var contentTarget string
	if event.Content != nil {
		if value, ok := event.Content["redacts"]; ok {
			text, ok := value.(string)
			if !ok {
				return "", fmt.Errorf("%w: content.redacts is not a string", ErrInvalidRedaction)
			}
			contentTarget = text
		}
	}
	if event.Redacts != "" && contentTarget != "" && event.Redacts != contentTarget {
		return "", fmt.Errorf("%w: conflicting redacts fields", ErrInvalidRedaction)
	}
	target := contentTarget
	if target == "" {
		target = event.Redacts
	}
	if target == "" {
		return "", fmt.Errorf("%w: missing redacts field", ErrInvalidRedaction)
	}
	return target, nil
}

// CanRedact reports whether redaction may redact target. Authorization is
// granted when the sender has the room's redact power level or when both
// senders are on the same homeserver. The state must be the state current at
// the redaction event, not at target delivery time.
func CanRedact(redaction, target Event, state RoomState) bool {
	if redaction.Type != EventTypeRedaction {
		return false
	}
	return CanRedactSender(redaction.Sender, target.Sender, state)
}

// CanRedactSender is CanRedact for callers that only have the target sender.
func CanRedactSender(sender, targetSender string, state RoomState) bool {
	if sender == "" || targetSender == "" {
		return false
	}
	if sender == targetSender || SameServer(sender, targetSender) {
		return true
	}

	powerLevels := state.PowerLevels
	required := levelValue(powerLevels["redact"], defaultRedactPowerLevel)
	usersDefault := levelValue(powerLevels["users_default"], 0)

	userLevel := usersDefault
	if value, ok := mapStringValue(powerLevels["users"], sender); ok {
		userLevel = levelValue(value, usersDefault)
	}
	return userLevel >= required
}

// SameServer reports whether two Matrix user IDs belong to the same
// homeserver. Equality is also accepted because identical senders necessarily
// originate on the same server even if a legacy ID is not fully qualified.
func SameServer(first, second string) bool {
	if first == "" || second == "" {
		return false
	}
	if first == second {
		return true
	}
	firstServer, firstOK := ServerName(first)
	secondServer, secondOK := ServerName(second)
	return firstOK && secondOK && strings.EqualFold(firstServer, secondServer)
}

// ServerName extracts the homeserver from a Matrix user ID.
func ServerName(userID string) (string, bool) {
	if len(userID) < 4 || userID[0] != '@' {
		return "", false
	}
	colon := strings.IndexByte(userID, ':')
	if colon <= 1 || colon == len(userID)-1 {
		return "", false
	}
	return userID[colon+1:], true
}

// RedactEvent returns the redacted form of target. Only event_id, type,
// room_id, sender, state_key, and the event type's content whitelist survive.
// m.room.create is the one type whose full content is preserved, as required
// by the Matrix redaction algorithm.
func RedactEvent(target Event) Event {
	redacted := Event{
		EventID: target.EventID,
		Type:    target.Type,
		RoomID:  target.RoomID,
		Sender:  target.Sender,
	}
	if target.StateKey != nil {
		stateKey := *target.StateKey
		redacted.StateKey = &stateKey
	}
	redacted.Content = redactedContent(target.Type, target.Content)
	return redacted
}

// RedactPDU applies the redaction algorithm to a JSON-style PDU map. It is
// useful at a federation decoder where unsigned data, hashes, signatures,
// and other top-level fields must not be carried into the redacted event.
func RedactPDU(target map[string]any) map[string]any {
	redacted := make(map[string]any, 6)
	for _, key := range []string{"event_id", "type", "room_id", "sender", "state_key"} {
		if value, ok := target[key]; ok {
			redacted[key] = cloneValue(value)
		}
	}
	eventType, _ := target["type"].(string)
	var content map[string]any
	if value, ok := target["content"].(map[string]any); ok {
		content = value
	}
	redacted["content"] = redactedContent(eventType, content)
	return redacted
}

func redactedContent(eventType string, content map[string]any) map[string]any {
	out := make(map[string]any)
	if eventType == "m.room.create" {
		for key, value := range content {
			out[key] = cloneValue(value)
		}
		return out
	}
	allowed := contentWhitelist[eventType]
	for _, key := range allowed {
		if value, ok := content[key]; ok {
			out[key] = cloneValue(value)
		}
	}
	return out
}

var contentWhitelist = map[string][]string{
	"m.room.member":             {"membership"},
	"m.room.join_rules":         {"join_rule", "allow"},
	"m.room.power_levels":       {"ban", "events", "events_default", "invite", "kick", "redact", "state_default", "users", "users_default"},
	"m.room.aliases":            {"aliases"},
	"m.room.history_visibility": {"history_visibility"},
	"m.room.third_party_invite": {"display_name", "key_validity_url", "public_key", "public_keys"},
	"m.room.encryption":         {"algorithm", "rotation_period_ms", "rotation_period_msgs"},
	"m.room.avatar":             {"url"},
	"m.room.canonical_alias":    {"alias", "alt_aliases"},
	"m.room.pinned":             {"pinned"},
	"m.room.tombstone":          {"replacement_room"},
	"m.room.server_acl":         {"allow", "deny"},
	"m.room.retention":          {"max_lifetime", "min_lifetime"},
	"m.room.guest_access":       {"guest_access"},
	"m.room.redaction":          {"redacts"},
}

func validateEvent(event Event) error {
	if event.EventID == "" {
		return fmt.Errorf("%w: missing event_id", ErrInvalidEvent)
	}
	if event.Type == "" {
		return fmt.Errorf("%w: missing type", ErrInvalidEvent)
	}
	if event.RoomID == "" {
		return fmt.Errorf("%w: missing room_id", ErrInvalidEvent)
	}
	if event.Sender == "" {
		return fmt.Errorf("%w: missing sender", ErrInvalidEvent)
	}
	return nil
}

// Clone returns a deep copy. StateKey is copied so mutating the returned
// event cannot change the original.
func (event Event) Clone() Event {
	clone := Event{
		EventID: event.EventID,
		Type:    event.Type,
		RoomID:  event.RoomID,
		Sender:  event.Sender,
		Redacts: event.Redacts,
		Content: cloneMap(event.Content),
	}
	if event.StateKey != nil {
		stateKey := *event.StateKey
		clone.StateKey = &stateKey
	}
	return clone
}

// ToMap converts the event to its JSON-style PDU representation.
func (event Event) ToMap() map[string]any {
	pdu := map[string]any{
		"event_id": event.EventID,
		"type":     event.Type,
		"room_id":  event.RoomID,
		"sender":   event.Sender,
		"content":  cloneMap(event.Content),
	}
	if event.StateKey != nil {
		pdu["state_key"] = *event.StateKey
	}
	if event.Redacts != "" {
		pdu["redacts"] = event.Redacts
	}
	return pdu
}

// EventFromMap converts a decoded PDU map to Event. Unknown top-level fields
// are intentionally ignored; RedactPDU should be used when the map itself is
// the stored event.
func EventFromMap(pdu map[string]any) Event {
	event := Event{}
	event.EventID, _ = pdu["event_id"].(string)
	event.Type, _ = pdu["type"].(string)
	event.RoomID, _ = pdu["room_id"].(string)
	event.Sender, _ = pdu["sender"].(string)
	event.Redacts, _ = pdu["redacts"].(string)
	if stateKey, ok := pdu["state_key"].(string); ok {
		event.StateKey = &stateKey
	}
	if content, ok := pdu["content"].(map[string]any); ok {
		event.Content = cloneMap(content)
	}
	return event
}

// StateFromEvents derives a redaction state snapshot from an ordered state
// slice. The last m.room.power_levels event in the slice wins.
func StateFromEvents(events []Event) RoomState {
	var state RoomState
	for _, event := range events {
		if event.Type == "m.room.power_levels" {
			state.PowerLevels = PowerLevels(cloneMap(event.Content))
		}
	}
	return state
}

// StateFromPowerLevels creates a room state snapshot from the content of an
// m.room.power_levels event.
func StateFromPowerLevels(content map[string]any) RoomState {
	return RoomState{PowerLevels: PowerLevels(cloneMap(content))}
}

func (state RoomState) snapshot() RoomState {
	return RoomState{PowerLevels: PowerLevels(cloneMap(state.PowerLevels))}
}

func levelValue(value any, fallback int) int {
	if value == nil {
		return fallback
	}
	if number, ok := value.(json.Number); ok {
		if integer, err := number.Int64(); err == nil {
			return int(integer)
		}
		if float, err := number.Float64(); err == nil {
			return int(float)
		}
		return fallback
	}

	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return int(reflected.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return int(reflected.Uint())
	case reflect.Float32, reflect.Float64:
		return int(reflected.Float())
	default:
		return fallback
	}
}

func mapStringValue(value any, key string) (any, bool) {
	if value == nil {
		return nil, false
	}
	if mapping, ok := value.(map[string]any); ok {
		result, exists := mapping[key]
		return result, exists
	}

	reflected := reflect.ValueOf(value)
	if reflected.Kind() != reflect.Map || reflected.Type().Key().Kind() != reflect.String {
		return nil, false
	}
	keyValue := reflect.ValueOf(key)
	if keyValue.Type() != reflected.Type().Key() {
		if !keyValue.Type().ConvertibleTo(reflected.Type().Key()) {
			return nil, false
		}
		keyValue = keyValue.Convert(reflected.Type().Key())
	}
	result := reflected.MapIndex(keyValue)
	if !result.IsValid() {
		return nil, false
	}
	return result.Interface(), true
}

func cloneMap(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	output := make(map[string]any, len(input))
	for key, value := range input {
		output[key] = cloneValue(value)
	}
	return output
}

func cloneValue(value any) any {
	if value == nil {
		return nil
	}
	switch typed := value.(type) {
	case map[string]any:
		return cloneMap(typed)
	case []any:
		output := make([]any, len(typed))
		for index, item := range typed {
			output[index] = cloneValue(item)
		}
		return output
	}

	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Map:
		if reflected.Type().Key().Kind() == reflect.String {
			output := make(map[string]any, reflected.Len())
			iter := reflected.MapRange()
			for iter.Next() {
				output[iter.Key().String()] = cloneValue(iter.Value().Interface())
			}
			return output
		}
	case reflect.Slice, reflect.Array:
		output := make([]any, reflected.Len())
		for index := range output {
			output[index] = cloneValue(reflected.Index(index).Interface())
		}
		return output
	}
	return value
}
