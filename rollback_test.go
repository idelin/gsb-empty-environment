package rpki

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestOldManifestSequenceReplayAndHiddenRevocationRejected(t *testing.T) {
	h := newTestHarness(t)
	statePath := filepath.Join(t.TempDir(), "rpki-state.json")
	store := NewFileStateStore(statePath)
	ctx := context.Background()

	victim := h.createEE("future-revoked-ee", h.root, inheritClaims())
	h.files["root/victim.cer"] = victim.Cert.X509.Raw
	manifestEE := h.createEE("manifest-ee", h.root, inheritClaims())
	crlName := h.writeCRL(h.root)
	names := []string{"ta.cer", "victim.cer", crlName}

	h.writeManifest(h.root, manifestEE, 1, h.now.Add(-72*time.Hour), names)
	report, err := NewValidator(h.repository(), Options{Now: fixedNow(h.now), States: store}).ValidateTAL(ctx, h.tal)
	if err != nil {
		t.Fatalf("round one should pass: %v", err)
	}
	if !report.Valid {
		t.Fatalf("round one report invalid: %v", report.Err)
	}
	oldManifest := append([]byte(nil), h.files["root/root.mft"]...)
	oldCRL := append([]byte(nil), h.files["root/root.crl"]...)

	h.writeCRL(h.root, victim.Cert.X509)
	h.writeManifest(h.root, manifestEE, 2, h.now.Add(-time.Hour), names)
	_, err = NewValidator(h.repository(), Options{Now: fixedNow(h.now), States: store}).ValidateTAL(ctx, h.tal)
	if !errors.Is(err, ErrRevoked) {
		t.Fatalf("round two should surface victim revocation, got %v", err)
	}
	wm, ok, err := store.LoadWatermark(ctx, h.root.Cert.StateKey())
	if err != nil || !ok || wm.ManifestNumber.Int64() != 2 {
		t.Fatalf("expected persisted watermark 2, wm=%+v ok=%v err=%v", wm, ok, err)
	}

	// Malicious mirror serves the old, cryptographically valid manifest and
	// old clean CRL to hide the revocation.
	h.files["root/root.mft"] = oldManifest
	h.files["root/root.crl"] = oldCRL
	reopenedStore := NewFileStateStore(statePath)
	_, err = NewValidator(h.repository(), Options{Now: fixedNow(h.now), States: reopenedStore}).ValidateTAL(ctx, h.tal)
	if !errors.Is(err, ErrRollback) {
		t.Fatalf("expected ErrRollback, got %v", err)
	}
	wm, ok, err = reopenedStore.LoadWatermark(ctx, h.root.Cert.StateKey())
	if err != nil || !ok || wm.ManifestNumber.Int64() != 2 {
		t.Fatalf("rollback attempt must retain watermark 2, wm=%+v ok=%v err=%v", wm, ok, err)
	}
}

func TestExpiredManifestRejectedAndStateRetained(t *testing.T) {
	h := newTestHarness(t)
	store := NewMemoryStateStore()
	ctx := context.Background()
	ee := h.createEE("mft-ee", h.root, inheritClaims())
	crlName := h.writeCRL(h.root)
	names := []string{"ta.cer", crlName}
	h.writeManifest(h.root, ee, 9, h.now.Add(-48*time.Hour), names)
	if _, err := NewValidator(h.repository(), Options{Now: fixedNow(h.now), States: store}).ValidateTAL(ctx, h.tal); err != nil {
		t.Fatal(err)
	}

	h.writeManifestAt(h.root, ee, 10, h.now.Add(-2*time.Hour), h.now.Add(-time.Minute), names)
	_, err := NewValidator(h.repository(), Options{Now: fixedNow(h.now), States: store}).ValidateTAL(ctx, h.tal)
	if !errors.Is(err, ErrManifestExpired) {
		t.Fatalf("expected ErrManifestExpired, got %v", err)
	}
	wm, ok, _ := store.LoadWatermark(ctx, h.root.Cert.StateKey())
	if !ok || wm.ManifestNumber.Int64() != 9 {
		t.Fatalf("expired candidate must retain old watermark, got %+v ok=%v", wm, ok)
	}
}

func TestIdenticalManifestCanBeRevalidated(t *testing.T) {
	w := newHappyWorld(t)
	ctx := context.Background()
	v := w.validator()
	if _, err := v.ValidateTAL(ctx, w.h.tal); err != nil {
		t.Fatal(err)
	}
	if _, err := v.ValidateTAL(ctx, w.h.tal); err != nil {
		t.Fatalf("identical manifest should be idempotent: %v", err)
	}
}

func TestCheckWatermarkTimeRegression(t *testing.T) {
	old := Watermark{CAKey: "x", ManifestNumberString: "5", ThisUpdate: time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC)}
	candidate := Watermark{CAKey: "x", ManifestNumberString: "6", ThisUpdate: time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)}
	err := CheckWatermark(old, candidate)
	if !errors.Is(err, ErrRollback) {
		t.Fatalf("expected time regression rollback, got %v", err)
	}
}

func fixedNow(now time.Time) func() time.Time {
	return func() time.Time { return now }
}
