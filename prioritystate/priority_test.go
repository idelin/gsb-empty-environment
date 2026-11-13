package prioritystate

import (
	"errors"
	"testing"
)

func isConnErr(err error) bool {
	var connErr *ConnError
	return errors.As(err, &connErr)
}

func frameStreamID(streamID uint32) []byte {
	return []byte{
		byte(streamID >> 24),
		byte(streamID >> 16),
		byte(streamID >> 8),
		byte(streamID),
	}
}

func update(t *testing.T, s *State, streamID uint32, field string) error {
	t.Helper()
	payload := append(frameStreamID(streamID), []byte(field)...)
	return s.HandleFrame(payload)
}

func openAndGet(t *testing.T, s *State, streamID uint32) (Entry, bool) {
	t.Helper()
	if _, err := s.StreamOpened(streamID); err != nil {
		t.Fatalf("StreamOpened(%d): %v", streamID, err)
	}
	return s.Priority(streamID)
}

func TestEvenAndZeroStreamIDsAreConnectionErrors(t *testing.T) {
	s := New()

	for _, streamID := range []uint32{0, 2, 4, 100, 1<<31 + 2} {
		err := update(t, s, streamID, "u=1")
		if !isConnErr(err) {
			t.Fatalf("stream %d: expected connection error, got %v", streamID, err)
		}
		if !errors.Is(err, ErrProtocolError) {
			t.Fatalf("stream %d: expected ErrProtocolError, got %v", streamID, err)
		}
	}
	if got := s.PendingCount(); got != 0 {
		t.Fatalf("invalid stream must not create state, pending=%d", got)
	}
}

func TestShortAndReservedBitPayloads(t *testing.T) {
	s := New()

	if err := s.HandleFrame([]byte{0, 0, 1}); !isConnErr(err) {
		t.Fatalf("short payload: expected connection error, got %v", err)
	}

	// The R reserved bit must be masked off; the odd identifier is accepted.
	payload := []byte{0x80, 0, 0, 3, 'u', '=', '0'}
	if err := s.HandleFrame(payload); err != nil {
		t.Fatalf("R-bit masking: unexpected error %v", err)
	}
	if _, ok := s.Priority(3); ok {
		t.Fatal("stream 3 is still pending, must not report open priority")
	}
}

func TestUrgencyOutOfRangeRejected(t *testing.T) {
	s := New()

	cases := []string{
		"u=8",
		"u=9",
		"u=-1",
		"u=9999999999999999", // 16 digits: structural integer overflow
	}
	for _, field := range cases {
		if err := update(t, s, 3, field); !errors.Is(err, ErrMalformedField) {
			t.Fatalf("field %q: expected ErrMalformedField, got %v", field, err)
		}
	}
	if got := s.PendingCount(); got != 0 {
		t.Fatalf("rejected field must not create state, pending=%d", got)
	}

	// Boundaries 0 and 7 are valid.
	for _, pair := range []struct {
		id uint32
		u  string
	}{{5, "u=0"}, {7, "u=7"}} {
		if err := update(t, s, pair.id, pair.u); err != nil {
			t.Fatalf("field %q: unexpected error %v", pair.u, err)
		}
	}
}

func TestWrongTypeDiscardsWholeField(t *testing.T) {
	s := New()

	// Establish a valid pending entry that malformed updates must not touch.
	if err := update(t, s, 3, "u=1, i=?1"); err != nil {
		t.Fatalf("seed update: %v", err)
	}

	bad := []string{
		"i=5",          // i must be boolean
		"u=?0",         // u must be integer
		"u=2.0",        // decimal is not an integer
		`u="3"`,        // string is not an integer
		"u=(1 2)",      // inner list is not an integer
		"u=1,i=?0,i=7", // last i has wrong type: whole value discarded
	}
	for _, field := range bad {
		err := update(t, s, 3, field)
		if !errors.Is(err, ErrMalformedField) {
			t.Fatalf("field %q: expected ErrMalformedField, got %v", field, err)
		}
	}

	// The seed entry must be unchanged after every rejection.
	entry, _ := openAndGet(t, s, 3)
	if entry != (Entry{Urgency: 1, Incremental: true}) {
		t.Fatalf("seed entry mutated by rejected updates: %+v", entry)
	}

	// Parameters are part of the grammar and tolerated.
	if err := update(t, s, 5, "u=1; foo=bar, i=?0; rel=1"); err != nil {
		t.Fatalf("parameters should be tolerated: %v", err)
	}
	if entry, ok := openAndGet(t, s, 5); !ok || entry != (Entry{Urgency: 1}) {
		t.Fatalf("parameter-tolerant parse: got %+v ok=%v", entry, ok)
	}
}
func TestDuplicateKeysLastWins(t *testing.T) {
	s := New()

	if err := update(t, s, 3, "u=7, u=0, i=?0, i=?1"); err != nil {
		t.Fatalf("duplicate keys: %v", err)
	}
	entry, ok := openAndGet(t, s, 3)
	if !ok {
		t.Fatal("stream 3 not open")
	}
	if entry.Urgency != 0 || !entry.Incremental {
		t.Fatalf("last-wins: got %+v, want {0 true}", entry)
	}
}

func TestUnknownKeysIgnored(t *testing.T) {
	s := New()

	fields := []string{
		"unknown=42, u=2",
		"u=2, unknown=:aGVsbG8=:",
		"unknown, u=4, i=?1", // bare member means boolean true
		`x="str", token, u=6`,
	}
	for idx, field := range fields {
		id := uint32(3 + idx*2)
		if err := update(t, s, id, field); err != nil {
			t.Fatalf("field %q: %v", field, err)
		}
		if got := s.PendingCount(); got != idx+1 {
			t.Fatalf("field %q: pending=%d", field, got)
		}
	}

	// Empty and unknown-only dictionaries are structurally valid and default.
	for _, field := range []string{"", "   ", "foo=1"} {
		s2 := New()
		if err := update(t, s2, 5, field); err != nil {
			t.Fatalf("field %q: %v", field, err)
		}
		entry, _ := openAndGet(t, s2, 5)
		if entry != DefaultEntry {
			t.Fatalf("field %q: got %+v, want default", field, entry)
		}
	}
}

func TestMalformedStructuredFields(t *testing.T) {
	s := New()
	bad := []string{
		"u=",                  // equals with no value
		"=1",                  // missing key
		"u 1",                 // missing equals
		"u=1,",                // trailing comma
		",u=1",                // leading comma
		"u=1 i=?1",            // missing comma
		"u=10000000000000000", // integer too long
		"u=1.2.3",             // bad decimal
		`u="unterminated`,
		"i=?",
		"u=1; =2", // bad parameter key
		"u=:abc",  // unterminated byte sequence
		"U=1",     // keys are lowercase only
	}
	for _, field := range bad {
		if err := update(t, s, 3, field); !errors.Is(err, ErrMalformedField) {
			t.Fatalf("field %q: expected ErrMalformedField, got %v", field, err)
		}
	}
	if got := s.PendingCount(); got != 0 {
		t.Fatalf("malformed fields must not create state, pending=%d", got)
	}
}

func TestPendingEvictionIsFIFO(t *testing.T) {
	s := New()

	// 64 distinct odd stream identifiers: 3, 5, ..., 129.
	for id := uint32(3); id <= 129; id += 2 {
		if err := update(t, s, id, "u=1"); err != nil {
			t.Fatalf("id %d: %v", id, err)
		}
	}
	if got := s.PendingCount(); got != MaxPendingEntries {
		t.Fatalf("pending=%d, want %d", got, MaxPendingEntries)
	}

	// The 65th pending entry evicts the oldest (stream 3).
	if err := update(t, s, 131, "u=2"); err != nil {
		t.Fatalf("evicting update: %v", err)
	}
	if got := s.PendingCount(); got != MaxPendingEntries {
		t.Fatalf("pending after eviction=%d, want %d", got, MaxPendingEntries)
	}
	if _, ok := s.Priority(3); ok {
		t.Fatal("evicted stream 3 must have no state")
	}

	// The surviving chain starts at 5 with its original entry.
	if entry, ok := openAndGet(t, s, 5); !ok || entry.Urgency != 1 {
		t.Fatalf("stream 5 should survive eviction: %+v ok=%v", entry, ok)
	}

	// The newest entry is at the tail.
	if entry, ok := openAndGet(t, s, 131); !ok || entry.Urgency != 2 {
		t.Fatalf("stream 131 should be the newest entry: %+v ok=%v", entry, ok)
	}

	// After streams open, pending capacity frees up and skipped identifiers
	// stay ignored.
	if err := update(t, s, 7, "u=5"); err != nil {
		t.Fatalf("skipped stream update: %v", err)
	}
	if _, ok := s.Priority(7); ok {
		t.Fatal("skipped stream 7 must retain no state")
	}
}

func TestStalePendingEntriesArePurged(t *testing.T) {
	s := New()

	// A low pending entry followed by higher ones.
	if err := update(t, s, 5, "u=0"); err != nil {
		t.Fatal(err)
	}
	if err := update(t, s, 101, "u=1"); err != nil {
		t.Fatal(err)
	}
	if err := update(t, s, 103, "u=2"); err != nil {
		t.Fatal(err)
	}

	// Opening a stream above 5 keeps 5 possible (it may open next) and leaves
	// all pending entries intact.
	if _, err := s.StreamOpened(9); err != nil {
		t.Fatal(err)
	}
	if got := s.PendingCount(); got != 3 {
		t.Fatalf("pending entries below the opened identifier are still possible, got %d", got)
	}

	// Opening stream 5 makes every lower odd identifier impossible; there are
	// none pending yet, but skipped identifiers now allocate no state.
	if _, err := s.StreamOpened(5); err != nil {
		t.Fatal(err)
	}
	if err := update(t, s, 11, "u=4"); err != nil {
		t.Fatal(err)
	}
	if got := s.PendingCount(); got != 3 {
		t.Fatalf("pending count after opening 5: got %d, want 3", got)
	}

	// Updates for identifiers skipped below the lowest opened stream allocate
	// nothing.
	if err := update(t, s, 3, "u=2"); err != nil {
		t.Fatal(err)
	}
	if got := s.PendingCount(); got != 3 {
		t.Fatalf("impossible stream update must not allocate state, got %d", got)
	}
	if _, ok := s.Priority(3); ok {
		t.Fatal("skipped stream 3 must have no state")
	}

	// Future identifiers still get normal pending entries.
	if err := update(t, s, 201, "u=5"); err != nil {
		t.Fatal(err)
	}
	if got := s.PendingCount(); got != 4 {
		t.Fatalf("future stream must be retained, got %d", got)
	}

	// A stale pending entry below a newly opened low identifier is purged.
	s2 := New()
	if err := update(t, s2, 3, "u=1"); err != nil {
		t.Fatal(err)
	}
	if err := update(t, s2, 7, "u=2"); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.StreamOpened(5); err != nil {
		t.Fatal(err)
	}
	if err := update(t, s2, 9, "u=3"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s2.Priority(3); ok {
		t.Fatal("stream 3 is now impossible and must be purged")
	}
	if entry, ok := openAndGet(t, s2, 7); !ok || entry.Urgency != 2 {
		t.Fatalf("stream 7 should remain pending: %+v ok=%v", entry, ok)
	}
}

func TestClosedStreamUpdatesIgnored(t *testing.T) {
	s := New()

	if err := update(t, s, 3, "u=1, i=?1"); err != nil {
		t.Fatal(err)
	}
	entry, _ := openAndGet(t, s, 3)
	if entry.Urgency != 1 || !entry.Incremental {
		t.Fatalf("pre-close state: %+v", entry)
	}

	s.StreamClosed(3)
	if _, ok := s.Priority(3); ok {
		t.Fatal("closed stream must not report an entry")
	}
	if got := s.PendingCount(); got != 0 {
		t.Fatalf("closed stream must leave pending queue, got %d", got)
	}

	// Updates after close are silently ignored and never re-create state.
	if err := update(t, s, 3, "u=7, i=?0"); err != nil {
		t.Fatalf("post-close update must be ignored, got %v", err)
	}
	if _, ok := s.Priority(3); ok {
		t.Fatal("post-close update must not re-create state")
	}
	if got := s.PendingCount(); got != 0 {
		t.Fatalf("post-close update must not enter pending queue, got %d", got)
	}

	// Reopening a closed identifier is a connection error.
	if _, err := s.StreamOpened(3); !isConnErr(err) {
		t.Fatalf("reopen closed stream: expected connection error, got %v", err)
	}
}

func TestPendingAdoptedOnOpen(t *testing.T) {
	s := New()

	// No update before open: default entry.
	entry, err := s.StreamOpened(3)
	if err != nil {
		t.Fatal(err)
	}
	if entry != DefaultEntry {
		t.Fatalf("default open: got %+v", entry)
	}

	// Update before open: adopted when the stream opens.
	if err := update(t, s, 5, "u=0, i=?1"); err != nil {
		t.Fatal(err)
	}
	if got := s.PendingCount(); got != 1 {
		t.Fatalf("pending=%d", got)
	}
	entry, err = s.StreamOpened(5)
	if err != nil {
		t.Fatal(err)
	}
	if entry.Urgency != 0 || !entry.Incremental {
		t.Fatalf("adopted entry: %+v", entry)
	}
	if got := s.PendingCount(); got != 0 {
		t.Fatalf("open must drain pending queue, got %d", got)
	}

	// Updates after open mutate the live entry.
	if err := update(t, s, 5, "u=6"); err != nil {
		t.Fatal(err)
	}
	entry, ok := s.Priority(5)
	if !ok || entry.Urgency != 6 || entry.Incremental {
		t.Fatalf("open-stream update: %+v ok=%v", entry, ok)
	}

	// A malformed update leaves the live entry untouched.
	if err := update(t, s, 5, "u=8"); !errors.Is(err, ErrMalformedField) {
		t.Fatalf("u=8: %v", err)
	}
	if entry, _ := s.Priority(5); entry.Urgency != 6 {
		t.Fatalf("malformed update mutated state: %+v", entry)
	}
}

func TestPendingUpdateReplacesEntryInPlace(t *testing.T) {
	s := New()

	if err := update(t, s, 3, "u=1"); err != nil {
		t.Fatal(err)
	}
	if err := update(t, s, 5, "u=2"); err != nil {
		t.Fatal(err)
	}
	if err := update(t, s, 3, "u=7"); err != nil {
		t.Fatal(err)
	}

	// Repeated pending updates keep FIFO position but replace the entry.
	entry, _ := openAndGet(t, s, 3)
	if entry.Urgency != 7 {
		t.Fatalf("in-place replace: got urgency %d, want 7", entry.Urgency)
	}
	entry, _ = openAndGet(t, s, 5)
	if entry.Urgency != 2 {
		t.Fatalf("stream 5 entry: got urgency %d, want 2", entry.Urgency)
	}
}
