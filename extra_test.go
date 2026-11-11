package knownhosts

import (
	"errors"
	"strings"
	"testing"
)

func TestNegationSemantics(t *testing.T) {
	pub, _ := ed25519KeyPair()
	key := testPublicKey{"ssh-ed25519", marshalEd25519(pub)}

	// A negation-only line never matches anything on its own.
	kh, err := NewFromReader(strings.NewReader("!blocked.example.com " + encodeB64(key)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := kh.HostKeyCallback("blocked.example.com", 22, key); err == nil {
		t.Fatal("negation-only line must not accept the host")
	}

	// A negated member of a wildcard line excludes just that member, while a
	// separate plain line for the same host still matches independently.
	content := strings.Join([]string{
		"*.pool.example.com,!special.pool.example.com " + encodeB64(key),
		"special.pool.example.com " + encodeB64(key),
	}, "\n")
	kh, err = NewFromReader(strings.NewReader(content))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := kh.HostKeyCallback("node.pool.example.com", 22, key); err != nil {
		t.Fatalf("wildcard member should be accepted: %v", err)
	}
	if err := kh.HostKeyCallback("special.pool.example.com", 22, key); err != nil {
		t.Fatalf("separate plain line should still accept: %v", err)
	}
}

func TestCertificateEdgeCases(t *testing.T) {
	caPubRaw, caPriv := ed25519KeyPair()
	caKey := testPublicKey{"ssh-ed25519", marshalEd25519(caPubRaw)}
	hostPub, _ := ed25519KeyPair()

	t.Run("no cert-authority entry", func(t *testing.T) {
		kh, err := NewFromReader(strings.NewReader(encodeLine("no-ca.example.com", caKey)))
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		cert := buildEd25519Certificate(hostPub, caPriv, caPubRaw, []string{"no-ca.example.com"}, false)
		err = kh.HostKeyCallback("no-ca.example.com", 22, cert)
		var keyError *KeyError
		if !errors.As(err, &keyError) {
			t.Fatalf("expected KeyError, got %v", err)
		}
	})

	t.Run("expired certificate rejected", func(t *testing.T) {
		kh, err := NewFromReader(strings.NewReader("@cert-authority expired.example.com " + encodeB64(caKey)))
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		cert := buildEd25519CertificateWindow(hostPub, caPriv, caPubRaw,
			[]string{"expired.example.com"}, false, 100, 200)
		err = kh.HostKeyCallback("expired.example.com", 22, cert)
		var keyError *KeyError
		if !errors.As(err, &keyError) {
			t.Fatalf("expected KeyError for expired cert, got %v", err)
		}
		if !strings.Contains(err.Error(), "validity") {
			t.Fatalf("expected validity error, got %v", err)
		}
	})

	t.Run("revoked wildcard CA rejects any certificate", func(t *testing.T) {
		kh, err := NewFromReader(strings.NewReader("@revoked wild-revoked.example.com *"))
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		cert := buildEd25519Certificate(hostPub, caPriv, caPubRaw, []string{"wild-revoked.example.com"}, false)
		err = kh.HostKeyCallback("wild-revoked.example.com", 22, cert)
		var revoked *RevokedError
		if !errors.As(err, &revoked) {
			t.Fatalf("expected RevokedError, got %v", err)
		}
	})
}

func TestRevokedLinePriorityOverMultipleKeys(t *testing.T) {
	pub, _ := ed25519KeyPair()
	recorded := testPublicKey{"ssh-ed25519", marshalEd25519(pub)}
	other, _ := ed25519KeyPair()
	unknown := testPublicKey{"ssh-ed25519", marshalEd25519(other)}

	content := strings.Join([]string{
		"priority.example.com " + encodeB64(recorded),
		"@revoked priority.example.com " + encodeB64(recorded),
	}, "\n")
	kh, err := NewFromReader(strings.NewReader(content))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if err := kh.HostKeyCallback("priority.example.com", 22, recorded); err == nil {
		t.Fatal("recorded key must still be rejected because it is revoked")
	} else {
		var revoked *RevokedError
		if !errors.As(err, &revoked) {
			t.Fatalf("expected RevokedError, got %T: %v", err, err)
		}
	}

	err = kh.HostKeyCallback("priority.example.com", 22, unknown)
	var keyError *KeyError
	if !errors.As(err, &keyError) {
		t.Fatalf("non-revoked unknown key should yield KeyError, got %v", err)
	}
}
