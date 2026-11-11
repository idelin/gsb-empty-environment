package knownhosts

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fixture struct {
	kh *KnownHosts

	hostEdPub  testPublicKey
	hostEdPriv ed25519.PrivateKey
	otherEdPub testPublicKey
	rsaPub     testPublicKey
	rsaPriv    *rsa.PrivateKey
	ecdsaPub   testPublicKey
	ecdsaPriv  *ecdsa.PrivateKey
	caEdPub    testPublicKey
	caEdPriv   ed25519.PrivateKey
	otherCAPub testPublicKey
	caRSAPub   testPublicKey
	caRSAPriv  *rsa.PrivateKey
}

func newFixture(t *testing.T) fixture {
	t.Helper()

	hostEdPubRaw, hostEdPriv := ed25519KeyPair()
	otherEdPubRaw, _ := ed25519KeyPair()
	caEdPubRaw, caEdPriv := ed25519KeyPair()
	otherCARaw, _ := ed25519KeyPair()

	rsaPriv := rsaKeyPair()
	ecdsaPriv := ecdsaKeyPair()
	caRSAPriv := rsaKeyPair()

	fx := fixture{
		hostEdPub:  testPublicKey{"ssh-ed25519", marshalEd25519(hostEdPubRaw)},
		hostEdPriv: hostEdPriv,
		otherEdPub: testPublicKey{"ssh-ed25519", marshalEd25519(otherEdPubRaw)},
		rsaPub:     testPublicKey{"ssh-rsa", marshalRSA(&rsaPriv.PublicKey)},
		rsaPriv:    rsaPriv,
		ecdsaPub:   testPublicKey{"ecdsa-sha2-nistp256", marshalECDSA(&ecdsaPriv.PublicKey)},
		ecdsaPriv:  ecdsaPriv,
		caEdPub:    testPublicKey{"ssh-ed25519", marshalEd25519(caEdPubRaw)},
		caEdPriv:   caEdPriv,
		otherCAPub: testPublicKey{"ssh-ed25519", marshalEd25519(otherCARaw)},
		caRSAPub:   testPublicKey{"ssh-rsa", marshalRSA(&caRSAPriv.PublicKey)},
		caRSAPriv:  caRSAPriv,
	}

	hashGit, err := HashHostname("git.example.com", 22)
	if err != nil {
		t.Fatalf("HashHostname: %v", err)
	}
	hashGitAlt, err := HashHostname("git.example.com", 2222)
	if err != nil {
		t.Fatalf("HashHostname: %v", err)
	}
	hashV6, err := HashHostname("2001:db8::1", 22)
	if err != nil {
		t.Fatalf("HashHostname: %v", err)
	}

	b64 := func(k testPublicKey) string {
		return base64.StdEncoding.EncodeToString(k.blob)
	}

	lines := []string{
		"# mixed-format known_hosts fixture",
		"",
		"plain.example.com " + b64(fx.hostEdPub) + " plain host",
		"[bracketed.example.com]:2200 " + b64(fx.hostEdPub) + " bracketed with port",
		"v4only.example.com:2222 " + b64(fx.hostEdPub) + " unbracketed host:port",
		"[2001:db8::2]:22 " + b64(fx.hostEdPub) + " ipv6 literal",
		"*.corp.example.com " + b64(fx.hostEdPub) + " wildcard suffix",
		"*.int.example.com,!admin.int.example.com " + b64(fx.hostEdPub) + " negated member",
		"[multi.example.com]:22,[multi.example.com]:2222 " + b64(fx.hostEdPub) + " multi pattern",
		"[wildport.example.com]:* " + b64(fx.hostEdPub) + " wildcard port",
		hashGit + " " + b64(fx.hostEdPub) + " hashed host:22",
		hashGitAlt + " " + b64(fx.rsaPub) + " hashed host:2222",
		hashV6 + " " + b64(fx.hostEdPub) + " hashed ipv6",
		"two.example.com " + b64(fx.hostEdPub) + " first key",
		"two.example.com " + b64(fx.otherEdPub) + " second key",
		"@cert-authority ca.example.com " + b64(fx.caEdPub) + " ed25519 ca",
		"@cert-authority rsa-ca.example.com " + b64(fx.caRSAPub) + " rsa ca",
		"@cert-authority ecdsa-ca.example.com " + b64(fx.ecdsaPub) + " ecdsa ca",
		"@revoked revoked.example.com " + b64(fx.hostEdPub) + " revoked same key",
		"@revoked revoked-alt.example.com " + b64(fx.rsaPub) + " revoked rsa",
		"@revoked evil.example.com * " + "wildcard revocation",
		"@revoked revoked-ca.example.com " + b64(fx.caEdPub) + " revoked ca",
		"revoked-and-good.example.com " + b64(fx.hostEdPub) + " would accept",
		"@revoked revoked-and-good.example.com " + b64(fx.hostEdPub) + " but revoked",
		"@revoked rsa-revoked.example.com " + b64(fx.rsaPub) + " rsa only",
	}
	lines = append(lines, "@custom-flag flagged.example.com "+b64(fx.hostEdPub))

	dir := t.TempDir()
	path := filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	kh, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	fx.kh = kh
	return fx
}

var (
	keyErr     = errors.New("key error")
	revokedErr = errors.New("revoked error")
)

func ed25519Raw(k testPublicKey) ed25519.PublicKey {
	return ed25519.PublicKey(k.blob[len(k.blob)-ed25519.PublicKeySize:])
}

type decision struct {
	name string
	host string
	port int
	key  func(fixture) testPublicKey
	want error
}

func (fx fixture) decisions() []decision {
	caRaw := ed25519Raw(fx.caEdPub)
	otherCARaw := ed25519Raw(fx.otherCAPub)

	certGood := func() testPublicKey {
		pub, _ := ed25519KeyPair()
		return buildEd25519Certificate(pub, fx.caEdPriv, caRaw, []string{"ca.example.com"}, false)
	}
	certWrongCA := func() testPublicKey {
		pub, _ := ed25519KeyPair()
		return buildEd25519Certificate(pub, fx.hostEdPriv, otherCARaw, []string{"ca.example.com"}, false)
	}
	certBadPrincipal := func() testPublicKey {
		pub, _ := ed25519KeyPair()
		return buildEd25519Certificate(pub, fx.caEdPriv, caRaw, []string{"other.example.com"}, false)
	}
	certTampered := func() testPublicKey {
		pub, _ := ed25519KeyPair()
		return buildEd25519Certificate(pub, fx.caEdPriv, caRaw, []string{"ca.example.com"}, true)
	}
	certAnyPrincipal := func() testPublicKey {
		pub, _ := ed25519KeyPair()
		return buildEd25519Certificate(pub, fx.caEdPriv, caRaw, nil, false)
	}
	rsaCertGood := func() testPublicKey {
		return buildRSACertificate(&fx.rsaPriv.PublicKey, fx.caRSAPriv, []string{"rsa-ca.example.com"})
	}
	ecdsaCertGood := func() testPublicKey {
		return buildECDSACertificate(&fx.ecdsaPriv.PublicKey, fx.ecdsaPriv, []string{"ecdsa-ca.example.com"})
	}

	return []decision{
		{"plain host accepts recorded key", "plain.example.com", 22, func(f fixture) testPublicKey { return f.hostEdPub }, nil},
		{"plain host accepts on another port", "plain.example.com", 9999, func(f fixture) testPublicKey { return f.hostEdPub }, nil},
		{"plain host rejects unknown key", "plain.example.com", 22, func(f fixture) testPublicKey { return f.otherEdPub }, keyErr},
		{"plain host rejects different key type", "plain.example.com", 22, func(f fixture) testPublicKey { return f.rsaPub }, keyErr},
		{"unknown host not accepted", "stranger.example.net", 22, func(f fixture) testPublicKey { return f.hostEdPub }, keyErr},

		{"bracketed host accepts matching port", "bracketed.example.com", 2200, func(f fixture) testPublicKey { return f.hostEdPub }, nil},
		{"bracketed host rejects wrong port", "bracketed.example.com", 22, func(f fixture) testPublicKey { return f.hostEdPub }, keyErr},
		{"host:port accepts matching port", "v4only.example.com", 2222, func(f fixture) testPublicKey { return f.hostEdPub }, nil},
		{"host:port rejects wrong port", "v4only.example.com", 22, func(f fixture) testPublicKey { return f.hostEdPub }, keyErr},
		{"ipv6 literal accepts", "2001:db8::2", 22, func(f fixture) testPublicKey { return f.hostEdPub }, nil},
		{"ipv6 literal rejects wrong key", "2001:db8::2", 22, func(f fixture) testPublicKey { return f.otherEdPub }, keyErr},

		{"glob suffix matches subdomain", "node1.corp.example.com", 22, func(f fixture) testPublicKey { return f.hostEdPub }, nil},
		{"glob suffix does not match bare domain", "corp.example.com", 22, func(f fixture) testPublicKey { return f.hostEdPub }, keyErr},
		{"glob with negation matches other subdomain", "node.int.example.com", 22, func(f fixture) testPublicKey { return f.hostEdPub }, nil},
		{"glob with negation excludes admin host", "admin.int.example.com", 22, func(f fixture) testPublicKey { return f.hostEdPub }, keyErr},

		{"multi pattern matches default port", "multi.example.com", 22, func(f fixture) testPublicKey { return f.hostEdPub }, nil},
		{"multi pattern matches alternate port", "multi.example.com", 2222, func(f fixture) testPublicKey { return f.hostEdPub }, nil},
		{"multi pattern rejects other port", "multi.example.com", 3333, func(f fixture) testPublicKey { return f.hostEdPub }, keyErr},
		{"wildcard port matches any port", "wildport.example.com", 65000, func(f fixture) testPublicKey { return f.hostEdPub }, nil},

		{"hashed host matches recorded host and port", "git.example.com", 22, func(f fixture) testPublicKey { return f.hostEdPub }, nil},
		{"hashed host rejects wrong port", "git.example.com", 2222, func(f fixture) testPublicKey { return f.hostEdPub }, keyErr},
		{"hashed host alternate port matches its own key", "git.example.com", 2222, func(f fixture) testPublicKey { return f.rsaPub }, nil},
		{"hashed host does not match other host", "git2.example.com", 22, func(f fixture) testPublicKey { return f.hostEdPub }, keyErr},
		{"hashed ipv6 matches", "2001:db8::1", 22, func(f fixture) testPublicKey { return f.hostEdPub }, nil},

		{"two keys first accepted", "two.example.com", 22, func(f fixture) testPublicKey { return f.hostEdPub }, nil},
		{"two keys second accepted", "two.example.com", 22, func(f fixture) testPublicKey { return f.otherEdPub }, nil},
		{"two keys unknown third rejected", "two.example.com", 22, func(f fixture) testPublicKey {
			pub, _ := ed25519KeyPair()
			return testPublicKey{"ssh-ed25519", marshalEd25519(pub)}
		}, keyErr},

		{"valid certificate accepted", "ca.example.com", 22, func(f fixture) testPublicKey { return certGood() }, nil},
		{"certificate signed by wrong CA rejected", "ca.example.com", 22, func(f fixture) testPublicKey { return certWrongCA() }, keyErr},
		{"certificate with bad principal rejected", "ca.example.com", 22, func(f fixture) testPublicKey { return certBadPrincipal() }, keyErr},
		{"tampered certificate rejected", "ca.example.com", 22, func(f fixture) testPublicKey { return certTampered() }, keyErr},
		{"certificate with empty principals accepted", "ca.example.com", 22, func(f fixture) testPublicKey { return certAnyPrincipal() }, nil},
		{"plain key rejected when only CA line exists", "ca.example.com", 22, func(f fixture) testPublicKey { return f.hostEdPub }, keyErr},
		{"RSA certificate accepted", "rsa-ca.example.com", 22, func(f fixture) testPublicKey { return rsaCertGood() }, nil},
		{"ECDSA certificate accepted", "ecdsa-ca.example.com", 22, func(f fixture) testPublicKey { return ecdsaCertGood() }, nil},

		{"revoked key rejected even when identical", "revoked.example.com", 22, func(f fixture) testPublicKey { return f.hostEdPub }, revokedErr},
		{"revoked line rejects any other key too", "revoked.example.com", 22, func(f fixture) testPublicKey { return f.otherEdPub }, keyErr},
		{"wildcard revocation rejects ed25519", "evil.example.com", 22, func(f fixture) testPublicKey { return f.hostEdPub }, revokedErr},
		{"wildcard revocation rejects rsa", "evil.example.com", 22, func(f fixture) testPublicKey { return f.rsaPub }, revokedErr},
		{"revocation wins over accepting line", "revoked-and-good.example.com", 22, func(f fixture) testPublicKey { return f.hostEdPub }, revokedErr},
		{"revocation of different key type yields conflict", "rsa-revoked.example.com", 22, func(f fixture) testPublicKey { return f.hostEdPub }, keyErr},
		{"certificate by revoked CA is rejected", "revoked-ca.example.com", 22, func(f fixture) testPublicKey {
			pub, _ := ed25519KeyPair()
			return buildEd25519Certificate(pub, fx.caEdPriv, caRaw, []string{"revoked-ca.example.com"}, false)
		}, revokedErr},

		{"unknown marker line is tolerated", "flagged.example.com", 22, func(f fixture) testPublicKey { return f.hostEdPub }, nil},
		{"case insensitive host matching", "PLAIN.Example.COM", 22, func(f fixture) testPublicKey { return f.hostEdPub }, nil},
	}
}

func TestHostKeyDecisions(t *testing.T) {
	fx := newFixture(t)

	for _, tc := range fx.decisions() {
		t.Run(tc.name, func(t *testing.T) {
			err := fx.kh.HostKeyCallback(tc.host, tc.port, tc.key(fx))
			switch tc.want {
			case nil:
				if err != nil {
					t.Fatalf("expected acceptance, got %v", err)
				}
			case keyErr:
				var keyError *KeyError
				if !errors.As(err, &keyError) {
					t.Fatalf("expected *KeyError, got %T: %v", err, err)
				}
				if len(keyError.Want) > 0 && keyError.Want[0].Line == 0 {
					t.Fatalf("KeyError.Want entries must carry line numbers: %+v", keyError.Want)
				}
			case revokedErr:
				var revoked *RevokedError
				if !errors.As(err, &revoked) {
					t.Fatalf("expected *RevokedError, got %T: %v", err, err)
				}
				if revoked.Line == 0 {
					t.Fatalf("RevokedError must carry the line number")
				}
			}
		})
	}
}
