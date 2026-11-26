package sigverify

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/url"
	"testing"
	"time"
)

const (
	testOrigin  = "rekor.example.test"
	testKeyName = "test-log-key"
	testIssuer  = "https://accounts.example.com"
	testSubject = "spiffe://example.com/workload/builder"
)

type caMaterial struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	der  []byte
}

func generateECDSAKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate ECDSA key: %v", err)
	}
	return key
}

func mintCA(t *testing.T, parent *caMaterial, isRoot bool) *caMaterial {
	t.Helper()
	key := generateECDSAKey(t)
	serial := big.NewInt(time.Now().UnixNano())
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName: map[bool]string{true: "Test Fulcio Root", false: "Test Fulcio Intermediate"}[isRoot],
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}

	signerCert, signerKey := template, key
	if isRoot {
		parent = &caMaterial{cert: template, key: key}
	} else {
		signerCert = parent.cert
		signerKey = parent.key
	}
	der, err := x509.CreateCertificate(rand.Reader, template, signerCert, &key.PublicKey, signerKey)
	if err != nil {
		t.Fatalf("create CA certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse CA certificate: %v", err)
	}
	return &caMaterial{cert: cert, key: key, der: der}
}

type leafOptions struct {
	notBefore   time.Time
	notAfter    time.Time
	issuerV2    bool
	codeSigning bool
	emailSAN    string
	uriSAN      string
}

func defaultLeafOptions(notBefore time.Time) leafOptions {
	return leafOptions{
		notBefore:   notBefore,
		notAfter:    notBefore.Add(10 * time.Minute),
		codeSigning: true,
		uriSAN:      testSubject,
	}
}

func mintLeaf(t *testing.T, issuer *caMaterial, opts leafOptions) (*x509.Certificate, *ecdsa.PrivateKey, []byte) {
	t.Helper()
	key := generateECDSAKey(t)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano() + 1),
		Subject:      pkix.Name{CommonName: "test-fulcio-leaf"},
		NotBefore:    opts.notBefore,
		NotAfter:     opts.notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	if opts.codeSigning {
		template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning}
	}
	if opts.emailSAN != "" {
		template.EmailAddresses = []string{opts.emailSAN}
	}
	if opts.uriSAN != "" {
		parsed, err := url.Parse(opts.uriSAN)
		if err != nil {
			t.Fatalf("parse SAN URI: %v", err)
		}
		template.URIs = []*url.URL{parsed}
	}
	template.ExtraExtensions = append(template.ExtraExtensions, issuerExtension(t, testIssuer, opts.issuerV2))

	der, err := x509.CreateCertificate(rand.Reader, template, issuer.cert, &key.PublicKey, issuer.key)
	if err != nil {
		t.Fatalf("create leaf certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse leaf certificate: %v", err)
	}
	return cert, key, der
}

func issuerExtension(t *testing.T, issuer string, v2 bool) pkix.Extension {
	t.Helper()
	oid := OIDIssuerV1
	var value []byte
	var err error
	if v2 {
		oid = OIDIssuerV2
		value, err = asn1.Marshal(issuerV2Extension{IssuerURL: issuer})
	} else {
		value, err = asn1.Marshal(issuer)
	}
	if err != nil {
		t.Fatalf("marshal issuer extension: %v", err)
	}
	return pkix.Extension{Id: oid, Critical: false, Value: value}
}

func encodeCheckpoint(t *testing.T, origin string, size uint64, root []byte, key ed25519.PrivateKey) []byte {
	t.Helper()
	body := []byte(origin + "\n" +
		itoa(size) + "\n" +
		base64.StdEncoding.EncodeToString(root) + "\n\n")
	signature := ed25519.Sign(key, body)
	sigLine := signatureSeparator + testKeyName + " " + base64.StdEncoding.EncodeToString(signature)
	return append(append(append([]byte{}, body...), []byte(sigLine)...), '\n')
}

func signSET(t *testing.T, logIndex uint64, integratedTime int64, leafHash []byte, key ed25519.PrivateKey) []byte {
	t.Helper()
	payload, err := json.Marshal(signedEntryTimestamp{
		LogIndex:       logIndex,
		IntegratedTime: integratedTime,
		LeafHash:       leafHash,
	})
	if err != nil {
		t.Fatalf("marshal SET payload: %v", err)
	}
	return ed25519.Sign(key, payload)
}

func itoa(n uint64) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// fixture bundles everything a test needs to build and mutate an offline
// Sigstore bundle.
type fixture struct {
	root         *caMaterial
	intermediate *caMaterial
	logPub       ed25519.PublicKey
	logPriv      ed25519.PrivateKey
	leafCert     *x509.Certificate
	leafKey      *ecdsa.PrivateKey
	leafDER      []byte
	artifact     []byte
	artifactHash []byte
	signature    []byte
	integratedAt time.Time
	logIndex     uint64
	treeSize     uint64
	entry        LogEntry
	leafHashes   [][]byte
	proof        [][]byte
	rootHash     []byte
	checkpoint   []byte
	set          []byte
}

func buildFixture(t *testing.T, treeSize, logIndex uint64) *fixture {
	t.Helper()

	root := mintCA(t, nil, true)
	intermediate := mintCA(t, root, false)

	logPub, logPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate log key: %v", err)
	}

	integratedAt := time.Date(2026, 11, 27, 9, 30, 0, 0, time.UTC)
	opts := defaultLeafOptions(integratedAt.Add(-2 * time.Minute))
	leafCert, leafKey, leafDER := mintLeaf(t, intermediate, opts)

	artifact := []byte("release-artifact:hello-supply-chain@v1.4.2")
	digest := sha256.Sum256(artifact)
	signature, err := ecdsa.SignASN1(rand.Reader, leafKey, digest[:])
	if err != nil {
		t.Fatalf("sign artifact: %v", err)
	}

	entry := LogEntry{
		ArtifactDigest: Digest{Algorithm: "SHA-256", Value: digest[:]},
		Signature:      signature,
		PublicKeyHint:  "fulcio-leaf",
		CertificateDER: leafDER,
		SignatureAlgo:  "ECDSA-P256-SHA256",
		LogEntryType:   "hashedrekord",
	}

	// Construct an RFC 6962 tree of treeSize leaves; the target entry sits at
	// logIndex while sibling entries are distinct decoys.
	leafBytes := make([][]byte, treeSize)
	leafHashes := make([][]byte, treeSize)
	for i := range leafBytes {
		if uint64(i) == logIndex {
			leafBytes[i], err = entry.CanonicalBytes()
			if err != nil {
				t.Fatalf("canonical entry: %v", err)
			}
		} else {
			leafBytes[i] = []byte("decoy-entry-" + itoa(uint64(i)))
		}
		leafHashes[i] = LeafHash(leafBytes[i])
	}
	proof := refInclusionPath(leafBytes, int(logIndex))
	rootHash := refMTH(leafBytes)

	checkpoint := encodeCheckpoint(t, testOrigin, treeSize, rootHash, logPriv)
	set := signSET(t, logIndex, integratedAt.Unix(), leafHashes[logIndex], logPriv)

	return &fixture{
		root:         root,
		intermediate: intermediate,
		logPub:       logPub,
		logPriv:      logPriv,
		leafCert:     leafCert,
		leafKey:      leafKey,
		leafDER:      leafDER,
		artifact:     artifact,
		artifactHash: digest[:],
		signature:    signature,
		integratedAt: integratedAt,
		logIndex:     logIndex,
		treeSize:     treeSize,
		entry:        entry,
		leafHashes:   leafHashes,
		proof:        proof,
		rootHash:     rootHash,
		checkpoint:   checkpoint,
		set:          set,
	}
}

func (f *fixture) bundle() *Bundle {
	hashes := make([]b64, len(f.proof))
	for i, h := range f.proof {
		hashes[i] = h
	}
	return &Bundle{
		Version:                    BundleVersion,
		ArtifactDigest:             Digest{Algorithm: "SHA-256", Value: f.artifactHash},
		ArtifactSignature:          f.signature,
		LeafCertificateDER:         f.leafDER,
		IntermediateCertificateDER: []b64{f.intermediate.der},
		LogEntry:                   f.entry,
		IntegratedTime:             f.integratedAt.Unix(),
		InclusionProof: InclusionProof{
			LogIndex: f.logIndex,
			TreeSize: f.treeSize,
			Hashes:   hashes,
		},
		Checkpoint: f.checkpoint,
		SET:        f.set,
	}
}

func (f *fixture) trust() TrustedMaterial {
	return TrustedMaterial{
		FulcioRoots:              [][]byte{f.root.der},
		RekorPublicKey:           f.logPub,
		ExpectedCheckpointOrigin: testOrigin,
	}
}

func (f *fixture) policy() Policy {
	return Policy{
		AllowedOIDCIssuers: []string{testIssuer},
		AllowedSubjects:    []string{testSubject},
	}
}

func cloneBytes(b []byte) []byte { return append([]byte(nil), b...) }

// rebuildTree regenerates the canonical entry, Merkle leaf hashes, inclusion
// path, root, checkpoint and SET after a fixture mutation.
func (f *fixture) rebuildTree(t *testing.T) {
	t.Helper()

	canonical, err := f.entry.CanonicalBytes()
	if err != nil {
		t.Fatalf("canonical entry: %v", err)
	}

	leafBytes := make([][]byte, f.treeSize)
	f.leafHashes = make([][]byte, f.treeSize)
	for i := range leafBytes {
		if uint64(i) == f.logIndex {
			leafBytes[i] = canonical
		} else {
			leafBytes[i] = []byte("decoy-entry-" + itoa(uint64(i)))
		}
		f.leafHashes[i] = LeafHash(leafBytes[i])
	}

	f.proof = refInclusionPath(leafBytes, int(f.logIndex))
	f.rootHash = refMTH(leafBytes)
	f.checkpoint = encodeCheckpoint(t, testOrigin, f.treeSize, f.rootHash, f.logPriv)
	f.set = signSET(t, f.logIndex, f.integratedAt.Unix(), f.leafHashes[f.logIndex], f.logPriv)
}

// mintLeafWithIssuer creates a leaf with an arbitrary OIDC issuer extension,
// used to simulate a spoofed identity that policy must reject.
func mintLeafWithIssuer(t *testing.T, issuer *caMaterial, opts leafOptions, issuerURL string) (*x509.Certificate, *ecdsa.PrivateKey, []byte) {
	t.Helper()
	key := generateECDSAKey(t)
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano() + 2),
		Subject:               pkix.Name{CommonName: "test-fulcio-leaf"},
		NotBefore:             opts.notBefore,
		NotAfter:              opts.notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	if opts.codeSigning {
		template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning}
	}
	if opts.uriSAN != "" {
		parsed, err := url.Parse(opts.uriSAN)
		if err != nil {
			t.Fatalf("parse SAN URI: %v", err)
		}
		template.URIs = []*url.URL{parsed}
	}
	extValue, err := asn1.Marshal(issuerURL)
	if err != nil {
		t.Fatalf("marshal issuer: %v", err)
	}
	template.ExtraExtensions = []pkix.Extension{{Id: OIDIssuerV1, Critical: false, Value: extValue}}

	der, err := x509.CreateCertificate(rand.Reader, template, issuer.cert, &key.PublicKey, issuer.key)
	if err != nil {
		t.Fatalf("create leaf certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse leaf certificate: %v", err)
	}
	return cert, key, der
}
