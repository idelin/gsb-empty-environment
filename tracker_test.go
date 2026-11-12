package netsec

import (
	"crypto/tls"
	"testing"
)

func TestSNICertTrackerResolve(t *testing.T) {
	tracker := NewSNICertTracker()

	aLeaf := mustLeaf(t, []string{"a.example"})
	bLeaf := mustLeaf(t, []string{"b.example"})
	aCert := &tls.Certificate{Leaf: aLeaf, Certificate: [][]byte{aLeaf.Raw}}
	bCert := &tls.Certificate{Certificate: [][]byte{bLeaf.Raw}} // force lazy parse

	if err := tracker.Register(aCert, "a.example", "A.EXAMPLE."); err != nil {
		t.Fatalf("register a: %v", err)
	}
	if err := tracker.Register(bCert, "b.example"); err != nil {
		t.Fatalf("register b: %v", err)
	}

	got, err := tracker.LeafForConnection(&tls.ConnectionState{ServerName: "a.example."})
	if err != nil || got != aLeaf {
		t.Fatalf("resolve a = %v, %v", got, err)
	}

	got, err = tracker.LeafForConnection(&tls.ConnectionState{ServerName: "B.Example"})
	if err != nil || got == nil || string(got.Raw) != string(bLeaf.Raw) {
		t.Fatalf("resolve b (lazy parse) = %v, %v", got, err)
	}

	if got, _ := tracker.LeafForConnection(&tls.ConnectionState{ServerName: "unknown.example"}); got != nil {
		t.Fatalf("unknown SNI should resolve to nil, got %v", got)
	}
	if got, _ := tracker.LeafForConnection(&tls.ConnectionState{}); got != nil {
		t.Fatalf("empty SNI should resolve to nil, got %v", got)
	}
}

func TestSNICertTrackerGetCertificate(t *testing.T) {
	tracker := NewSNICertTracker()
	cert := &tls.Certificate{
		Leaf:        mustLeaf(t, []string{"a.example"}),
		Certificate: [][]byte{mustLeaf(t, []string{"a.example"}).Raw},
	}
	if err := tracker.Register(cert, "a.example"); err != nil {
		t.Fatal(err)
	}

	got, err := tracker.GetCertificate(&tls.ClientHelloInfo{ServerName: "A.Example."})
	if err != nil || got != cert {
		t.Fatalf("GetCertificate = %v, %v", got, err)
	}

	if _, err := tracker.GetCertificate(&tls.ClientHelloInfo{ServerName: "other.example"}); err == nil {
		t.Fatal("expected error for unregistered SNI")
	}
}

func TestSNICertTrackerObserve(t *testing.T) {
	tracker := NewSNICertTracker()
	leaf := mustLeaf(t, []string{"dynamic.example"})
	cert := &tls.Certificate{Leaf: leaf, Certificate: [][]byte{leaf.Raw}}

	cb := tracker.Observe(func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
		return cert, nil
	})
	returned, err := cb(&tls.ClientHelloInfo{ServerName: "dynamic.example"})
	if err != nil || returned != cert {
		t.Fatalf("observe callback = %v, %v", returned, err)
	}

	got, err := tracker.LeafForConnection(&tls.ConnectionState{ServerName: "dynamic.example"})
	if err != nil || got != leaf {
		t.Fatalf("observed leaf = %v, %v", got, err)
	}
}
