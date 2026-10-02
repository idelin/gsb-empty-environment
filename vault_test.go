package tracevault

import (
	"bytes"
	"fmt"
	"sync"
	"testing"
	"time"
)

var (
	testMasterKey    = bytes.Repeat([]byte{0xA1}, 32)
	testRegulatorKey = bytes.Repeat([]byte{0xB2}, 32)
	testSalt         = bytes.Repeat([]byte{0xC3}, 32)
	testBase         = time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
)

func newTestVault(t *testing.T) *Vault {
	t.Helper()
	v, err := NewVault(Config{
		MasterKey:    testMasterKey,
		RegulatorKey: testRegulatorKey,
		Salt:         testSalt,
		EpochSeconds: 3600,
		GridSize:     0.01,
	})
	if err != nil {
		t.Fatalf("NewVault: %v", err)
	}
	return v
}

func issueTestCredential(t *testing.T, v *Vault, entityID string, from, to time.Time) Credential {
	t.Helper()
	now := testBase.Add(48 * time.Hour)
	cred, err := IssueCredential(testRegulatorKey, entityID,
		v.EpochOf(from), v.EpochOf(to), "test-court",
		now.Add(-time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fatalf("IssueCredential: %v", err)
	}
	return cred
}

func TestPseudonymTimeVarying(t *testing.T) {
	p1 := Pseudonym(testMasterKey, "user-1", 100)
	if p1 != Pseudonym(testMasterKey, "user-1", 100) {
		t.Fatal("pseudonym must be deterministic for same entity+epoch")
	}
	if p1 == Pseudonym(testMasterKey, "user-1", 101) {
		t.Fatal("pseudonym must rotate across epochs")
	}
	if p1 == Pseudonym(testMasterKey, "user-2", 100) {
		t.Fatal("pseudonym must differ across entities")
	}
	if p1 == Pseudonym(bytes.Repeat([]byte{0xFF}, 32), "user-1", 100) {
		t.Fatal("pseudonym must depend on the master key")
	}
}

func TestIngestStoresNoPlaintext(t *testing.T) {
	v := newTestVault(t)
	p := Point{EntityID: "user-secret-42", Lat: 31.2304, Lon: 121.4737, Timestamp: testBase}
	if err := v.Ingest(p); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if v.StoreLen() != 1 {
		t.Fatalf("StoreLen = %d, want 1", v.StoreLen())
	}
	rec := v.store.ByPseudonym(Pseudonym(testMasterKey, p.EntityID, v.EpochOf(testBase)), 0, 1<<62)[0]
	if bytes.Contains([]byte(rec.Pseudonym), []byte(p.EntityID)) {
		t.Fatal("pseudonym leaks entity id")
	}
	if bytes.Contains(rec.Ciphertext, []byte(p.EntityID)) {
		t.Fatal("ciphertext leaks entity id")
	}
	if bytes.Contains([]byte(rec.CellHash), []byte("31.23")) {
		t.Fatal("cell hash leaks coordinates")
	}
}

func TestHeatmapAggregation(t *testing.T) {
	v := newTestVault(t)
	// Two entities in the same cell, one elsewhere, one outside the window.
	inside := []Point{
		{EntityID: "a", Lat: 31.2301, Lon: 121.4701, Timestamp: testBase},
		{EntityID: "b", Lat: 31.2302, Lon: 121.4702, Timestamp: testBase.Add(time.Hour)},
		{EntityID: "a", Lat: 31.5000, Lon: 121.5000, Timestamp: testBase.Add(2 * time.Hour)},
		{EntityID: "a", Lat: 31.2301, Lon: 121.4701, Timestamp: testBase.Add(48 * time.Hour)},
	}
	for _, p := range inside {
		if err := v.Ingest(p); err != nil {
			t.Fatalf("Ingest: %v", err)
		}
	}
	heat := v.Heatmap(testBase, testBase.Add(3*time.Hour))
	total := 0
	for _, n := range heat {
		total += n
	}
	if total != 3 {
		t.Fatalf("heatmap total = %d, want 3", total)
	}
	if len(heat) != 2 {
		t.Fatalf("heatmap cells = %d, want 2", len(heat))
	}
	shared := CellHash(testSalt, CellID(31.2301, 121.4701, 0.01))
	if heat[shared] != 2 {
		t.Fatalf("shared cell count = %d, want 2 (entities aggregated anonymously)", heat[shared])
	}
}

func TestRevealValidCredential(t *testing.T) {
	v := newTestVault(t)
	target := "user-1002"
	var want []Point
	for h := 0; h < 6; h++ {
		p := Point{
			EntityID:  target,
			Lat:       31.23 + float64(h)*0.001,
			Lon:       121.47 + float64(h)*0.001,
			Timestamp: testBase.Add(time.Duration(h) * time.Hour),
		}
		want = append(want, p)
		if err := v.Ingest(p); err != nil {
			t.Fatalf("Ingest: %v", err)
		}
	}
	// Decoy entity in the same cells must not be revealed.
	if err := v.Ingest(Point{EntityID: "user-9999", Lat: 31.23, Lon: 121.47, Timestamp: testBase}); err != nil {
		t.Fatalf("Ingest decoy: %v", err)
	}

	// Authorize only hours 1..3 (3 of the 6 points).
	from := testBase.Add(time.Hour)
	to := testBase.Add(3 * time.Hour)
	cred := issueTestCredential(t, v, target, from, to)
	points, err := v.Reveal(cred, testBase.Add(48*time.Hour))
	if err != nil {
		t.Fatalf("Reveal: %v", err)
	}
	if len(points) != 3 {
		t.Fatalf("revealed %d points, want exactly 3 (authorized window only)", len(points))
	}
	for i, p := range points {
		w := want[i+1]
		if p.EntityID != w.EntityID || p.Lat != w.Lat || p.Lon != w.Lon || !p.Timestamp.Equal(w.Timestamp) {
			t.Fatalf("point %d = %+v, want %+v", i, p, w)
		}
	}
	// Audit log must record the reveal.
	entries := v.AuditLog().Entries()
	if len(entries) != 1 || entries[0].Action != "REVEAL" || entries[0].Records != 3 || entries[0].EntityID != target {
		t.Fatalf("unexpected audit entries: %+v", entries)
	}
	if !v.AuditLog().Verify() {
		t.Fatal("audit chain must verify")
	}
}

func TestRevealForgedSignature(t *testing.T) {
	v := newTestVault(t)
	cred := issueTestCredential(t, v, "user-1", testBase, testBase.Add(time.Hour))
	cred.EntityID = "user-2" // tampered after signing
	if _, err := v.Reveal(cred, testBase.Add(48*time.Hour)); err == nil {
		t.Fatal("forged credential must be rejected")
	}
	entries := v.AuditLog().Entries()
	if len(entries) != 1 || entries[0].Action != "REVEAL_DENIED" {
		t.Fatalf("denied attempt must be audited, got %+v", entries)
	}
}

func TestRevealExpiredCredential(t *testing.T) {
	v := newTestVault(t)
	now := testBase.Add(48 * time.Hour)
	cred, err := IssueCredential(testRegulatorKey, "user-1",
		v.EpochOf(testBase), v.EpochOf(testBase), "test-court",
		now.Add(-2*time.Hour), now.Add(-time.Hour)) // already expired
	if err != nil {
		t.Fatalf("IssueCredential: %v", err)
	}
	if _, err := v.Reveal(cred, now); err == nil {
		t.Fatal("expired credential must be rejected")
	}
}

func TestCredentialBoundToEntity(t *testing.T) {
	v := newTestVault(t)
	if err := v.Ingest(Point{EntityID: "user-A", Lat: 31.23, Lon: 121.47, Timestamp: testBase}); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	// Valid credential for user-B must not reveal user-A's records.
	cred := issueTestCredential(t, v, "user-B", testBase, testBase)
	points, err := v.Reveal(cred, testBase.Add(48*time.Hour))
	if err != nil {
		t.Fatalf("Reveal: %v", err)
	}
	if len(points) != 0 {
		t.Fatalf("credential for user-B revealed %d records of another entity", len(points))
	}
}

func TestAuditLogTamperEvidence(t *testing.T) {
	log := NewAuditLog()
	now := testBase
	for i := 0; i < 3; i++ {
		log.Append("REVEAL", "court", fmt.Sprintf("user-%d", i), 1, 2, 5, "nonce", now)
	}
	if !log.Verify() {
		t.Fatal("untampered log must verify")
	}
	log.entries[1].Records = 999 // tamper
	if log.Verify() {
		t.Fatal("tampered log must not verify")
	}
}

func TestConcurrentIngest(t *testing.T) {
	v := newTestVault(t)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				p := Point{
					EntityID:  fmt.Sprintf("user-%d", g),
					Lat:       31.0 + float64(i)*0.0001,
					Lon:       121.0,
					Timestamp: testBase.Add(time.Duration(i) * time.Minute),
				}
				if err := v.Ingest(p); err != nil {
					t.Errorf("Ingest: %v", err)
					return
				}
			}
		}(g)
	}
	wg.Wait()
	if v.StoreLen() != 800 {
		t.Fatalf("StoreLen = %d, want 800", v.StoreLen())
	}
}

func TestIngestRejectsBadInput(t *testing.T) {
	v := newTestVault(t)
	if err := v.Ingest(Point{EntityID: "", Lat: 31, Lon: 121, Timestamp: testBase}); err == nil {
		t.Fatal("empty entity id must be rejected")
	}
	if err := v.Ingest(Point{EntityID: "u", Lat: 91, Lon: 121, Timestamp: testBase}); err == nil {
		t.Fatal("out-of-range latitude must be rejected")
	}
}

func BenchmarkIngest(b *testing.B) {
	v, err := NewVault(Config{MasterKey: testMasterKey, RegulatorKey: testRegulatorKey, Salt: testSalt})
	if err != nil {
		b.Fatal(err)
	}
	p := Point{EntityID: "bench-user", Lat: 31.23, Lon: 121.47, Timestamp: testBase}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.Timestamp = p.Timestamp.Add(time.Minute)
		if err := v.Ingest(p); err != nil {
			b.Fatal(err)
		}
	}
}
