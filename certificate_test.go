package netsec

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type signerFunc func([]byte) (algorithm string, signature []byte)

type certFixture struct {
	blob            []byte
	signedBytes     []byte
	caBlob          []byte
	caLine          string
	certLine        string
	forceOffset     int
	principalOffset int
	extensionOffset int
	validAfterOff   int
}

func buildCertificateFixture(
	t *testing.T,
	certType string,
	userKeyBlob []byte,
	caKeyBlob []byte,
	caLine string,
	sign signerFunc,
) *certFixture {
	t.Helper()

	const (
		command     = "echo certificate-command"
		principal   = "alice@example"
		keyID       = "certificate-key-id"
		extension   = "permit-pty"
		validAfter  = uint64(0x1122334455667788)
		validBefore = uint64(0x8877665544332211)
	)

	var blob []byte
	blob = appendString(blob, []byte(certType))
	blob = appendString(blob, bytes.Repeat([]byte{0x5a}, 32))
	userKey, err := parsePublicKeyBlob(userKeyBlob)
	if err != nil {
		t.Fatal(err)
	}
	keyFields, err := marshalCertificateKeyFields(userKey)
	if err != nil {
		t.Fatal(err)
	}
	blob = append(blob, keyFields...)
	blob = appendUint64(blob, 0x0102030405060708)
	blob = appendUint32(blob, UserCert)
	blob = appendString(blob, []byte(keyID))
	blob = appendString(blob, []byte(principal))
	blob = appendUint64(blob, validAfter)
	blob = appendUint64(blob, validBefore)

	critical := appendString(nil, []byte("force-command"))
	critical = appendString(critical, []byte(command))
	blob = appendString(blob, critical)

	extensions := appendString(nil, []byte(extension))
	extensions = appendString(extensions, nil)
	blob = appendString(blob, extensions)

	blob = appendString(blob, nil)
	blob = appendString(blob, caKeyBlob)

	signed := append([]byte(nil), blob...)
	algorithm, signature := sign(signed)
	signatureBlob := appendString(nil, []byte(algorithm))
	signatureBlob = appendString(signatureBlob, signature)
	blob = appendString(blob, signatureBlob)

	caBase64 := base64.StdEncoding.EncodeToString(caKeyBlob)
	certBase64 := base64.StdEncoding.EncodeToString(blob)

	return &certFixture{
		blob:            blob,
		signedBytes:     signed,
		caBlob:          caKeyBlob,
		caLine:          "ssh-ed25519 " + caBase64 + " certificate-authority",
		certLine:        certType + " " + certBase64 + " test-certificate",
		forceOffset:     bytes.Index(blob[:len(signed)], []byte(command)),
		principalOffset: bytes.Index(blob[:len(signed)], []byte(principal)),
		extensionOffset: bytes.Index(blob[:len(signed)], []byte(extension)),
		validAfterOff:   len(blob[:len(signed)]) - (len(caKeyBlob) + 4) - 8 - 8 - 4 - 4 - 8,
	}
}

func ed25519CA(t *testing.T) (ed25519.PrivateKey, []byte, []byte) {
	t.Helper()
	caPub, caPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	userPub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caKey, err := NewPublicKey(caPub)
	if err != nil {
		t.Fatal(err)
	}
	userKey, err := NewPublicKey(userPub)
	if err != nil {
		t.Fatal(err)
	}
	return caPriv, caKey.Marshal(), userKey.Marshal()
}

func TestOriginalCertificateSignaturePasses(t *testing.T) {
	caPriv, caBlob, userBlob := ed25519CA(t)
	fixture := buildCertificateFixture(
		t,
		"ssh-ed25519-cert-v01@openssh.com",
		userBlob,
		caBlob,
		"ssh-ed25519 "+base64.StdEncoding.EncodeToString(caBlob),
		func(data []byte) (string, []byte) {
			return "ssh-ed25519", ed25519.Sign(caPriv, data)
		},
	)

	if err := VerifyCertificate(fixture.blob, fixture.caBlob); err != nil {
		t.Fatalf("original binary certificate did not verify: %v", err)
	}
	if err := VerifyCertificate([]byte(fixture.certLine), []byte(fixture.caLine)); err != nil {
		t.Fatalf("original authorized_keys certificate did not verify: %v", err)
	}
	if err := VerifyCertificate(
		[]byte(base64.StdEncoding.EncodeToString(fixture.blob)),
		[]byte(base64.StdEncoding.EncodeToString(fixture.caBlob)),
	); err != nil {
		t.Fatalf("base64-only certificate did not verify: %v", err)
	}

	parsed, err := ParseCertificate([]byte(fixture.certLine))
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.ValidPrincipals) != 1 || parsed.ValidPrincipals[0] != "alice@example" {
		t.Fatalf("valid principals were not parsed: %#v", parsed.ValidPrincipals)
	}
	if len(parsed.CriticalOptions) != 1 ||
		parsed.CriticalOptions[0].Name != "force-command" ||
		string(parsed.CriticalOptions[0].Data) != "echo certificate-command" {
		t.Fatalf("critical options were not parsed: %#v", parsed.CriticalOptions)
	}
	if len(parsed.Extensions) != 1 || parsed.Extensions[0].Name != "permit-pty" {
		t.Fatalf("extensions were not parsed: %#v", parsed.Extensions)
	}
	if !bytes.Equal(parsed.SignedBytes(), fixture.signedBytes) {
		t.Fatal("signed bytes do not match the certificate prefix")
	}
	if !bytes.Equal(parsed.Marshal(), fixture.blob) {
		t.Fatal("unmodified Marshal does not reproduce the original blob")
	}
}

func TestForceCommandSingleByteChangeFails(t *testing.T) {
	caPriv, caBlob, userBlob := ed25519CA(t)
	fixture := buildCertificateFixture(
		t,
		"ssh-ed25519-cert-v01@openssh.com",
		userBlob,
		caBlob,
		"ssh-ed25519 "+base64.StdEncoding.EncodeToString(caBlob),
		func(data []byte) (string, []byte) {
			return "ssh-ed25519", ed25519.Sign(caPriv, data)
		},
	)
	if fixture.forceOffset < 0 {
		t.Fatal("force-command was not found in the fixture")
	}

	mutated := append([]byte(nil), fixture.blob...)
	mutated[fixture.forceOffset] ^= 0x01

	_, err := ParseCertificate(mutated)
	if err != nil {
		t.Fatalf("single-byte force-command change should still parse: %v", err)
	}
	err = VerifyCertificate(mutated, fixture.caBlob)
	if !errors.Is(err, ErrSignatureMismatch) {
		t.Fatalf("force-command change should fail the original signature, got %v", err)
	}
}

func TestEverySignedByteIsCovered(t *testing.T) {
	caPriv, caBlob, userBlob := ed25519CA(t)
	fixture := buildCertificateFixture(
		t,
		"ssh-ed25519-cert-v01@openssh.com",
		userBlob,
		caBlob,
		"ssh-ed25519 "+base64.StdEncoding.EncodeToString(caBlob),
		func(data []byte) (string, []byte) {
			return "ssh-ed25519", ed25519.Sign(caPriv, data)
		},
	)

	for offset := range fixture.signedBytes {
		mutated := append([]byte(nil), fixture.blob...)
		mutated[offset] ^= 0x01
		if err := VerifyCertificate(mutated, fixture.caBlob); err == nil {
			t.Fatalf("changing signed byte at offset %d still verified", offset)
		}
	}
}

func TestParsedFieldChangesFail(t *testing.T) {
	caPriv, caBlob, userBlob := ed25519CA(t)
	fixture := buildCertificateFixture(
		t,
		"ssh-ed25519-cert-v01@openssh.com",
		userBlob,
		caBlob,
		"ssh-ed25519 "+base64.StdEncoding.EncodeToString(caBlob),
		func(data []byte) (string, []byte) {
			return "ssh-ed25519", ed25519.Sign(caPriv, data)
		},
	)

	mutations := map[string]func(*Certificate){
		"critical option": func(c *Certificate) { c.CriticalOptions[0].Data[0] ^= 0x01 },
		"extension":       func(c *Certificate) { c.Extensions[0].Data = []byte("changed") },
		"principal":       func(c *Certificate) { c.ValidPrincipals[0] = "mallory" },
		"valid-after":     func(c *Certificate) { c.ValidAfter++ },
		"valid-before":    func(c *Certificate) { c.ValidBefore-- },
	}

	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			parsed, err := ParseCertificate(fixture.blob)
			if err != nil {
				t.Fatal(err)
			}
			mutate(parsed)
			err = parsed.VerifySignature(fixture.caBlob)
			if !errors.Is(err, ErrCertificateFieldsModified) {
				t.Fatalf("parsed %s change should be rejected, got %v", name, err)
			}
			if err := VerifyCertificate(parsed.Marshal(), fixture.caBlob); err == nil {
				t.Fatalf("re-marshaled %s change should not verify", name)
			}
		})
	}
}

func TestMalformedCertificateInputsFail(t *testing.T) {
	caPriv, caBlob, userBlob := ed25519CA(t)
	fixture := buildCertificateFixture(
		t,
		"ssh-ed25519-cert-v01@openssh.com",
		userBlob,
		caBlob,
		"ssh-ed25519 "+base64.StdEncoding.EncodeToString(caBlob),
		func(data []byte) (string, []byte) {
			return "ssh-ed25519", ed25519.Sign(caPriv, data)
		},
	)

	trailing := append(append([]byte(nil), fixture.blob...), 0x00)
	if err := VerifyCertificate(trailing, fixture.caBlob); err == nil {
		t.Fatal("trailing byte after signature was accepted")
	}

	truncated := fixture.blob[:len(fixture.blob)-8]
	if err := VerifyCertificate(truncated, fixture.caBlob); err == nil {
		t.Fatal("truncated signature was accepted")
	}
}

func TestRSACertificate(t *testing.T) {
	caPriv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	userPriv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	caKey, err := NewPublicKey(caPriv.Public())
	if err != nil {
		t.Fatal(err)
	}
	userKey, err := NewPublicKey(userPriv.Public())
	if err != nil {
		t.Fatal(err)
	}
	caBlob := caKey.Marshal()
	fixture := buildCertificateFixture(
		t,
		"ssh-rsa-cert-v01@openssh.com",
		userKey.Marshal(),
		caBlob,
		"ssh-rsa "+base64.StdEncoding.EncodeToString(caBlob),
		func(data []byte) (string, []byte) {
			digest := sha256.Sum256(data)
			sig, err := rsa.SignPKCS1v15(rand.Reader, caPriv, crypto.SHA256, digest[:])
			if err != nil {
				t.Fatal(err)
			}
			return "rsa-sha2-256", sig
		},
	)

	if err := VerifyCertificate(fixture.blob, fixture.caBlob); err != nil {
		t.Fatalf("RSA certificate did not verify: %v", err)
	}
	mutated := append([]byte(nil), fixture.blob...)
	mutated[fixture.forceOffset] ^= 0x01
	if err := VerifyCertificate(mutated, fixture.caBlob); !errors.Is(err, ErrSignatureMismatch) {
		t.Fatalf("RSA force-command change should fail, got %v", err)
	}
}

func TestECDSACertificate(t *testing.T) {
	cases := []struct {
		name    string
		curve   elliptic.Curve
		keyType string
		hash    func([]byte) []byte
	}{
		{
			name:    "nistp256",
			curve:   elliptic.P256(),
			keyType: "ecdsa-sha2-nistp256",
			hash: func(data []byte) []byte {
				sum := sha256.Sum256(data)
				return sum[:]
			},
		},
		{
			name:    "nistp384",
			curve:   elliptic.P384(),
			keyType: "ecdsa-sha2-nistp384",
			hash: func(data []byte) []byte {
				sum := sha512.Sum384(data)
				return sum[:]
			},
		},
		{
			name:    "nistp521",
			curve:   elliptic.P521(),
			keyType: "ecdsa-sha2-nistp521",
			hash: func(data []byte) []byte {
				sum := sha512.Sum512(data)
				return sum[:]
			},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			caPriv, err := ecdsa.GenerateKey(testCase.curve, rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			userPriv, err := ecdsa.GenerateKey(testCase.curve, rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			caKey, err := NewPublicKey(caPriv.Public())
			if err != nil {
				t.Fatal(err)
			}
			userKey, err := NewPublicKey(userPriv.Public())
			if err != nil {
				t.Fatal(err)
			}
			caBlob := caKey.Marshal()
			fixture := buildCertificateFixture(
				t,
				testCase.keyType+"-cert-v01@openssh.com",
				userKey.Marshal(),
				caBlob,
				testCase.keyType+" "+base64.StdEncoding.EncodeToString(caBlob),
				func(data []byte) (string, []byte) {
					r, s, err := ecdsa.Sign(rand.Reader, caPriv, testCase.hash(data))
					if err != nil {
						t.Fatal(err)
					}
					sig := appendString(nil, r.Bytes())
					sig = appendString(sig, s.Bytes())
					return testCase.keyType, sig
				},
			)

			if err := VerifyCertificate(fixture.blob, fixture.caBlob); err != nil {
				t.Fatalf("ECDSA %s certificate did not verify: %v", testCase.name, err)
			}
			mutated := append([]byte(nil), fixture.blob...)
			mutated[fixture.principalOffset] ^= 0x01
			if err := VerifyCertificate(mutated, fixture.caBlob); !errors.Is(err, ErrSignatureMismatch) {
				t.Fatalf("ECDSA %s principal change should fail, got %v", testCase.name, err)
			}
		})
	}
}

func TestPEMAuthorityKey(t *testing.T) {
	caPriv, caBlob, userBlob := ed25519CA(t)
	fixture := buildCertificateFixture(
		t,
		"ssh-ed25519-cert-v01@openssh.com",
		userBlob,
		caBlob,
		"ssh-ed25519 "+base64.StdEncoding.EncodeToString(caBlob),
		func(data []byte) (string, []byte) {
			return "ssh-ed25519", ed25519.Sign(caPriv, data)
		},
	)

	der, err := x509.MarshalPKIXPublicKey(caPriv.Public())
	if err != nil {
		t.Fatal(err)
	}
	pemKey := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	if err := VerifyCertificate(fixture.blob, pemKey); err != nil {
		t.Fatalf("PEM authority key did not verify: %v", err)
	}
}

func TestSHA512RSASignature(t *testing.T) {
	caPriv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	userPriv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	caKey, err := NewPublicKey(caPriv.Public())
	if err != nil {
		t.Fatal(err)
	}
	userKey, err := NewPublicKey(userPriv.Public())
	if err != nil {
		t.Fatal(err)
	}
	caBlob := caKey.Marshal()
	fixture := buildCertificateFixture(
		t,
		"ssh-rsa-cert-v01@openssh.com",
		userKey.Marshal(),
		caBlob,
		"ssh-rsa "+base64.StdEncoding.EncodeToString(caBlob),
		func(data []byte) (string, []byte) {
			digest := sha512.Sum512(data)
			sig, err := rsa.SignPKCS1v15(rand.Reader, caPriv, crypto.SHA512, digest[:])
			if err != nil {
				t.Fatal(err)
			}
			return "rsa-sha2-512", sig
		},
	)
	if err := VerifyCertificate(fixture.blob, fixture.caBlob); err != nil {
		t.Fatalf("RSA SHA-512 certificate did not verify: %v", err)
	}
}

func TestOpenSSHCertificateInterop(t *testing.T) {
	sshKeygen, err := exec.LookPath("ssh-keygen")
	if err != nil {
		t.Skip("ssh-keygen is not available")
	}

	cases := []struct {
		name string
		args []string
	}{
		{name: "ed25519", args: []string{"-t", "ed25519"}},
		{name: "rsa", args: []string{"-t", "rsa", "-b", "2048"}},
		{name: "ecdsa", args: []string{"-t", "ecdsa", "-b", "256"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			testOpenSSHInterop(t, sshKeygen, testCase.args)
		})
	}
}

func testOpenSSHInterop(t *testing.T, sshKeygen string, keyArgs []string) {
	t.Helper()

	dir := t.TempDir()
	caPath := filepath.Join(dir, "ca")
	userPath := filepath.Join(dir, "user")
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(sshKeygen, args...)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("ssh-keygen %v: %v\n%s", args, err, output)
		}
	}
	caArgs := append([]string{"-q"}, keyArgs...)
	caArgs = append(caArgs, "-N", "", "-f", caPath)
	userArgs := append([]string{"-q"}, keyArgs...)
	userArgs = append(userArgs, "-N", "", "-f", userPath)
	run(caArgs...)
	run(userArgs...)
	run("-q", "-s", caPath, "-I", "interop-id", "-n", "alice",
		"-O", "force-command=echo interop-command",
		"-V", "-1m:+1h", userPath+".pub")

	caLine, err := os.ReadFile(caPath + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	certLine, err := os.ReadFile(userPath + "-cert.pub")
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyCertificate(certLine, caLine); err != nil {
		t.Fatalf("real OpenSSH certificate did not verify: %v", err)
	}

	parsed, err := ParseCertificate(certLine)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.CriticalOptions) != 1 || parsed.CriticalOptions[0].Name != "force-command" {
		t.Fatalf("real OpenSSH force-command was not parsed: %#v", parsed.CriticalOptions)
	}
	if !strings.Contains(string(parsed.CriticalOptions[0].Data), "interop-command") {
		t.Fatalf("unexpected force-command: %q", parsed.CriticalOptions[0].Data)
	}

	blob := parsed.Marshal()
	offset := bytes.Index(blob[:len(parsed.SignedBytes())], []byte("interop-command"))
	if offset < 0 {
		t.Fatal("force-command text not found in signed bytes")
	}
	blob[offset] ^= 0x01
	if err := VerifyCertificate(blob, caLine); !errors.Is(err, ErrSignatureMismatch) {
		t.Fatalf("real OpenSSH force-command change should fail, got %v", err)
	}
}

type marshaledBlob []byte

func (m marshaledBlob) Marshal() []byte {
	return m
}

type pointerMarshaledBlob struct {
	blob []byte
}

func (m *pointerMarshaledBlob) Marshal() []byte {
	return m.blob
}

func TestMarshalInterop(t *testing.T) {
	caPriv, caBlob, userBlob := ed25519CA(t)
	fixture := buildCertificateFixture(
		t,
		"ssh-ed25519-cert-v01@openssh.com",
		userBlob,
		caBlob,
		"ssh-ed25519 "+base64.StdEncoding.EncodeToString(caBlob),
		func(data []byte) (string, []byte) {
			return "ssh-ed25519", ed25519.Sign(caPriv, data)
		},
	)

	if err := VerifyCertificate(marshaledBlob(fixture.blob), marshaledBlob(fixture.caBlob)); err != nil {
		t.Fatalf("value Marshal interop failed: %v", err)
	}
	if err := VerifyCertificate(
		pointerMarshaledBlob{blob: fixture.blob},
		pointerMarshaledBlob{blob: fixture.caBlob},
	); err != nil {
		t.Fatalf("pointer Marshal interop failed: %v", err)
	}
}
