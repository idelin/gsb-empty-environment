package rpki

import (
	"testing"
	"time"
)

type happyWorld struct {
	h     *testHarness
	child *testCA
	grand *testCA
	store *MemoryStateStore
}

func newHappyWorld(t *testing.T) *happyWorld {
	t.Helper()
	h := newTestHarness(t)
	childClaims := mustClaims(t,
		[]string{"10.1.0.0/16", "192.168.1.0/24"},
		[]string{"2001:db8:1::/48"},
		[]string{"64501"},
	)
	child := h.createCA("child", "child", h.root, childClaims)
	h.files["root/child.cer"] = child.Cert.X509.Raw

	grand := h.createCA("grand", "grand", child, inheritClaims())
	h.files["child/grand.cer"] = grand.Cert.X509.Raw

	rootMFTEE := h.createEE("root-mft-ee", h.root, inheritClaims())
	rootCRL := h.writeCRL(h.root)
	rootROAEE := h.createEE("root-roa-ee", h.root, mustClaims(t, []string{"10.1.0.0/16"}, nil, nil))
	h.writeROA(h.root, rootROAEE, "root.roa", 64501, []string{"10.1.0.0/16"}, 24)
	h.writeManifest(h.root, rootMFTEE, 1, h.now.Add(-2*time.Hour), []string{"ta.cer", "child.cer", rootCRL, "root.roa"})

	childMFTEE := h.createEE("child-mft-ee", child, inheritClaims())
	childCRL := h.writeCRL(child)
	childASPAEE := h.createEE("child-aspa-ee", child, mustClaims(t, nil, nil, []string{"64501"}))
	h.writeASPA(child, childASPAEE, "child.aspa", 64501, 64502, 64503)
	h.writeManifest(child, childMFTEE, 1, h.now.Add(-90*time.Minute), []string{"grand.cer", childCRL, "child.aspa"})

	grandMFTEE := h.createEE("grand-mft-ee", grand, inheritClaims())
	grandCRL := h.writeCRL(grand)
	h.writeManifest(grand, grandMFTEE, 1, h.now.Add(-30*time.Minute), []string{grandCRL})

	return &happyWorld{h: h, child: child, grand: grand, store: NewMemoryStateStore()}
}

func (w *happyWorld) validator() *Validator {
	return NewValidator(w.h.repository(), Options{
		Now:      func() time.Time { return w.h.now },
		States:   w.store,
		MaxDepth: 8,
	})
}
