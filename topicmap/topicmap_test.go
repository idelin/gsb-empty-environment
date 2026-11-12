package topicmap

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

const (
	clientA = "192.0.2.10:20000"
	clientB = "192.0.2.20:20001"
)

func newTestTable(t *testing.T) *Table {
	t.Helper()
	table, err := New(PredefinedWhitelist{
		clientA: {MinPredefinedID: "a/predefined", MaxPredefinedID: "a/other"},
		clientB: {MinPredefinedID: "b/predefined"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return table
}

// A REGISTER carrying an MQTT wildcard must never create a mapping.
func TestRegisterRejectsWildcards(t *testing.T) {
	table := newTestTable(t)

	for _, topic := range []string{"sensor/+/temp", "#", "a/#/b", "", "lead+trail"} {
		if id, err := table.Register(clientA, topic); err == nil {
			t.Errorf("Register(%q) = 0x%04X, want rejection", topic, id)
		} else if topic != "" && !errors.Is(err, ErrWildcardTopic) {
			t.Errorf("Register(%q) error = %v, want ErrWildcardTopic", topic, err)
		} else if topic == "" && !errors.Is(err, ErrEmptyTopic) {
			t.Errorf("Register(%q) error = %v, want ErrEmptyTopic", topic, err)
		}

		// A rejected registration must leave nothing behind.
		if _, ok := table.LookupID(clientA, topic); ok {
			t.Errorf("LookupID(%q) found a mapping after rejected Register", topic)
		}
	}

	// Whitelist construction also rejects wildcard predefined topics.
	if _, err := New(PredefinedWhitelist{
		clientA: {MinPredefinedID: "wild/#"},
	}); !errors.Is(err, ErrWildcardTopic) {
		t.Fatalf("New with wildcard predefined topic: error = %v, want ErrWildcardTopic", err)
	}
}

// Normal topic ids are per-client: client B cannot resolve an id
// allocated from client A's REGISTER.
func TestNormalIDsIsolatedPerClient(t *testing.T) {
	table := newTestTable(t)

	idA, err := table.Register(clientA, "a/temp")
	if err != nil {
		t.Fatalf("Register clientA: %v", err)
	}

	if got, err := table.Resolve(clientA, idA); err != nil || got != "a/temp" {
		t.Fatalf("Resolve(clientA, 0x%04X) = %q, %v; want a/temp", idA, got, err)
	}

	if _, err := table.Resolve(clientB, idA); !errors.Is(err, ErrUnknownTopicID) {
		t.Fatalf("Resolve(clientB, clientA id 0x%04X): error = %v, want ErrUnknownTopicID", idA, err)
	}

	// Client B allocates the same numeric id independently for its own topic.
	idB, err := table.Register(clientB, "b/temp")
	if err != nil {
		t.Fatalf("Register clientB: %v", err)
	}
	if idB != idA {
		t.Fatalf("clientB id = 0x%04X, want independent allocation 0x%04X", idB, idA)
	}
	if got, err := table.Resolve(clientB, idB); err != nil || got != "b/temp" {
		t.Fatalf("Resolve(clientB, 0x%04X) = %q, %v; want b/temp", idB, got, err)
	}

	// Re-registering the same topic returns the same id.
	idAgain, err := table.Register(clientA, "a/temp")
	if err != nil || idAgain != idA {
		t.Fatalf("re-Register(clientA) = 0x%04X, %v; want 0x%04X", idAgain, err, idA)
	}
}

// After a clean session every id the client held is invalid; an attacker
// replaying an old datagram gets no resolution.
func TestCleanSessionInvalidatesOldIDs(t *testing.T) {
	table := newTestTable(t)

	oldID, err := table.Register(clientA, "a/temp")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	table.CleanSession(clientA)

	if _, err := table.Resolve(clientA, oldID); !errors.Is(err, ErrUnknownTopicID) {
		t.Fatalf("Resolve old id 0x%04X after CleanSession: error = %v, want ErrUnknownTopicID", oldID, err)
	}

	// A fresh session starts allocating from the bottom of the id space,
	// and the reallocated id must not resurrect the stale topic.
	newID, err := table.Register(clientA, "a/new-temp")
	if err != nil {
		t.Fatalf("Register after CleanSession: %v", err)
	}
	if newID != MinNormalID {
		t.Fatalf("new id = 0x%04X, want 0x%04X", newID, MinNormalID)
	}
	if got, err := table.Resolve(clientA, newID); err != nil || got != "a/new-temp" {
		t.Fatalf("Resolve(new id) = %q, %v; want a/new-temp", got, err)
	}

	// Cleaning one client must not touch another client's session.
	if _, err := table.Resolve(clientB, idBFromOtherSession(t, table)); err != nil {
		t.Fatalf("clientB mapping lost after clientA cleanup: %v", err)
	}
}

func idBFromOtherSession(t *testing.T, table *Table) uint16 {
	t.Helper()
	id, err := table.Register(clientB, "b/keep")
	if err != nil {
		t.Fatalf("Register clientB: %v", err)
	}
	return id
}

// Predefined ids can only reference entries in the calling client's
// whitelist; borrowing another client's predefined id is rejected.
func TestPredefinedIDsRequireWhitelist(t *testing.T) {
	table := newTestTable(t)

	got, err := table.Resolve(clientA, MinPredefinedID)
	if err != nil || got != "a/predefined" {
		t.Fatalf("whitelisted Resolve = %q, %v; want a/predefined", got, err)
	}

	if got, err := table.Resolve(clientB, MinPredefinedID); err != nil || got != "b/predefined" {
		t.Fatalf("clientB Resolve = %q, %v; want b/predefined", got, err)
	}

	// clientB has no entry for 0xFFFF, which clientA is allowed to use.
	if _, err := table.Resolve(clientB, MaxPredefinedID); !errors.Is(err, ErrPredefinedNotAllowed) {
		t.Fatalf("cross-client predefined id: error = %v, want ErrPredefinedNotAllowed", err)
	}

	// A client without any whitelist entries is rejected outright.
	if _, err := table.Resolve("192.0.2.99:29999", MinPredefinedID); !errors.Is(err, ErrPredefinedNotAllowed) {
		t.Fatalf("unwhitelisted client: error = %v, want ErrPredefinedNotAllowed", err)
	}

	// Predefined ids outside the valid range cannot even be configured.
	if _, err := New(PredefinedWhitelist{
		clientA: {MinNormalID: "wrong/range"},
	}); err == nil {
		t.Fatal("New accepted a predefined id in the normal id range")
	}

	// CleanSession must not weaken predefined access control.
	table.CleanSession(clientB)
	if got, err := table.Resolve(clientB, MinPredefinedID); err != nil || got != "b/predefined" {
		t.Fatalf("whitelisted predefined id after cleanup = %q, %v; want b/predefined (whitelist is config, not session state)", got, err)
	}
	if _, err := table.Resolve(clientB, MaxPredefinedID); !errors.Is(err, ErrPredefinedNotAllowed) {
		t.Fatalf("non-whitelisted predefined id after cleanup: error = %v, want ErrPredefinedNotAllowed", err)
	}
}

// Short topic names ride inside the two topic id bytes and are decoded
// byte-for-byte for forwarding without table lookup.
func TestShortTopicNamesForwardedAsIs(t *testing.T) {
	cases := []struct {
		id   uint16
		want string
		raw  uint16
	}{
		{id: 0x6162, want: "ab", raw: 0x6162}, // 'a','b'
		{id: 0x6100, want: "a", raw: 0x6100},  // single byte, trailing zero
		{id: 0x7879, want: "xy", raw: 0x7879},
	}
	for _, tc := range cases {
		if got := ShortTopicName(tc.id); got != tc.want {
			t.Errorf("ShortTopicName(0x%04X) = %q, want %q", tc.id, got, tc.want)
		}
		encoded, err := EncodeShortTopic(tc.want)
		if err != nil || encoded != tc.raw {
			t.Errorf("ShortTopicID(%q) = 0x%04X, %v; want 0x%04X", tc.want, encoded, err, tc.raw)
		}
	}

	if _, err := EncodeShortTopic("too-long"); !errors.Is(err, ErrShortTopicLength) {
		t.Fatalf("EncodeShortTopic length error = %v, want ErrShortTopicLength", err)
	}
	if got := ShortTopicName(ShortTopicID); got != "" {
		t.Fatalf("ShortTopicName(0x0000) = %q, want empty string", got)
	}

	// The short topic id itself must not be interpreted as a table id.
	table := newTestTable(t)
	if _, err := table.Resolve(clientA, ShortTopicID); !errors.Is(err, ErrReservedTopicID) {
		t.Fatalf("Resolve(0x0000): error = %v, want ErrReservedTopicID", err)
	}
}

// Smoke test that concurrent datagram handling keeps the table coherent.
func TestConcurrentAccess(t *testing.T) {
	table := newTestTable(t)
	const goroutines = 16

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(i int) {
			defer wg.Done()
			client := clientA
			topic := "a/concurrent"
			id, err := table.Register(client, topic)
			if err != nil {
				t.Errorf("Register: %v", err)
				return
			}
			if got, err := table.Resolve(client, id); err != nil || got != topic {
				t.Errorf("Resolve = %q, %v; want %q", got, err, topic)
			}
			table.CleanSession(client)
		}(i)
	}
	wg.Wait()
}

// Exhausting the 0x0001..0xFF00 normal id range must surface an error
// rather than overflowing into the predefined range.
func TestRegisterTableFull(t *testing.T) {
	table := newTestTable(t)

	for id := MinNormalID; id <= MaxNormalID; id++ {
		got, err := table.Register(clientA, fmt.Sprintf("topic/%d", id))
		if err != nil {
			t.Fatalf("Register at id 0x%04X: %v", id, err)
		}
		if got != id {
			t.Fatalf("allocated id 0x%04X, want 0x%04X", got, id)
		}
	}

	if _, err := table.Register(clientA, "topic/overflow"); !errors.Is(err, ErrTableFull) {
		t.Fatalf("Register past MaxNormalID: error = %v, want ErrTableFull", err)
	}
}

func TestLookupUnknownClientAndTopic(t *testing.T) {
	table := newTestTable(t)

	if _, ok := table.LookupID("192.0.2.99:29999", "none/registered"); ok {
		t.Fatal("LookupID hit for a client that never registered")
	}
	if _, err := table.Resolve("192.0.2.99:29999", MinNormalID); !errors.Is(err, ErrUnknownTopicID) {
		t.Fatalf("Resolve for unknown client: error = %v, want ErrUnknownTopicID", err)
	}

	if _, err := table.Register(clientA, "a/temp"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, ok := table.LookupID(clientA, "a/missing"); ok {
		t.Fatal("LookupID hit for a topic that was never registered")
	}

	// Client exists, but this particular normal id was never allocated.
	if _, err := table.Resolve(clientA, 1000); !errors.Is(err, ErrUnknownTopicID) {
		t.Fatalf("Resolve unallocated id for known client: error = %v, want ErrUnknownTopicID", err)
	}
}
