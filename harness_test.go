package rpki

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"math/big"
	"path"
	"testing"
	"time"
)

type testCA struct {
	Name   string
	Dir    string
	SIA    string
	Key    *ecdsa.PrivateKey
	Cert   *CertInfo
	Claims ResourceClaims
}

type testEE struct {
	Name   string
	Key    *ecdsa.PrivateKey
	Cert   *CertInfo
	Claims ResourceClaims
}

type testHarness struct {
	t     *testing.T
	files map[string][]byte
	now   time.Time
	root  *testCA
	tal   TAL
}

func newTestHarness(t *testing.T) *testHarness {
	t.Helper()
	now := time.Date(2026, 11, 27, 5, 0, 0, 0, time.UTC)
	h := &testHarness{t: t, files: map[string][]byte{}, now: now}
	rootClaims := mustClaims(t,
		[]string{"10.0.0.0/8", "192.168.0.0/16"},
		[]string{"2001:db8::/32"},
		[]string{"64500-64700"},
	)
	h.root = h.createCA("root", "root", nil, rootClaims)
	h.files["root/ta.cer"] = h.root.Cert.X509.Raw
	h.tal = TAL{
		Name:                 "unit-test-tal",
		URIs:                 []string{"rsync://rpki.test/root/ta.cer"},
		SubjectPublicKeyInfo: h.root.Cert.X509.RawSubjectPublicKeyInfo,
	}
	return h
}

func mustClaims(t *testing.T, v4CIDRs, v6CIDRs, asRanges []string) ResourceClaims {
	t.Helper()
	claims := ResourceClaims{}
	addIP := func(values []string, family IPFamily) *IPChoice {
		if len(values) == 0 {
			return nil
		}
		choice := &IPChoice{}
		for _, value := range values {
			prefix, err := ParseIPPrefix(value)
			if err != nil {
				t.Fatal(err)
			}
			if prefix.Family != family {
				t.Fatalf("family mismatch for %s", value)
			}
			choice.Ranges = append(choice.Ranges, prefix.Range())
		}
		return choice
	}
	claims.IPv4 = addIP(v4CIDRs, IPv4)
	claims.IPv6 = addIP(v6CIDRs, IPv6)
	if len(asRanges) > 0 {
		choice := &ASChoice{}
		for _, value := range asRanges {
			r, err := ParseASRange(value)
			if err != nil {
				t.Fatal(err)
			}
			choice.Ranges = append(choice.Ranges, r)
		}
		claims.ASNs = choice
	}
	return claims
}

func inheritClaims() ResourceClaims {
	return ResourceClaims{
		IPv4: &IPChoice{Inherit: true},
		IPv6: &IPChoice{Inherit: true},
		ASNs: &ASChoice{Inherit: true},
	}
}

func (h *testHarness) createCA(name, dir string, parent *testCA, claims ResourceClaims) *testCA {
	h.t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		h.t.Fatal(err)
	}
	sia := "rsync://rpki.test/" + dir + "/"
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		h.t.Fatal(err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		h.t.Fatal(err)
	}
	skiSum := sha256.Sum256(pubDER)
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: name, Organization: []string{"RPKI Test"}},
		NotBefore:             h.now.Add(-20 * 365 * 24 * time.Hour),
		NotAfter:              h.now.Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		SubjectKeyId:          skiSum[:20],
		ExtraExtensions:       h.rfc3779Extensions(claims, sia),
	}
	signer := key
	if parent != nil {
		tmpl.AuthorityKeyId = parent.Cert.X509.SubjectKeyId
		signer = parent.Key
	} else {
		tmpl.AuthorityKeyId = tmpl.SubjectKeyId
	}
	parentCert := tmpl
	if parent != nil {
		parentCert = parent.Cert.X509
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parentCert, &key.PublicKey, signer)
	if err != nil {
		h.t.Fatalf("CreateCertificate %s: %v", name, err)
	}
	cert, err := ParseCertificate(der)
	if err != nil {
		h.t.Fatal(err)
	}
	return &testCA{Name: name, Dir: dir, SIA: sia, Key: key, Cert: cert, Claims: claims}
}

func (h *testHarness) createEE(name string, issuer *testCA, claims ResourceClaims) *testEE {
	h.t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		h.t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		h.t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: name, Organization: []string{"RPKI EE"}},
		NotBefore:             h.now.Add(-365 * 24 * time.Hour),
		NotAfter:              h.now.Add(5 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  false,
		UnknownExtKeyUsage:    []asn1.ObjectIdentifier{oidKPSignedObject},
		AuthorityKeyId:        issuer.Cert.X509.SubjectKeyId,
		ExtraExtensions:       h.rfc3779Extensions(claims, ""),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, issuer.Cert.X509, &key.PublicKey, issuer.Key)
	if err != nil {
		h.t.Fatalf("CreateEECertificate %s: %v", name, err)
	}
	cert, err := ParseCertificate(der)
	if err != nil {
		h.t.Fatal(err)
	}
	return &testEE{Name: name, Key: key, Cert: cert, Claims: claims}
}

func (h *testHarness) rfc3779Extensions(claims ResourceClaims, sia string) []pkix.Extension {
	h.t.Helper()
	ipDER, asDER, err := MarshalResourceExtensions(claims)
	if err != nil {
		h.t.Fatal(err)
	}
	var exts []pkix.Extension
	if len(ipDER) > 0 {
		exts = append(exts, pkix.Extension{Id: oidPEIPAddrBlocks, Critical: true, Value: ipDER})
	}
	if len(asDER) > 0 {
		exts = append(exts, pkix.Extension{Id: oidPEASIdentifiers, Critical: true, Value: asDER})
	}
	if sia != "" {
		siaDER, err := MarshalSubjectInfoAccess(sia)
		if err != nil {
			h.t.Fatal(err)
		}
		exts = append(exts, pkix.Extension{Id: oidSubjectInfoAccess, Critical: false, Value: siaDER})
	}
	return exts
}

func (h *testHarness) writeCRL(ca *testCA, revoked ...*x509.Certificate) string {
	h.t.Helper()
	entries := make([]x509.RevocationListEntry, 0, len(revoked))
	for _, cert := range revoked {
		entries = append(entries, x509.RevocationListEntry{
			SerialNumber:   cert.SerialNumber,
			RevocationTime: h.now.Add(-24 * time.Hour),
		})
	}
	tmpl := &x509.RevocationList{
		Number:                    big.NewInt(time.Now().UnixNano()),
		ThisUpdate:                h.now.Add(-2 * time.Hour),
		NextUpdate:                h.now.Add(30 * 24 * time.Hour),
		RevokedCertificateEntries: entries,
	}
	der, err := x509.CreateRevocationList(rand.Reader, tmpl, ca.Cert.X509, ca.Key)
	if err != nil {
		h.t.Fatal(err)
	}
	name := ca.Name + ".crl"
	h.files[path.Join(ca.Dir, name)] = der
	return name
}

func (h *testHarness) writeManifest(ca *testCA, ee *testEE, number int64, thisUpdate time.Time, names []string) string {
	return h.writeManifestAt(ca, ee, number, thisUpdate, time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC), names)
}

func (h *testHarness) writeManifestAt(ca *testCA, ee *testEE, number int64, thisUpdate, nextUpdate time.Time, names []string) string {
	h.t.Helper()
	files := make([]FileHash, 0, len(names))
	for _, name := range names {
		full := path.Join(ca.Dir, name)
		content, ok := h.files[full]
		if !ok {
			h.t.Fatalf("manifest input missing %s", full)
		}
		files = append(files, FileHash{Name: name, SHA256: sha256.Sum256(content)})
	}
	manifest := &Manifest{
		ManifestNumber: big.NewInt(number),
		ThisUpdate:     thisUpdate,
		NextUpdate:     nextUpdate,
		FileHashes:     files,
	}
	eContent, err := MarshalManifest(manifest)
	if err != nil {
		h.t.Fatal(err)
	}
	cmsDER := createCMSTest(h.t, oidManifest, eContent, ee)
	name := ca.Name + ".mft"
	h.files[path.Join(ca.Dir, name)] = cmsDER
	return name
}

func (h *testHarness) writeROA(ca *testCA, ee *testEE, fileName string, asn uint64, prefixes []string, maxLength int) {
	h.t.Helper()
	var families []roaFamilyWire
	for _, cidr := range prefixes {
		p, err := ParseIPPrefix(cidr)
		if err != nil {
			h.t.Fatal(err)
		}
		bs, err := prefixToBitString(p.Prefix, p.Bits, p.Family)
		if err != nil {
			h.t.Fatal(err)
		}
		family := []byte{0, 1}
		if p.Family == IPv6 {
			family = []byte{0, 2}
		}
		found := -1
		for i := range families {
			if bytesEqual(families[i].AddressFamily, family) {
				found = i
				break
			}
		}
		addr := roaAddressWire{Address: bs}
		if maxLength > p.Bits {
			addr.MaxLength = maxLength
		}
		if found < 0 {
			families = append(families, roaFamilyWire{AddressFamily: family, Addresses: []roaAddressWire{addr}})
		} else {
			families[found].Addresses = append(families[found].Addresses, addr)
		}
	}
	roaDER, err := asn1.Marshal(roaWire{ASID: new(big.Int).SetUint64(asn), Blocks: families})
	if err != nil {
		h.t.Fatal(err)
	}
	h.files[path.Join(ca.Dir, fileName)] = createCMSTest(h.t, oidROA, roaDER, ee)
}

func (h *testHarness) writeASPA(ca *testCA, ee *testEE, fileName string, customer uint64, providers ...uint64) {
	h.t.Helper()
	providerASNs := make([]*big.Int, 0, len(providers))
	for _, asn := range providers {
		providerASNs = append(providerASNs, new(big.Int).SetUint64(asn))
	}
	aspaDER, err := asn1.Marshal(aspaWire{CustomerAS: new(big.Int).SetUint64(customer), ProviderASs: providerASNs})
	if err != nil {
		h.t.Fatal(err)
	}
	h.files[path.Join(ca.Dir, fileName)] = createCMSTest(h.t, oidASPA, aspaDER, ee)
}

func (h *testHarness) repository() *MapRepository {
	return NewMapRepository(h.files)
}

type cmsAttributeBuild struct {
	Type   asn1.ObjectIdentifier
	Values []asn1.RawValue `asn1:"set"`
}

type cmsSignerInfoBuild struct {
	Version         int
	SID             asn1.RawValue
	DigestAlgorithm pkix.AlgorithmIdentifier
	SignedAttrs     asn1.RawValue
	SignatureAlg    pkix.AlgorithmIdentifier
	Signature       []byte
}

type cmsSignedDataBuild struct {
	Version          int
	DigestAlgorithms []pkix.AlgorithmIdentifier `asn1:"set"`
	EncapContentInfo encapContentInfoWire
	Certificates     asn1.RawValue `asn1:"optional"`
	SignerInfos      asn1.RawValue
}

func createCMSTest(t *testing.T, contentType asn1.ObjectIdentifier, eContent []byte, ee *testEE) []byte {
	t.Helper()
	return createCMSWithKeyTest(t, contentType, eContent, ee, ee.Key)
}

func createCMSWithKeyTest(t *testing.T, contentType asn1.ObjectIdentifier, eContent []byte, ee *testEE, signerKey *ecdsa.PrivateKey) []byte {
	t.Helper()
	nullAlg := pkix.AlgorithmIdentifier{Algorithm: oidSHA256, Parameters: asn1.RawValue{Tag: asn1.TagNull}}

	contentTypeRaw := mustASN1Raw(t, contentType)
	digestRaw := mustASN1Raw(t, oidMessageDigest) // replaced below? no
	_ = digestRaw
	messageDigest := sha256.Sum256(eContent)
	digestOctet := mustASN1Raw(t, messageDigest[:])

	attrs := []cmsAttributeBuild{
		{Type: oidContentType, Values: []asn1.RawValue{contentTypeRaw}},
		{Type: oidMessageDigest, Values: []asn1.RawValue{digestOctet}},
	}
	var attrContent []byte
	for _, attr := range attrs {
		der, err := asn1.Marshal(attr)
		if err != nil {
			t.Fatal(err)
		}
		attrContent = append(attrContent, der...)
	}
	setDER, err := asn1.Marshal(asn1.RawValue{Class: 0, Tag: asn1.TagSet, IsCompound: true, Bytes: attrContent})
	if err != nil {
		t.Fatal(err)
	}
	signedDigest := sha256.Sum256(setDER)
	signature, err := ecdsa.SignASN1(rand.Reader, signerKey, signedDigest[:])
	if err != nil {
		t.Fatal(err)
	}

	var issuerRaw asn1.RawValue
	if _, err := asn1.Unmarshal(ee.Cert.X509.RawIssuer, &issuerRaw); err != nil {
		t.Fatal(err)
	}
	sidDER, err := asn1.Marshal(struct {
		Issuer asn1.RawValue
		Serial *big.Int
	}{Issuer: issuerRaw, Serial: ee.Cert.X509.SerialNumber})
	if err != nil {
		t.Fatal(err)
	}
	var sidRaw asn1.RawValue
	if _, err := asn1.Unmarshal(sidDER, &sidRaw); err != nil {
		t.Fatal(err)
	}

	si := cmsSignerInfoBuild{
		Version:         1,
		SID:             sidRaw,
		DigestAlgorithm: nullAlg,
		SignedAttrs:     asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 0, IsCompound: true, Bytes: attrContent},
		SignatureAlg:    pkix.AlgorithmIdentifier{Algorithm: oidECDSAWithSHA256},
		Signature:       signature,
	}
	siDER, err := asn1.Marshal(si)
	if err != nil {
		t.Fatal(err)
	}
	signerInfos := asn1.RawValue{Class: 0, Tag: asn1.TagSet, IsCompound: true, Bytes: siDER}

	signedData := cmsSignedDataBuild{
		Version:          1,
		DigestAlgorithms: []pkix.AlgorithmIdentifier{nullAlg},
		EncapContentInfo: encapContentInfoWire{ContentType: contentType, EContent: eContent},
		Certificates: asn1.RawValue{
			Class:      asn1.ClassContextSpecific,
			Tag:        0,
			IsCompound: true,
			Bytes:      ee.Cert.X509.Raw,
		},
		SignerInfos: signerInfos,
	}
	sdDER, err := asn1.Marshal(signedData)
	if err != nil {
		t.Fatal(err)
	}
	contentWrapper := asn1.RawValue{
		Class:      asn1.ClassContextSpecific,
		Tag:        0,
		IsCompound: true,
		Bytes:      sdDER,
	}
	ciDER, err := asn1.Marshal(contentInfoWire{ContentType: oidSignedData, Content: contentWrapper})
	if err != nil {
		t.Fatal(err)
	}
	return ciDER
}

func mustASN1Raw(t *testing.T, value any) asn1.RawValue {
	t.Helper()
	der, err := asn1.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var raw asn1.RawValue
	if _, err := asn1.Unmarshal(der, &raw); err != nil {
		t.Fatal(err)
	}
	return raw
}
