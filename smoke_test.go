package rpki

import (
	"context"
	"testing"
	"time"
)

func TestSmokeRootManifestCMS(t *testing.T) {
	h := newTestHarness(t)
	ee := h.createEE("root-manifest-ee", h.root, inheritClaims())
	crlName := h.writeCRL(h.root)
	h.writeManifest(h.root, ee, 1, h.now.Add(-time.Hour), []string{"ta.cer", crlName})

	v := NewValidator(h.repository(), Options{Now: func() time.Time { return h.now }})
	report, err := v.ValidateTAL(context.Background(), h.tal)
	if err != nil {
		t.Fatalf("ValidateTAL: %v", err)
	}
	if !report.Valid {
		t.Fatalf("root report invalid: %v", report.Err)
	}
	if report.Manifest == nil || report.Manifest.ManifestNumber.Int64() != 1 {
		t.Fatalf("unexpected manifest: %#v", report.Manifest)
	}
}
