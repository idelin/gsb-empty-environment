package netsec_test

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	netsec "example.org/netsec/q0384"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

type testEnv struct {
	backend     *netsec.Backend
	backendPub  ed25519.PublicKey
	stationPriv ed25519.PrivateKey
	otherPriv   ed25519.PrivateKey
	clock       *fakeClock
}

func newTestEnv(t *testing.T) testEnv {
	t.Helper()

	_, backendPriv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate backend key: %v", err)
	}
	backend, err := netsec.NewBackend(backendPriv)
	if err != nil {
		t.Fatalf("new backend: %v", err)
	}

	stationPub, stationPriv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate station key: %v", err)
	}
	otherPub, otherPriv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate other station key: %v", err)
	}

	if err := backend.RegisterStation("station-A", stationPub); err != nil {
		t.Fatalf("register station: %v", err)
	}
	if err := backend.RegisterStation("station-B", otherPub); err != nil {
		t.Fatalf("register other station: %v", err)
	}

	return testEnv{
		backend:     backend,
		backendPub:  backend.PublicKey(),
		stationPriv: stationPriv,
		otherPriv:   otherPriv,
		clock:       &fakeClock{t: time.Unix(1_700_000_000, 0)},
	}
}

func (e testEnv) issue(t *testing.T, auth netsec.Authorization) []byte {
	t.Helper()
	token, err := e.backend.Issue(auth)
	if err != nil {
		t.Fatalf("issue authorization: %v", err)
	}
	return token
}

func sampleAuth(stationID string) netsec.Authorization {
	return netsec.Authorization{
		AuthID:      "auth-1",
		StationID:   stationID,
		ConnectorID: "connector-2",
		AccountID:   "account-42",
		Quota:       100,
		ValidFrom:   time.Unix(1_700_000_000, 0).Unix(),
		ValidUntil:  time.Unix(1_700_000_999, 0).Unix(),
	}
}

func openStation(t *testing.T, env testEnv, stationID, dataDir string) *netsec.Station {
	t.Helper()
	priv := env.stationPriv
	if stationID == "station-B" {
		priv = env.otherPriv
	}
	station, err := netsec.OpenStation(netsec.StationConfig{
		StationID:  stationID,
		PrivateKey: priv,
		BackendKey: env.backendPub,
		DataDir:    dataDir,
		Now:        env.clock.now,
	})
	if err != nil {
		t.Fatalf("open station: %v", err)
	}
	return station
}

func mustEncodeReceipt(t *testing.T, r netsec.SignedReceipt) []byte {
	t.Helper()
	wire, err := netsec.EncodeReceipt(r)
	if err != nil {
		t.Fatalf("encode receipt: %v", err)
	}
	return wire
}

func contains(haystack, needle string) bool { return strings.Contains(haystack, needle) }

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, in)
		return err
	})
}

func TestLocalStoreHoldsOnlyDigestsAndCounters(t *testing.T) {
	env := newTestEnv(t)
	dataDir := filepath.Join(t.TempDir(), "station")
	station := openStation(t, env, "station-A", dataDir)
	token := env.issue(t, sampleAuth("station-A"))

	if err := station.InstallList([][]byte{token}); err != nil {
		t.Fatalf("install list: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dataDir, "offline-auth-state.json"))
	if err != nil {
		t.Fatalf("read state file: %v", err)
	}
	persisted := string(raw)
	for _, secret := range []string{"account-42", "connector-2", "auth-1"} {
		if contains(persisted, secret) {
			t.Fatalf("state file leaks cleartext field %q: %s", secret, persisted)
		}
	}
	if !contains(persisted, `"remaining": 100`) {
		t.Fatalf("state file missing quota counter: %s", persisted)
	}
}

func TestCopiedListRejectedOnAnotherStation(t *testing.T) {
	env := newTestEnv(t)
	dataDir := filepath.Join(t.TempDir(), "station")
	station := openStation(t, env, "station-A", dataDir)
	token := env.issue(t, sampleAuth("station-A"))
	if err := station.InstallList([][]byte{token}); err != nil {
		t.Fatalf("install list: %v", err)
	}

	copiedDir := filepath.Join(t.TempDir(), "copied-station")
	if err := copyDir(dataDir, copiedDir); err != nil {
		t.Fatalf("copy data dir: %v", err)
	}
	if _, err := netsec.OpenStation(netsec.StationConfig{
		StationID:  "station-B",
		PrivateKey: env.otherPriv,
		BackendKey: env.backendPub,
		DataDir:    copiedDir,
		Now:        env.clock.now,
	}); !errors.Is(err, netsec.ErrCopiedStore) {
		t.Fatalf("expected ErrCopiedStore, got %v", err)
	}

	freshDir := filepath.Join(t.TempDir(), "fresh-station")
	other := openStation(t, env, "station-B", freshDir)
	if err := other.InstallList([][]byte{token}); !errors.Is(err, netsec.ErrForeignStation) {
		t.Fatalf("expected ErrForeignStation on install, got %v", err)
	}
	if _, err := other.Consume(token, "connector-2", 10); !errors.Is(err, netsec.ErrForeignStation) {
		t.Fatalf("expected ErrForeignStation on consume, got %v", err)
	}
}

func TestQuotaReuseAndExhaustion(t *testing.T) {
	env := newTestEnv(t)
	station := openStation(t, env, "station-A", filepath.Join(t.TempDir(), "station"))
	token := env.issue(t, sampleAuth("station-A"))
	if err := station.InstallList([][]byte{token}); err != nil {
		t.Fatalf("install list: %v", err)
	}

	receipt1, err := station.Consume(token, "connector-2", 30)
	if err != nil {
		t.Fatalf("first consume: %v", err)
	}
	receipt2, err := station.Consume(token, "connector-2", 70)
	if err != nil {
		t.Fatalf("second consume: %v", err)
	}
	remaining, err := station.Remaining(token)
	if err != nil {
		t.Fatalf("remaining: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("expected zero remaining, got %d", remaining)
	}

	if _, err := station.Consume(token, "connector-2", 1); !errors.Is(err, netsec.ErrQuotaExceeded) {
		t.Fatalf("expected ErrQuotaExceeded, got %v", err)
	}

	if err := station.InstallList([][]byte{token}); err != nil {
		t.Fatalf("reinstall list: %v", err)
	}
	remaining, _ = station.Remaining(token)
	if remaining != 0 {
		t.Fatalf("expected counter to survive reinstall, got %d", remaining)
	}

	wire1 := mustEncodeReceipt(t, receipt1)
	wire2 := mustEncodeReceipt(t, receipt2)
	if err := env.backend.ReceiveReceipt(wire1); err != nil {
		t.Fatalf("receive receipt1: %v", err)
	}
	if err := env.backend.ReceiveReceipt(wire2); err != nil {
		t.Fatalf("receive receipt2: %v", err)
	}
	if err := env.backend.ReceiveReceipt(wire1); !errors.Is(err, netsec.ErrReplay) {
		t.Fatalf("expected ErrReplay, got %v", err)
	}
	consumed, err := env.backend.ConsumedTotal("auth-1")
	if err != nil {
		t.Fatalf("consumed total: %v", err)
	}
	if consumed != 100 {
		t.Fatalf("expected 100 consumed, got %d", consumed)
	}
}

func TestExpiredAndFutureAuthorizationRejected(t *testing.T) {
	env := newTestEnv(t)
	station := openStation(t, env, "station-A", filepath.Join(t.TempDir(), "station"))
	auth := sampleAuth("station-A")
	token := env.issue(t, auth)
	if err := station.InstallList([][]byte{token}); err != nil {
		t.Fatalf("install list: %v", err)
	}

	env.clock.t = time.Unix(auth.ValidUntil+1, 0)
	if _, err := station.Consume(token, "connector-2", 5); !errors.Is(err, netsec.ErrExpired) {
		t.Fatalf("expected ErrExpired, got %v", err)
	}

	env.clock.t = time.Unix(auth.ValidFrom-1, 0)
	if _, err := station.Consume(token, "connector-2", 5); !errors.Is(err, netsec.ErrNotYetValid) {
		t.Fatalf("expected ErrNotYetValid, got %v", err)
	}
}

func TestValidOfflineConsumptionAndReport(t *testing.T) {
	env := newTestEnv(t)
	station := openStation(t, env, "station-A", filepath.Join(t.TempDir(), "station"))
	token := env.issue(t, sampleAuth("station-A"))
	if err := station.InstallList([][]byte{token}); err != nil {
		t.Fatalf("install list: %v", err)
	}

	receipt, err := station.Consume(token, "connector-2", 25)
	if err != nil {
		t.Fatalf("offline consume: %v", err)
	}
	if receipt.Receipt.StationID != "station-A" || receipt.Receipt.Seq != 1 {
		t.Fatalf("unexpected receipt: %+v", receipt.Receipt)
	}
	if station.PendingReceiptCount() != 1 {
		t.Fatalf("expected one pending receipt")
	}

	pending := station.Flush()
	if len(pending) != 1 {
		t.Fatalf("expected one flushed receipt, got %d", len(pending))
	}
	for _, signed := range pending {
		if err := env.backend.ReceiveReceipt(mustEncodeReceipt(t, signed)); err != nil {
			t.Fatalf("backend receive: %v", err)
		}
	}
	if err := station.Ack(len(pending)); err != nil {
		t.Fatalf("ack: %v", err)
	}
	if station.PendingReceiptCount() != 0 {
		t.Fatalf("expected empty outbox after ack")
	}

	consumed, _ := env.backend.ConsumedTotal("auth-1")
	if consumed != 25 {
		t.Fatalf("expected 25 consumed on backend, got %d", consumed)
	}
}

func TestTamperedTokenRejected(t *testing.T) {
	env := newTestEnv(t)
	station := openStation(t, env, "station-A", filepath.Join(t.TempDir(), "station"))
	token := env.issue(t, sampleAuth("station-A"))

	tampered := append([]byte(nil), token...)
	tampered[len(tampered)-1] ^= 0xff
	if err := station.InstallList([][]byte{tampered}); err == nil {
		t.Fatalf("expected tampered token to be rejected")
	}
}

func TestAtomicConcurrentConsumption(t *testing.T) {
	env := newTestEnv(t)
	station := openStation(t, env, "station-A", filepath.Join(t.TempDir(), "station"))
	auth := sampleAuth("station-A")
	auth.Quota = 500
	token := env.issue(t, auth)
	if err := station.InstallList([][]byte{token}); err != nil {
		t.Fatalf("install list: %v", err)
	}

	const goroutines = 50
	var wg sync.WaitGroup
	var success, rejected int
	var mu sync.Mutex
	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := station.Consume(token, "connector-2", 10)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				success++
			case errors.Is(err, netsec.ErrQuotaExceeded):
				rejected++
			default:
				t.Errorf("unexpected consume error: %v", err)
			}
		}()
	}
	wg.Wait()

	if success != 50 || rejected != 0 {
		t.Fatalf("expected 50 successful and 0 rejected, got success=%d rejected=%d", success, rejected)
	}
	remaining, _ := station.Remaining(token)
	if remaining != 0 {
		t.Fatalf("expected zero remaining, got %d", remaining)
	}

	for _, signed := range station.Flush() {
		if err := env.backend.ReceiveReceipt(mustEncodeReceipt(t, signed)); err != nil {
			t.Fatalf("backend rejected chained receipt: %v", err)
		}
	}
	consumed, _ := env.backend.ConsumedTotal("auth-1")
	if consumed != 500 {
		t.Fatalf("expected 500 consumed, got %d", consumed)
	}
}

func TestStateSurvivesRestartAndChainContinues(t *testing.T) {
	env := newTestEnv(t)
	dataDir := filepath.Join(t.TempDir(), "station")
	station := openStation(t, env, "station-A", dataDir)
	token := env.issue(t, sampleAuth("station-A"))
	if err := station.InstallList([][]byte{token}); err != nil {
		t.Fatalf("install list: %v", err)
	}
	if _, err := station.Consume(token, "connector-2", 40); err != nil {
		t.Fatalf("first consume: %v", err)
	}

	reopened := openStation(t, env, "station-A", dataDir)
	remaining, err := reopened.Remaining(token)
	if err != nil {
		t.Fatalf("remaining after restart: %v", err)
	}
	if remaining != 60 {
		t.Fatalf("expected 60 remaining after restart, got %d", remaining)
	}
	if reopened.PendingReceiptCount() != 1 {
		t.Fatalf("expected outbox to survive restart")
	}

	receipt, err := reopened.Consume(token, "connector-2", 60)
	if err != nil {
		t.Fatalf("second consume after restart: %v", err)
	}
	if receipt.Receipt.Seq != 2 {
		t.Fatalf("expected sequence to continue at 2, got %d", receipt.Receipt.Seq)
	}

	for _, signed := range reopened.Flush() {
		if err := env.backend.ReceiveReceipt(mustEncodeReceipt(t, signed)); err != nil {
			t.Fatalf("backend rejected receipt across restart: %v", err)
		}
	}
}

func TestBackendRejectsOverQuotaAcrossStations(t *testing.T) {
	env := newTestEnv(t)
	station := openStation(t, env, "station-A", filepath.Join(t.TempDir(), "station"))
	token := env.issue(t, sampleAuth("station-A"))
	if err := station.InstallList([][]byte{token}); err != nil {
		t.Fatalf("install list: %v", err)
	}

	receipt, err := station.Consume(token, "connector-2", 100)
	if err != nil {
		t.Fatalf("consume full quota: %v", err)
	}
	if err := env.backend.ReceiveReceipt(mustEncodeReceipt(t, receipt)); err != nil {
		t.Fatalf("receive legitimate receipt: %v", err)
	}

	// A forged receipt reusing the station's legitimate signature over a
	// modified payload must fail the backend signature check before any quota
	// accounting is touched.
	forged := receipt
	forged.Receipt.Amount = 1
	forged.Receipt.Seq = 2
	if err := env.backend.ReceiveReceipt(mustEncodeReceipt(t, forged)); !errors.Is(err, netsec.ErrBadSignature) {
		t.Fatalf("expected tampered receipt to fail signature check, got %v", err)
	}
}

func TestFullListSyncRevokesRemovedAuthorization(t *testing.T) {
	env := newTestEnv(t)
	station := openStation(t, env, "station-A", filepath.Join(t.TempDir(), "station"))
	token1 := env.issue(t, sampleAuth("station-A"))

	auth2 := sampleAuth("station-A")
	auth2.AuthID = "auth-2"
	token2 := env.issue(t, auth2)

	if err := station.InstallList([][]byte{token1, token2}); err != nil {
		t.Fatalf("install initial list: %v", err)
	}

	// A later synchronization pushes only auth-2; auth-1 must be revoked.
	if err := station.InstallList([][]byte{token2}); err != nil {
		t.Fatalf("install reduced list: %v", err)
	}
	if _, err := station.Consume(token1, "connector-2", 1); !errors.Is(err, netsec.ErrUnknownAuthorization) {
		t.Fatalf("expected revoked authorization to be rejected, got %v", err)
	}
	if _, err := station.Consume(token2, "connector-2", 5); err != nil {
		t.Fatalf("retained authorization should still consume: %v", err)
	}
}

func TestConnectorBindingEnforced(t *testing.T) {
	env := newTestEnv(t)
	station := openStation(t, env, "station-A", filepath.Join(t.TempDir(), "station"))
	token := env.issue(t, sampleAuth("station-A"))
	if err := station.InstallList([][]byte{token}); err != nil {
		t.Fatalf("install list: %v", err)
	}
	if _, err := station.Consume(token, "connector-9", 5); !errors.Is(err, netsec.ErrForeignStation) {
		t.Fatalf("expected connector mismatch to be rejected, got %v", err)
	}
}

func TestRecoversFromCrashedWriteOrder(t *testing.T) {
	env := newTestEnv(t)
	dataDir := filepath.Join(t.TempDir(), "station")
	station := openStation(t, env, "station-A", dataDir)
	token := env.issue(t, sampleAuth("station-A"))
	if err := station.InstallList([][]byte{token}); err != nil {
		t.Fatalf("install list: %v", err)
	}
	receipt, err := station.Consume(token, "connector-2", 10)
	if err != nil {
		t.Fatalf("consume: %v", err)
	}

	// Simulate a crash after the outbox was written but before the state file
	// advanced: roll the state back to the genesis-era counters while keeping
	// the unacknowledged receipt in the outbox.
	staleState := map[string]any{
		"station_id": "station-A",
		"entries":    []map[string]any{{"digest": receipt.Receipt.Digest, "remaining": 100}},
		"next_seq":   1,
		"chain_head": netsec.GenesisHead("station-A"),
	}
	raw, err := json.Marshal(staleState)
	if err != nil {
		t.Fatalf("marshal stale state: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "offline-auth-state.json"), raw, 0o600); err != nil {
		t.Fatalf("write stale state: %v", err)
	}

	reopened := openStation(t, env, "station-A", dataDir)
	if reopened.PendingReceiptCount() != 1 {
		t.Fatalf("expected receipt retained in outbox")
	}
	next, err := reopened.Consume(token, "connector-2", 5)
	if err != nil {
		t.Fatalf("consume after recovery: %v", err)
	}
	if next.Receipt.Seq != 2 {
		t.Fatalf("expected sequence rebuilt to 2, got %d", next.Receipt.Seq)
	}

	for _, signed := range reopened.Flush() {
		if err := env.backend.ReceiveReceipt(mustEncodeReceipt(t, signed)); err != nil {
			t.Fatalf("backend must accept recovered chain: %v", err)
		}
	}
}
