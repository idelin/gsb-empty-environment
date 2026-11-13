package netsec

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
)

const (
	testRoomID = "!room:example.org"
	evilServer = "evil.example"
	goodServer = "good.example"
)

func stringPointer(value string) *string {
	return &value
}

func stateWithLevel(redact int, users map[string]any) RoomState {
	content := map[string]any{
		"redact": redact,
	}
	if users != nil {
		content["users"] = users
	}
	return StateFromPowerLevels(content)
}

func redactionEvent(eventID, sender, targetID string) Event {
	return Event{
		EventID: eventID,
		Type:    EventTypeRedaction,
		RoomID:  testRoomID,
		Sender:  sender,
		Content: map[string]any{"redacts": targetID},
	}
}

func messageEvent(eventID, sender string) Event {
	return Event{
		EventID: eventID,
		Type:    "m.room.message",
		RoomID:  testRoomID,
		Sender:  sender,
		Content: map[string]any{
			"body":    "secret",
			"msgtype": "m.text",
		},
	}
}

func TestCrossServerRedactionRequiresPower(t *testing.T) {
	redactor := NewRedactor()
	target := messageEvent("$target", "@alice:"+goodServer)
	state := stateWithLevel(50, nil)

	if _, err := redactor.Ingest(target, state); err != nil {
		t.Fatalf("accept target: %v", err)
	}

	attack := redactionEvent("$attack", "@mallory:"+evilServer, target.EventID)
	_, err := redactor.Ingest(attack, state)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("cross-server redaction error = %v, want ErrUnauthorized", err)
	}

	stored, ok := redactor.KnownEvent(testRoomID, target.EventID)
	if !ok || stored.Content["body"] != "secret" {
		t.Fatalf("unauthorized redaction modified target: %#v", stored)
	}
}

func TestSameServerRedactionIsAuthorizedWithoutPower(t *testing.T) {
	redactor := NewRedactor()
	target := messageEvent("$target", "@alice:"+evilServer)
	state := stateWithLevel(50, nil)

	if _, err := redactor.Ingest(target, state); err != nil {
		t.Fatalf("accept target: %v", err)
	}

	attack := redactionEvent("$same-server", "@mallory:"+evilServer, target.EventID)
	result, err := redactor.Ingest(attack, state)
	if err != nil {
		t.Fatalf("same-server redaction should be authorized: %v", err)
	}
	if result.Redaction == nil || !result.Redaction.Authorized || !result.Redaction.Applied {
		t.Fatalf("unexpected same-server result: %#v", result)
	}
	stored, _ := redactor.KnownEvent(testRoomID, target.EventID)
	if _, ok := stored.Content["body"]; ok {
		t.Fatalf("redacted content still contains body: %#v", stored.Content)
	}
}

func TestCrossServerRedactionWithPowerIsAuthorized(t *testing.T) {
	redactor := NewRedactor()
	target := messageEvent("$target", "@alice:"+goodServer)
	state := stateWithLevel(50, map[string]any{
		"@moderator:" + evilServer: 50,
	})
	if _, err := redactor.Ingest(target, state); err != nil {
		t.Fatalf("accept target: %v", err)
	}

	redaction := redactionEvent("$moderation", "@moderator:"+evilServer, target.EventID)
	result, err := redactor.Ingest(redaction, state)
	if err != nil {
		t.Fatalf("power-level redaction should be authorized: %v", err)
	}
	if !result.Redaction.Applied || result.Redaction.RedactedTarget == nil {
		t.Fatalf("redaction was not applied: %#v", result.Redaction)
	}
}

func TestPendingRedactionUsesStateAtRedactionTime(t *testing.T) {
	redactor := NewRedactor()
	target := messageEvent("$late-target", "@alice:"+goodServer)

	levels := map[string]any{
		"redact": 50,
		"users": map[string]any{
			"@moderator:" + evilServer: 50,
		},
	}
	state := StateFromPowerLevels(levels)

	redaction := redactionEvent("$early-redaction", "@moderator:"+evilServer, target.EventID)
	result, err := redactor.Ingest(redaction, state)
	if err != nil {
		t.Fatalf("queue redaction: %v", err)
	}
	if !result.Pending || result.Redaction == nil || !result.Redaction.Pending {
		t.Fatalf("redaction should be pending: %#v", result)
	}
	if redactor.PendingCount(testRoomID) != 1 {
		t.Fatalf("pending count = %d, want 1", redactor.PendingCount(testRoomID))
	}

	// Mutating the caller's live state must not change the snapshot used for
	// the queued redaction.
	levels["users"].(map[string]any)["@moderator:"+evilServer] = 0

	arrived, err := redactor.Ingest(target, RoomState{})
	if err != nil {
		t.Fatalf("target arrival: %v", err)
	}
	if len(arrived.Applied) != 1 || len(arrived.Rejected) != 0 {
		t.Fatalf("pending redaction outcomes: applied=%d rejected=%d", len(arrived.Applied), len(arrived.Rejected))
	}
	if _, exists := arrived.Event.Content["body"]; exists {
		t.Fatalf("target content was not redacted: %#v", arrived.Event.Content)
	}
	if redactor.PendingCount(testRoomID) != 0 {
		t.Fatalf("pending count = %d after target arrival, want 0", redactor.PendingCount(testRoomID))
	}
}

func TestPendingRedactionCannotUseLaterPower(t *testing.T) {
	redactor := NewRedactor()
	target := messageEvent("$late-target", "@alice:"+goodServer)
	state := stateWithLevel(50, nil)
	redaction := redactionEvent("$early-redaction", "@mallory:"+evilServer, target.EventID)

	if _, err := redactor.Ingest(redaction, state); err != nil {
		t.Fatalf("queue redaction: %v", err)
	}
	// There is no state argument on arrival: even if the room later grants
	// mallory level 50, the redaction-time snapshot remains the decision.
	arrived, err := redactor.Ingest(target, stateWithLevel(50, map[string]any{
		"@mallory:" + evilServer: 50,
	}))
	if err != nil {
		t.Fatalf("target arrival: %v", err)
	}
	if len(arrived.Applied) != 0 || len(arrived.Rejected) != 1 {
		t.Fatalf("pending redaction outcomes: applied=%d rejected=%d", len(arrived.Applied), len(arrived.Rejected))
	}
	if arrived.Event.Content["body"] != "secret" {
		t.Fatalf("unauthorized pending redaction changed content: %#v", arrived.Event.Content)
	}
}

func TestPendingLimitPerRoom(t *testing.T) {
	redactor := NewRedactor() // 1000
	for index := 0; index < DefaultPendingLimit; index++ {
		event := redactionEvent(fmt.Sprintf("$queued-%d", index), "@mallory:"+evilServer, fmt.Sprintf("$absent-%d", index))
		if _, err := redactor.Ingest(event, RoomState{}); err != nil {
			t.Fatalf("queue %d: %v", index, err)
		}
	}

	overflow := redactionEvent("$overflow", "@mallory:"+evilServer, "$absent-overflow")
	if _, err := redactor.Ingest(overflow, RoomState{}); !errors.Is(err, ErrPendingLimit) {
		t.Fatalf("overflow error = %v, want ErrPendingLimit", err)
	}
	if redactor.PendingCount(testRoomID) != DefaultPendingLimit {
		t.Fatalf("pending count = %d, want %d", redactor.PendingCount(testRoomID), DefaultPendingLimit)
	}

	// The limit is per room, not global.
	otherRoom := redactionEvent("$other-room", "@mallory:"+evilServer, "$absent-other")
	otherRoom.RoomID = "!other:example.org"
	if _, err := redactor.Ingest(otherRoom, RoomState{}); err != nil {
		t.Fatalf("queue in second room: %v", err)
	}

	// Arrival of a target frees its slot.
	target := messageEvent("$absent-0", "@alice:"+goodServer)
	if _, err := redactor.Ingest(target, RoomState{}); err != nil {
		t.Fatalf("target arrival: %v", err)
	}
	replacement := redactionEvent("$replacement", "@mallory:"+evilServer, "$absent-replacement")
	if _, err := redactor.Ingest(replacement, RoomState{}); err != nil {
		t.Fatalf("queue after freeing slot: %v", err)
	}
	if redactor.PendingCount(testRoomID) != DefaultPendingLimit {
		t.Fatalf("pending count after replacement = %d, want %d", redactor.PendingCount(testRoomID), DefaultPendingLimit)
	}
}

func TestDuplicateRedactionDoesNotConsumePendingSlots(t *testing.T) {
	redactor := NewRedactorWithLimit(2)
	event := redactionEvent("$duplicate", "@mallory:"+evilServer, "$absent")
	for index := 0; index < 5; index++ {
		if _, err := redactor.Ingest(event, RoomState{}); err != nil {
			t.Fatalf("duplicate %d: %v", index, err)
		}
	}
	if redactor.PendingCount(testRoomID) != 1 {
		t.Fatalf("pending count = %d, want 1", redactor.PendingCount(testRoomID))
	}
}

func TestRedactedPowerLevelsKeepsOnlyWhitelistedKeys(t *testing.T) {
	stateKey := ""
	target := Event{
		EventID:  "$power-levels",
		Type:     "m.room.power_levels",
		RoomID:   testRoomID,
		Sender:   "@alice:" + goodServer,
		StateKey: &stateKey,
		Content: map[string]any{
			"ban":            50,
			"events":         map[string]any{"m.room.avatar": 50},
			"events_default": 0,
			"invite":         0,
			"kick":           50,
			"redact":         50,
			"state_default":  50,
			"users":          map[string]any{"@alice:" + goodServer: 100},
			"users_default":  0,
			"notifications":  map[string]any{"room": 50},
			"secret":         "do-not-retain",
		},
	}

	redacted := RedactEvent(target)
	if redacted.EventID != target.EventID || redacted.Type != target.Type || redacted.RoomID != target.RoomID || redacted.Sender != target.Sender {
		t.Fatalf("core event fields changed: %#v", redacted)
	}
	if redacted.StateKey == nil || *redacted.StateKey != "" {
		t.Fatalf("empty state_key was not preserved: %#v", redacted.StateKey)
	}
	allowed := map[string]bool{
		"ban": true, "events": true, "events_default": true, "invite": true,
		"kick": true, "redact": true, "state_default": true, "users": true,
		"users_default": true,
	}
	for key, value := range redacted.Content {
		if !allowed[key] {
			t.Fatalf("unexpected content key %q survived: %#v", key, value)
		}
	}
	for key := range allowed {
		if _, ok := redacted.Content[key]; !ok {
			t.Fatalf("whitelisted content key %q was removed", key)
		}
	}

	// The whitelisted nested maps must still be intact.
	users, ok := redacted.Content["users"].(map[string]any)
	if !ok || users["@alice:"+goodServer] != 100 {
		t.Fatalf("users map not preserved: %#v", redacted.Content["users"])
	}
}

func TestPendingRedactionAppliedToPowerLevelsOnArrival(t *testing.T) {
	redactor := NewRedactor()
	stateKey := ""
	target := Event{
		EventID:  "$late-power-levels",
		Type:     "m.room.power_levels",
		RoomID:   testRoomID,
		Sender:   "@alice:" + goodServer,
		StateKey: &stateKey,
		Content: map[string]any{
			"ban":           50,
			"kick":          50,
			"redact":        50,
			"events":        map[string]any{"m.room.name": 50},
			"users":         map[string]any{"@alice:" + goodServer: 100},
			"users_default": 0,
			"state_default": 50,
			"secret":        "should-not-survive",
		},
	}
	state := stateWithLevel(50, map[string]any{
		"@admin:" + evilServer: 100,
	})
	redaction := redactionEvent("$late-power-redaction", "@admin:"+evilServer, target.EventID)
	if _, err := redactor.Ingest(redaction, state); err != nil {
		t.Fatalf("queue redaction: %v", err)
	}

	arrived, err := redactor.Ingest(target, RoomState{})
	if err != nil {
		t.Fatalf("target arrival: %v", err)
	}
	if len(arrived.Applied) != 1 {
		t.Fatalf("applied redactions = %d, want 1", len(arrived.Applied))
	}
	if _, ok := arrived.Event.Content["secret"]; ok {
		t.Fatalf("non-whitelisted power-levels key survived: %#v", arrived.Event.Content)
	}
	for _, key := range []string{"ban", "kick", "redact", "events", "users", "users_default", "state_default"} {
		if _, ok := arrived.Event.Content[key]; !ok {
			t.Fatalf("power-levels key %q was removed", key)
		}
	}
}

func TestMemberRedactionKeepsOnlyMembership(t *testing.T) {
	stateKey := "@alice:" + goodServer
	target := Event{
		EventID:  "$membership",
		Type:     "m.room.member",
		RoomID:   testRoomID,
		Sender:   "@alice:" + goodServer,
		StateKey: &stateKey,
		Content: map[string]any{
			"membership":  "join",
			"displayname": "Alice",
			"avatar_url":  "mxc://example/secret",
			"admin":       true,
		},
	}
	redacted := RedactEvent(target)
	want := map[string]any{"membership": "join"}
	if !reflect.DeepEqual(redacted.Content, want) {
		t.Fatalf("member content = %#v, want %#v", redacted.Content, want)
	}
	if redacted.StateKey == nil || *redacted.StateKey != stateKey {
		t.Fatalf("member state_key not preserved: %#v", redacted.StateKey)
	}
}

func TestRedactPDUDropsTopLevelMetadataAndUnknownContent(t *testing.T) {
	pdu := map[string]any{
		"event_id":         "$message",
		"type":             "m.room.message",
		"room_id":          testRoomID,
		"sender":           "@alice:" + goodServer,
		"origin_server_ts": int64(1234),
		"unsigned":         map[string]any{"displayname": "Alice"},
		"hashes":           map[string]any{"sha256": "abcd"},
		"signatures":       map[string]any{"example.org": map[string]any{"ed25519:1": "sig"}},
		"content": map[string]any{
			"body":    "secret",
			"msgtype": "m.text",
		},
	}
	redacted := RedactPDU(pdu)
	if len(redacted) != 5 {
		t.Fatalf("redacted PDU keys = %#v", redacted)
	}
	for _, key := range []string{"origin_server_ts", "unsigned", "hashes", "signatures"} {
		if _, ok := redacted[key]; ok {
			t.Fatalf("top-level field %q survived", key)
		}
	}
	content, ok := redacted["content"].(map[string]any)
	if !ok || len(content) != 0 {
		t.Fatalf("message content = %#v, want empty", redacted["content"])
	}
}

func TestPowerLevelAuthorizationDefaults(t *testing.T) {
	target := messageEvent("$target", "@alice:"+goodServer)
	tests := []struct {
		name  string
		state RoomState
		want  bool
	}{
		{
			name:  "missing redact defaults to 50",
			state: StateFromPowerLevels(map[string]any{"users": map[string]any{"@moderator:" + evilServer: 50}}),
			want:  true,
		},
		{
			name:  "users_default applies",
			state: StateFromPowerLevels(map[string]any{"redact": 50, "users_default": 50}),
			want:  true,
		},
		{
			name:  "below required level",
			state: StateFromPowerLevels(map[string]any{"redact": 50, "users_default": 49}),
			want:  false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			redaction := redactionEvent("$auth-test", "@moderator:"+evilServer, target.EventID)
			if got := CanRedact(redaction, target, test.state); got != test.want {
				t.Fatalf("CanRedact = %v, want %v", got, test.want)
			}
		})
	}
}
