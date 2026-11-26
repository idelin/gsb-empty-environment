package rpki

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestAllRevokedEETypesRejected(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, h *testHarness)
	}{
		{
			name: "manifest EE",
			setup: func(t *testing.T, h *testHarness) {
				ee := h.createEE("revoked-manifest-ee", h.root, inheritClaims())
				crlName := h.writeCRL(h.root, ee.Cert.X509)
				h.writeManifest(h.root, ee, 1, h.now.Add(-time.Hour), []string{"ta.cer", crlName})
			},
		},
		{
			name: "standalone EE certificate",
			setup: func(t *testing.T, h *testHarness) {
				victim := h.createEE("revoked-standalone-ee", h.root, inheritClaims())
				h.files["root/victim.cer"] = victim.Cert.X509.Raw
				manifestEE := h.createEE("manifest-ee", h.root, inheritClaims())
				crlName := h.writeCRL(h.root, victim.Cert.X509)
				h.writeManifest(h.root, manifestEE, 1, h.now.Add(-time.Hour), []string{"ta.cer", "victim.cer", crlName})
			},
		},
		{
			name: "ROA EE",
			setup: func(t *testing.T, h *testHarness) {
				roaEE := h.createEE("revoked-roa-ee", h.root, mustClaims(t, []string{"10.1.0.0/16"}, nil, nil))
				crlName := h.writeCRL(h.root, roaEE.Cert.X509)
				h.writeROA(h.root, roaEE, "root.roa", 64501, []string{"10.1.0.0/16"}, 24)
				manifestEE := h.createEE("manifest-ee", h.root, inheritClaims())
				h.writeManifest(h.root, manifestEE, 1, h.now.Add(-time.Hour), []string{"ta.cer", crlName, "root.roa"})
			},
		},
		{
			name: "ASPA EE",
			setup: func(t *testing.T, h *testHarness) {
				aspaEE := h.createEE("revoked-aspa-ee", h.root, mustClaims(t, nil, nil, []string{"64501"}))
				crlName := h.writeCRL(h.root, aspaEE.Cert.X509)
				h.writeASPA(h.root, aspaEE, "root.aspa", 64501, 64502)
				manifestEE := h.createEE("manifest-ee", h.root, inheritClaims())
				h.writeManifest(h.root, manifestEE, 1, h.now.Add(-time.Hour), []string{"ta.cer", crlName, "root.aspa"})
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestHarness(t)
			tc.setup(t, h)
			v := NewValidator(h.repository(), Options{Now: func() time.Time { return h.now }})
			report, err := v.ValidateTAL(context.Background(), h.tal)
			if !errors.Is(err, ErrRevoked) {
				t.Fatalf("expected ErrRevoked, got %v", err)
			}
			if report != nil && report.Valid {
				t.Fatal("report must not be valid after revoked EE")
			}
		})
	}
}

func TestCRLWrongIssuerRejected(t *testing.T) {
	h := newTestHarness(t)
	other := h.createCA("other", "other", nil, mustClaims(t, []string{"172.16.0.0/16"}, nil, []string{"65000"}))
	ee := h.createEE("mft-ee", h.root, inheritClaims())
	crlName := h.writeCRL(other)
	// Move the other CA's CRL into root's publication point.
	h.files["root/"+crlName] = h.files["other/"+crlName]
	delete(h.files, "other/"+crlName)
	h.writeManifest(h.root, ee, 1, h.now.Add(-time.Hour), []string{"ta.cer", crlName})

	v := NewValidator(h.repository(), Options{Now: func() time.Time { return h.now }})
	_, err := v.ValidateTAL(context.Background(), h.tal)
	if !errors.Is(err, ErrCRLSignature) {
		t.Fatalf("expected CRL signature failure, got %v", err)
	}
}
