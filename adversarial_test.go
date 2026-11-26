package rpki

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestOutOfResourceChildSubtreeIsRejected(t *testing.T) {
	h := newTestHarness(t)
	evil := h.createCA("evil-child", "evil", h.root, mustClaims(t, []string{"11.0.0.0/16"}, nil, nil))
	h.files["root/evil.cer"] = evil.Cert.X509.Raw

	leaf := h.createCA("evil-leaf", "evil-leaf", evil, inheritClaims())
	h.files["evil/leaf.cer"] = leaf.Cert.X509.Raw
	evilEE := h.createEE("evil-mft-ee", evil, inheritClaims())
	evilCRL := h.writeCRL(evil)
	h.writeManifest(evil, evilEE, 1, h.now.Add(-time.Hour), []string{"leaf.cer", evilCRL})

	rootEE := h.createEE("root-mft-ee", h.root, inheritClaims())
	rootCRL := h.writeCRL(h.root)
	h.writeManifest(h.root, rootEE, 1, h.now.Add(-time.Hour), []string{"ta.cer", "evil.cer", rootCRL})

	store := NewMemoryStateStore()
	v := NewValidator(h.repository(), Options{Now: func() time.Time { return h.now }, States: store})
	report, err := v.ValidateTAL(context.Background(), h.tal)
	if err == nil {
		t.Fatal("expected out-of-resource child to fail")
	}
	if !errors.Is(err, ErrOutOfScope) {
		t.Fatalf("expected ErrOutOfScope, got %v", err)
	}
	evilReport := findChildByName(report, "evil-child")
	if evilReport == nil {
		t.Fatalf("missing invalid evil child report: %#v", report)
	}
	if evilReport.Valid {
		t.Fatal("evil child report must be invalid")
	}
	if len(evilReport.Children) != 0 {
		t.Fatalf("out-of-scope subtree must not be traversed, got %d children", len(evilReport.Children))
	}
	if _, ok, _ := store.LoadWatermark(context.Background(), evil.Cert.StateKey()); ok {
		t.Fatal("rejected evil CA must not have a watermark")
	}
	if _, ok, _ := store.LoadWatermark(context.Background(), leaf.Cert.StateKey()); ok {
		t.Fatal("untraversed descendant must not have a watermark")
	}
}

func TestOutOfResourceEEIsRejected(t *testing.T) {
	cases := []struct {
		name   string
		claims ResourceClaims
	}{
		{name: "IPv4", claims: mustClaims(t, []string{"11.0.0.0/16"}, nil, nil)},
		{name: "IPv6", claims: mustClaims(t, nil, []string{"2001:db9::/32"}, nil)},
		{name: "ASN", claims: mustClaims(t, nil, nil, []string{"64800"})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestHarness(t)
			badEE := h.createEE("bad-mft-ee", h.root, tc.claims)
			crlName := h.writeCRL(h.root)
			h.writeManifest(h.root, badEE, 1, h.now.Add(-time.Hour), []string{"ta.cer", crlName})

			v := NewValidator(h.repository(), Options{Now: func() time.Time { return h.now }})
			_, err := v.ValidateTAL(context.Background(), h.tal)
			if !errors.Is(err, ErrOutOfScope) {
				t.Fatalf("expected EE ErrOutOfScope, got %v", err)
			}
		})
	}
}

func TestOutOfResourceChildDimensions(t *testing.T) {
	cases := []struct {
		name   string
		claims ResourceClaims
	}{
		{name: "IPv4", claims: mustClaims(t, []string{"11.0.0.0/16"}, nil, nil)},
		{name: "IPv6", claims: mustClaims(t, nil, []string{"2001:db9::/32"}, nil)},
		{name: "ASN", claims: mustClaims(t, nil, nil, []string{"64800"})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestHarness(t)
			child := h.createCA("bad-child-"+tc.name, "bad-child", h.root, tc.claims)
			h.files["root/bad.cer"] = child.Cert.X509.Raw
			ee := h.createEE("root-mft-ee", h.root, inheritClaims())
			crlName := h.writeCRL(h.root)
			h.writeManifest(h.root, ee, 1, h.now.Add(-time.Hour), []string{"ta.cer", "bad.cer", crlName})

			_, err := NewValidator(h.repository(), Options{Now: fixedNow(h.now)}).ValidateTAL(context.Background(), h.tal)
			if !errors.Is(err, ErrOutOfScope) {
				t.Fatalf("expected child %s ErrOutOfScope, got %v", tc.name, err)
			}
		})
	}
}

func TestMissingManifestFileFailsClosed(t *testing.T) {
	w := newHappyWorld(t)
	delete(w.h.files, "root/root.roa")

	_, err := w.validator().ValidateTAL(context.Background(), w.h.tal)
	if !errors.Is(err, ErrManifestMissing) {
		t.Fatalf("expected ErrManifestMissing, got %v", err)
	}
	if _, ok, _ := w.store.LoadWatermark(context.Background(), w.h.root.Cert.StateKey()); ok {
		t.Fatal("failed publication point must not persist a new watermark")
	}
}

func TestManifestHashMismatchFailsClosed(t *testing.T) {
	w := newHappyWorld(t)
	original := w.h.files["root/root.roa"]
	w.h.files["root/root.roa"] = append(append([]byte(nil), original...), 0x00)

	_, err := w.validator().ValidateTAL(context.Background(), w.h.tal)
	if !errors.Is(err, ErrManifestHash) {
		t.Fatalf("expected ErrManifestHash, got %v", err)
	}
}

func TestManifestItselfMissingRejected(t *testing.T) {
	w := newHappyWorld(t)
	delete(w.h.files, "root/root.mft")

	_, err := w.validator().ValidateTAL(context.Background(), w.h.tal)
	if !errors.Is(err, ErrPublication) {
		t.Fatalf("expected ErrPublication for missing manifest, got %v", err)
	}
}

func TestUnlistedFileRejectedInStrictMode(t *testing.T) {
	w := newHappyWorld(t)
	w.h.files["root/unlisted.bin"] = []byte("mirror leftovers")

	_, err := w.validator().ValidateTAL(context.Background(), w.h.tal)
	if !errors.Is(err, ErrManifestExtra) {
		t.Fatalf("expected ErrManifestExtra, got %v", err)
	}
}

func TestSIACycleRejected(t *testing.T) {
	h := newTestHarness(t)
	cycle := h.createCA("cycle-child", "root", h.root, inheritClaims())
	h.files["root/cycle.cer"] = cycle.Cert.X509.Raw
	ee := h.createEE("mft-ee", h.root, inheritClaims())
	crlName := h.writeCRL(h.root)
	h.writeManifest(h.root, ee, 1, h.now.Add(-time.Hour), []string{"ta.cer", "cycle.cer", crlName})

	v := NewValidator(h.repository(), Options{Now: func() time.Time { return h.now }})
	_, err := v.ValidateTAL(context.Background(), h.tal)
	if !errors.Is(err, ErrSIACycle) {
		t.Fatalf("expected ErrSIACycle, got %v", err)
	}
}

func TestDelegationDepthLimitRejected(t *testing.T) {
	w := newHappyWorld(t)
	opts := Options{Now: func() time.Time { return w.h.now }, States: w.store, MaxDepth: 1}
	v := NewValidator(w.h.repository(), opts)
	_, err := v.ValidateTAL(context.Background(), w.h.tal)
	if !errors.Is(err, ErrDepth) {
		t.Fatalf("expected ErrDepth, got %v", err)
	}
}

func findChildByName(report *CAReport, name string) *CAReport {
	if report == nil {
		return nil
	}
	for _, child := range report.Children {
		if child.Name == name {
			return child
		}
	}
	return nil
}
