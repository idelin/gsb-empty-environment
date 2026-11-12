package netsec_test

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	netsec "example.org/netsec/q0195"
)

// fakeDeriver is an injected, deliberately expensive verifier.
//
// It mimics a KDF: deriveCost sleeps to make derivation visibly expensive,
// every call is recorded with the exact parameters supplied, and a single
// call may be blocked until release is closed so concurrency tests stay
// deterministic.
type fakeDeriver struct {
	mu         sync.Mutex
	deriveCost time.Duration
	calls      []call
	block      chan struct{}
	blockOnce  sync.Once
	err        error
}

type call struct {
	password string
	params   netsec.Parameters
}

func newFakeDeriver(cost time.Duration) *fakeDeriver {
	return &fakeDeriver{deriveCost: cost}
}

func (f *fakeDeriver) Derive(password string, params netsec.Parameters) ([]byte, error) {
	f.mu.Lock()
	block := f.block
	err := f.err
	f.mu.Unlock()
	if block != nil {
		<-block
	}
	time.Sleep(f.deriveCost)

	f.mu.Lock()
	defer f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	f.calls = append(f.calls, call{password: password, params: params.Clone()})
	return fakeHash(password, params), nil
}

func (f *fakeDeriver) blockNext()    { f.block = make(chan struct{}) }
func (f *fakeDeriver) releaseBlock() { f.blockOnce.Do(func() { close(f.block) }) }

func (f *fakeDeriver) recordedCalls() []call {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]call, len(f.calls))
	copy(out, f.calls)
	return out
}

func (f *fakeDeriver) resetCalls() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = nil
}

func (f *fakeDeriver) failWith(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

// fakeHash is deterministic but distinct per algorithm, cost and salt.
func fakeHash(password string, params netsec.Parameters) []byte {
	prefix := []byte(fmt.Sprintf("%s|%d|", params.Algorithm, params.Cost))
	salted := append(append([]byte(nil), params.Salt...), []byte("|"+password)...)
	return append(prefix, salted...)
}

func paramsFor(algo string, cost int, salt string) netsec.Parameters {
	return netsec.Parameters{Algorithm: algo, Cost: cost, Salt: []byte(salt)}
}

func recordFor(version int, password, algo string, cost int, salt string) netsec.Record {
	params := paramsFor(algo, cost, salt)
	return netsec.Record{
		Version:   version,
		Algorithm: params.Algorithm,
		Cost:      params.Cost,
		Salt:      append([]byte(nil), params.Salt...),
		Hash:      fakeHash(password, params),
	}
}

func assertParams(t *testing.T, got, want netsec.Parameters) {
	t.Helper()
	if got.Algorithm != want.Algorithm || got.Cost != want.Cost || !bytes.Equal(got.Salt, want.Salt) {
		t.Fatalf("derive params = %+v, want %+v", got, want)
	}
}

func TestIsHistoricalUsesEachRecordsOwnParameters(t *testing.T) {
	deriver := newFakeDeriver(2 * time.Millisecond)
	oldParams := paramsFor("argon2id", 3, "old-salt")
	newParams := paramsFor("argon2id", 5, "new-salt")

	store, err := netsec.Load(deriver, 10, []netsec.Record{
		recordFor(1, "hunter2", oldParams.Algorithm, oldParams.Cost, string(oldParams.Salt)),
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	historical, err := store.IsHistorical("hunter2")
	if err != nil {
		t.Fatalf("IsHistorical: %v", err)
	}
	if !historical {
		t.Fatal("known historical password must be rejected")
	}

	calls := deriver.recordedCalls()
	if len(calls) != 1 {
		t.Fatalf("deriver calls = %d, want 1", len(calls))
	}
	assertParams(t, calls[0].params, oldParams)

	// The new password is derived with the new parameters when appended; the
	// historical record's cost and salt must never leak into it.
	newRecord, err := store.ChangePassword(store.Version(), "correct horse", newParams)
	if err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	if newRecord.Cost != newParams.Cost || !bytes.Equal(newRecord.Salt, newParams.Salt) {
		t.Fatalf("new record bound to %+v, want %+v", newRecord.Parameters(), newParams)
	}

	unknown, err := store.IsHistorical("never used before")
	if err != nil || unknown {
		t.Fatalf("IsHistorical unknown = (%v, %v), want (false, nil)", unknown, err)
	}

	// Every comparison must use the matching record's own parameters, here
	// identified by its distinct salt.
	for _, recorded := range deriver.recordedCalls() {
		switch string(recorded.params.Salt) {
		case "old-salt":
			assertParams(t, recorded.params, oldParams)
		case "new-salt":
			assertParams(t, recorded.params, newParams)
		default:
			t.Fatalf("unexpected salt %q in derive call", recorded.params.Salt)
		}
	}
}

func TestCorruptRecordsAndParametersAreRejected(t *testing.T) {
	deriver := newFakeDeriver(time.Millisecond)

	valid := recordFor(1, "pw", "argon2id", 2, "salt")
	corruptCases := []netsec.Record{
		{Version: 2, Algorithm: "", Cost: 2, Salt: []byte("s"), Hash: []byte("h")},
		{Version: 3, Algorithm: "argon2id", Cost: 0, Salt: []byte("s"), Hash: []byte("h")},
		{Version: 4, Algorithm: "argon2id", Cost: 2, Salt: nil, Hash: []byte("h")},
		{Version: 5, Algorithm: "argon2id", Cost: 2, Salt: []byte("s"), Hash: nil},
		{Version: 0, Algorithm: "argon2id", Cost: 2, Salt: []byte("s"), Hash: []byte("h")},
	}

	for index, corrupt := range corruptCases {
		if _, err := netsec.Load(deriver, 10, []netsec.Record{valid, corrupt}); !errors.Is(err, netsec.ErrCorruptRecord) {
			t.Fatalf("corrupt case %d: Load err = %v, want ErrCorruptRecord", index, err)
		}
	}

	store, err := netsec.Load(deriver, 10, []netsec.Record{valid})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Damaged parameters on new input must be refused before any derive call
	// and leave history untouched.
	before := store.Records()
	badParams := []netsec.Parameters{
		{Algorithm: "", Cost: 3, Salt: []byte("s")},
		{Algorithm: "argon2id", Cost: -1, Salt: []byte("s")},
		{Algorithm: "argon2id", Cost: 3, Salt: nil},
	}
	for index, params := range badParams {
		if _, err := store.ChangePassword(store.Version(), "fresh pw", params); !errors.Is(err, netsec.ErrCorruptRecord) {
			t.Fatalf("bad params case %d: err = %v, want ErrCorruptRecord", index, err)
		}
	}
	if len(deriver.recordedCalls()) != 0 {
		t.Fatal("damaged parameters must not reach the deriver")
	}
	after := store.Records()
	if len(after) != len(before) || after[0].Version != before[0].Version {
		t.Fatal("failed validation must preserve the old history")
	}

	// A deriver failure against a historical record must surface instead of
	// silently treating the password as unused.
	boom := errors.New("kdf unavailable")
	deriver.failWith(boom)
	if _, err := store.IsHistorical("anything"); !errors.Is(err, boom) {
		t.Fatalf("deriver failure err = %v, want wrapped %v", err, boom)
	}
}

func TestConcurrentChangePasswordOneWinnerKeepsOldHistory(t *testing.T) {
	deriver := newFakeDeriver(5 * time.Millisecond)
	store, err := netsec.Load(deriver, 10, []netsec.Record{
		recordFor(1, "original", "argon2id", 2, "salt-1"),
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	deriver.blockNext()
	start := make(chan struct{})
	type result struct {
		record netsec.Record
		err    error
	}
	results := make(chan result, 2)

	change := func(password string, params netsec.Parameters) {
		<-start
		record, err := store.ChangePassword(1, password, params)
		results <- result{record: record, err: err}
	}

	go change("alpha password", paramsFor("argon2id", 3, "salt-a"))
	go change("beta password", paramsFor("argon2id", 4, "salt-b"))

	close(start)
	time.Sleep(20 * time.Millisecond)
	deriver.releaseBlock()

	var successes, conflicts int
	var winner result
	for index := 0; index < 2; index++ {
		res := <-results
		switch {
		case res.err == nil:
			successes++
			winner = res
		case errors.Is(res.err, netsec.ErrVersionConflict):
			conflicts++
		default:
			t.Fatalf("unexpected change error: %v", res.err)
		}
	}

	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes = %d, conflicts = %d, want 1 and 1", successes, conflicts)
	}
	if store.Version() != 2 || store.Len() != 2 {
		t.Fatalf("store = version %d len %d, want version 2 len 2", store.Version(), store.Len())
	}

	records := store.Records()
	if records[0].Version != 1 || records[0].Cost != 2 || string(records[0].Salt) != "salt-1" {
		t.Fatalf("old history altered: %+v", records[0])
	}
	records[1].Version = winner.record.Version
	if records[1].Version != 2 || !bytes.Equal(records[1].Hash, winner.record.Hash) {
		t.Fatal("committed record does not match the single winner")
	}

	// Follow-up calls must observe the bumped version.
	if _, err := store.ChangePassword(1, "stale again", paramsFor("argon2id", 5, "salt-c")); !errors.Is(err, netsec.ErrVersionConflict) {
		t.Fatalf("stale follow-up err = %v, want ErrVersionConflict", err)
	}
	third, err := store.ChangePassword(2, "gamma password", paramsFor("argon2id", 5, "salt-c"))
	if err != nil {
		t.Fatalf("sequential change at current version: %v", err)
	}
	if third.Version != 3 || store.Version() != 3 {
		t.Fatalf("version after sequential change = record %d store %d, want 3", third.Version, store.Version())
	}
}

func TestTrimmingDropsOldestButNeverCurrent(t *testing.T) {
	deriver := newFakeDeriver(time.Millisecond)
	const limit = 3
	store := netsec.NewStore(deriver, limit)

	passwords := []string{"pw-1", "pw-2", "pw-3", "pw-4", "pw-5"}
	var current netsec.Record
	for index, password := range passwords {
		version := index + 1
		salt := fmt.Sprintf("salt-%d", version)
		record, err := store.ChangePassword(store.Version(), password, paramsFor("argon2id", 2, salt))
		if err != nil {
			t.Fatalf("change %d: %v", version, err)
		}
		current = record
	}

	if store.Len() != limit {
		t.Fatalf("len = %d, want limit %d", store.Len(), limit)
	}

	records := store.Records()
	for offset, wantVersion := range []int{3, 4, 5} {
		if records[offset].Version != wantVersion {
			t.Fatalf("retained version at %d = %d, want %d", offset, records[offset].Version, wantVersion)
		}
	}

	gotCurrent, ok := store.Current()
	if !ok || gotCurrent.Version != current.Version || !bytes.Equal(gotCurrent.Hash, current.Hash) {
		t.Fatalf("current record lost during trimming: %+v ok=%v", gotCurrent, ok)
	}

	// The current password is still rejected; trimmed salts must never be
	// derived again.
	deriver.resetCalls()
	reused, err := store.IsHistorical("pw-5")
	if err != nil || !reused {
		t.Fatalf("IsHistorical current = (%v, %v), want (true, nil)", reused, err)
	}
	for _, recorded := range deriver.recordedCalls() {
		if string(recorded.params.Salt) == "salt-1" || string(recorded.params.Salt) == "salt-2" {
			t.Fatalf("trimmed record salt %q must never be derived again", recorded.params.Salt)
		}
	}
}

func TestRejectionsPreserveHistoryAndRejectReuse(t *testing.T) {
	deriver := newFakeDeriver(time.Millisecond)
	store, err := netsec.Load(deriver, 5, []netsec.Record{
		recordFor(1, "same old", "argon2id", 2, "salt-1"),
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if _, err := store.ChangePassword(store.Version(), "same old", paramsFor("argon2id", 9, "brand-new-salt")); !errors.Is(err, netsec.ErrReusedPassword) {
		t.Fatalf("reuse err = %v, want ErrReusedPassword", err)
	}
	if store.Version() != 1 || store.Len() != 1 {
		t.Fatalf("reused-password change mutated history: version %d len %d", store.Version(), store.Len())
	}

	if _, err := store.ChangePassword(99, "fresh enough", paramsFor("argon2id", 3, "salt-2")); !errors.Is(err, netsec.ErrVersionConflict) {
		t.Fatalf("conflict err = %v, want ErrVersionConflict", err)
	}
	if store.Version() != 1 || store.Len() != 1 {
		t.Fatalf("conflicting change mutated history: version %d len %d", store.Version(), store.Len())
	}

	record, err := store.ChangePassword(store.Version(), "fresh enough", paramsFor("argon2id", 3, "salt-2"))
	if err != nil {
		t.Fatalf("valid change: %v", err)
	}
	if record.Version != 2 || store.Version() != 2 || store.Len() != 2 {
		t.Fatalf("valid change state = record %d version %d len %d, want 2/2/2",
			record.Version, store.Version(), store.Len())
	}

	// Defensive copies must isolate stored salts and hashes from callers.
	snapshot := store.Records()
	snapshot[0].Salt[0] = 'X'
	snapshot[0].Hash[0] = 'Y'
	again, err := store.IsHistorical("same old")
	if err != nil || !again {
		t.Fatalf("history corrupted through snapshot: (%v, %v)", again, err)
	}
}
