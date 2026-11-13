package netsec

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"math/big"
	"testing"
	"time"
)

type testOCSPResponse struct {
	ResponseStatus asn1.Enumerated
	ResponseBytes  responseBytes `asn1:"explicit,tag:0"`
}

func TestConflictingSingleResponsesAreRejected(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	issuer, issuerKey, leaf := testCertificates(t, now)
	certID := testCertID(t, issuer, leaf.SerialNumber, oidSHA1)

	entries := []rawSingleResponse{
		goodSingleResponse(certID, now.Add(-time.Minute), now.Add(time.Hour)),
		revokedSingleResponse(t, certID, now.Add(-time.Minute), now.Add(time.Hour), now.Add(-30*time.Minute), KeyCompromise),
	}
	der := signedResponse(t, issuer, issuerKey, now, entries)

	if _, err := ParseBasicResponse(der); !errors.Is(err, ErrConflictingStatus) {
		t.Fatalf("ParseBasicResponse error = %v, want ErrConflictingStatus", err)
	}
	if _, err := ParseResponse(der); !errors.Is(err, ErrConflictingStatus) {
		t.Fatalf("ParseResponse error = %v, want ErrConflictingStatus", err)
	}
	if response, err := ParseResponseForCert(der, leaf, issuer); !errors.Is(err, ErrConflictingStatus) {
		t.Fatalf("ParseResponseForCert = (%+v, %v), want conflict", response, err)
	}
	if response, err := VerifyResponse(der, leaf, issuer); !errors.Is(err, ErrConflictingStatus) {
		t.Fatalf("VerifyResponse = (%+v, %v), want conflict", response, err)
	}
	if status, err := CheckStatus(der, leaf, issuer); !errors.Is(err, ErrConflictingStatus) || status == Good {
		t.Fatalf("CheckStatus = (%v, %v), want conflict", status, err)
	}
}

func TestRevokedOnlyIsReportedAsRevoked(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	issuer, issuerKey, leaf := testCertificates(t, now)
	certID := testCertID(t, issuer, leaf.SerialNumber, oidSHA1)
	revokedAt := now.Add(-30 * time.Minute)

	entries := []rawSingleResponse{
		revokedSingleResponse(t, certID, now.Add(-time.Minute), now.Add(time.Hour), revokedAt, KeyCompromise),
	}
	der := signedResponse(t, issuer, issuerKey, now, entries)

	response, err := ParseResponseForCert(der, leaf, issuer)
	if err != nil {
		t.Fatalf("ParseResponseForCert: %v", err)
	}
	if response.Status != Revoked {
		t.Fatalf("status = %s, want revoked", StatusString(response.Status))
	}
	if !response.RevokedAt.Equal(revokedAt) {
		t.Fatalf("RevokedAt = %s, want %s", response.RevokedAt, revokedAt)
	}

	verified, err := VerifyResponse(der, leaf, issuer)
	if err != nil {
		t.Fatalf("VerifyResponse: %v", err)
	}
	if verified.Status != Revoked {
		t.Fatalf("verified status = %s, want revoked", StatusString(verified.Status))
	}
	if status, err := CheckStatus(der, leaf, issuer); err != nil || status != Revoked {
		t.Fatalf("CheckStatus = (%s, %v), want (revoked, nil)", StatusString(status), err)
	}
	if err := ValidateResponse(der, leaf, issuer); !errors.Is(err, ErrCertificateRevoked) {
		t.Fatalf("ValidateResponse error = %v, want ErrCertificateRevoked", err)
	}
}

func TestDuplicateRevokedResponsesRemainRevoked(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	issuer, issuerKey, leaf := testCertificates(t, now)
	certID := testCertID(t, issuer, leaf.SerialNumber, oidSHA1)

	entries := []rawSingleResponse{
		revokedSingleResponse(t, certID, now.Add(-2*time.Minute), now.Add(time.Hour), now.Add(-30*time.Minute), KeyCompromise),
		revokedSingleResponse(t, certID, now.Add(-time.Minute), now.Add(time.Hour), now.Add(-20*time.Minute), Superseded),
	}
	der := signedResponse(t, issuer, issuerKey, now, entries)

	response, err := ParseResponseForCert(der, leaf, issuer)
	if err != nil {
		t.Fatalf("ParseResponseForCert: %v", err)
	}
	if response.Status != Revoked {
		t.Fatalf("status = %s, want revoked", StatusString(response.Status))
	}
	if len(response.Responses) != 2 {
		t.Fatalf("matched responses = %d, want 2", len(response.Responses))
	}
	if !response.RevokedAt.Equal(now.Add(-30 * time.Minute)) {
		t.Fatalf("RevokedAt = %s, want conservative earliest revocation time", response.RevokedAt)
	}
}

func TestGoodOnlyIsReportedAsGood(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	issuer, issuerKey, leaf := testCertificates(t, now)
	certID := testCertID(t, issuer, leaf.SerialNumber, oidSHA1)

	entries := []rawSingleResponse{
		goodSingleResponse(certID, now.Add(-time.Minute), now.Add(time.Hour)),
	}
	der := signedResponse(t, issuer, issuerKey, now, entries)

	response, err := VerifyResponse(der, leaf, issuer)
	if err != nil {
		t.Fatalf("VerifyResponse: %v", err)
	}
	if response.Status != Good {
		t.Fatalf("status = %s, want good", StatusString(response.Status))
	}
	if err := ValidateResponse(der, leaf, issuer); err != nil {
		t.Fatalf("ValidateResponse: %v", err)
	}
}

func TestConflictAcrossDifferentCertIDHashesIsRejected(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	issuer, issuerKey, leaf := testCertificates(t, now)
	sha1ID := testCertID(t, issuer, leaf.SerialNumber, oidSHA1)
	sha256ID := testCertID(t, issuer, leaf.SerialNumber, oidSHA256)

	entries := []rawSingleResponse{
		goodSingleResponse(sha1ID, now.Add(-time.Minute), now.Add(time.Hour)),
		revokedSingleResponse(t, sha256ID, now.Add(-time.Minute), now.Add(time.Hour), now.Add(-30*time.Minute), KeyCompromise),
	}
	der := signedResponse(t, issuer, issuerKey, now, entries)

	if response, err := ParseResponseForCert(der, leaf, issuer); !errors.Is(err, ErrConflictingStatus) {
		t.Fatalf("ParseResponseForCert = (%+v, %v), want conflict", response, err)
	}
	if response, err := VerifyResponse(der, leaf, issuer); !errors.Is(err, ErrConflictingStatus) {
		t.Fatalf("VerifyResponse = (%+v, %v), want conflict", response, err)
	}
}

func TestInvalidSignatureIsRejected(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	issuer, issuerKey, leaf := testCertificates(t, now)
	certID := testCertID(t, issuer, leaf.SerialNumber, oidSHA1)
	entries := []rawSingleResponse{goodSingleResponse(certID, now.Add(-time.Minute), now.Add(time.Hour))}
	der := signedResponse(t, issuer, issuerKey, now, entries)

	var top testOCSPResponse
	if _, err := asn1.Unmarshal(der, &top); err != nil {
		t.Fatal(err)
	}
	var basic basicOCSPResponse
	if _, err := asn1.Unmarshal(top.ResponseBytes.Response, &basic); err != nil {
		t.Fatal(err)
	}
	basic.Signature.Bytes[len(basic.Signature.Bytes)-1] ^= 0xff
	basicDER, err := asn1.Marshal(basic)
	if err != nil {
		t.Fatal(err)
	}
	top.ResponseBytes.Response = basicDER
	tampered, err := asn1.Marshal(top)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := VerifyResponse(tampered, leaf, issuer); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("VerifyResponse error = %v, want ErrInvalidSignature", err)
	}
}

func TestDelegatedResponderWithEmbeddedCertificate(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	issuer, issuerKey, leaf := testCertificates(t, now)
	responder, responderKey := testResponderCertificate(t, issuer, issuerKey, now)
	certID := testCertID(t, issuer, leaf.SerialNumber, oidSHA1)
	entries := []rawSingleResponse{goodSingleResponse(certID, now.Add(-time.Minute), now.Add(time.Hour))}

	der := signedResponseBy(t, responder, responderKey, responder, now, entries, true)
	response, err := VerifyResponse(der, leaf, issuer)
	if err != nil {
		t.Fatalf("VerifyResponse: %v", err)
	}
	if response.Status != Good {
		t.Fatalf("status = %s, want good", StatusString(response.Status))
	}
	if response.Certificate == nil || !response.Certificate.Equal(responder) {
		t.Fatal("verified responder certificate was not returned")
	}
}

func TestUnknownStatusFailsClosed(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	issuer, issuerKey, leaf := testCertificates(t, now)
	certID := testCertID(t, issuer, leaf.SerialNumber, oidSHA1)
	entries := []rawSingleResponse{unknownSingleResponse(certID, now.Add(-time.Minute), now.Add(time.Hour))}
	der := signedResponse(t, issuer, issuerKey, now, entries)

	if status, err := CheckStatus(der, leaf, issuer); err != nil || status != Unknown {
		t.Fatalf("CheckStatus = (%s, %v), want (unknown, nil)", StatusString(status), err)
	}
	if err := ValidateResponse(der, leaf, issuer); !errors.Is(err, ErrUnknownStatus) {
		t.Fatalf("ValidateResponse error = %v, want ErrUnknownStatus", err)
	}
}

func TestExpiredNextUpdateIsRejected(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	issuer, issuerKey, leaf := testCertificates(t, now)
	certID := testCertID(t, issuer, leaf.SerialNumber, oidSHA1)
	entries := []rawSingleResponse{goodSingleResponse(certID, now.Add(-time.Minute), now.Add(time.Hour))}
	der := signedResponse(t, issuer, issuerKey, now, entries)

	_, err := VerifyResponseWithOptions(der, leaf, issuer, VerifyOptions{
		Now:       now.Add(2 * time.Hour),
		ClockSkew: time.Minute,
	})
	if !errors.Is(err, ErrResponseExpired) {
		t.Fatalf("VerifyResponseWithOptions error = %v, want ErrResponseExpired", err)
	}
}

func testCertificates(t *testing.T, now time.Time) (*x509.Certificate, *rsa.PrivateKey, *x509.Certificate) {
	t.Helper()

	issuerKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	issuerTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(100),
		Subject:               pkix.Name{CommonName: "OCSP test issuer"},
		NotBefore:             now.Add(-24 * time.Hour),
		NotAfter:              now.Add(7 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	issuerDER, err := x509.CreateCertificate(rand.Reader, issuerTemplate, issuerTemplate, &issuerKey.PublicKey, issuerKey)
	if err != nil {
		t.Fatal(err)
	}
	issuer, err := x509.ParseCertificate(issuerDER)
	if err != nil {
		t.Fatal(err)
	}

	leafKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(200),
		Subject:      pkix.Name{CommonName: "OCSP test leaf"},
		NotBefore:    now.Add(-24 * time.Hour),
		NotAfter:     now.Add(7 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, issuer, &leafKey.PublicKey, issuerKey)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(leafDER)
	if err != nil {
		t.Fatal(err)
	}
	return issuer, issuerKey, leaf
}

func testCertID(t *testing.T, issuer *x509.Certificate, serial *big.Int, hashOID asn1.ObjectIdentifier) rawCertID {
	t.Helper()
	nameHash, err := hashBytes(hashOID, issuer.RawSubject)
	if err != nil {
		t.Fatal(err)
	}
	keyHash, err := issuerPublicKeyHash(hashOID, issuer)
	if err != nil {
		t.Fatal(err)
	}
	return rawCertID{
		HashAlgorithm:  pkix.AlgorithmIdentifier{Algorithm: hashOID, Parameters: asn1.NullRawValue},
		IssuerNameHash: nameHash,
		IssuerKeyHash:  keyHash,
		SerialNumber:   new(big.Int).Set(serial),
	}
}

func testResponderCertificate(t *testing.T, issuer *x509.Certificate, issuerKey *rsa.PrivateKey, now time.Time) (*x509.Certificate, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(300),
		Subject:               pkix.Name{CommonName: "OCSP delegated responder"},
		NotBefore:             now.Add(-24 * time.Hour),
		NotAfter:              now.Add(7 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageOCSPSigning},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, issuer, &key.PublicKey, issuerKey)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert, key
}

func goodSingleResponse(certID rawCertID, thisUpdate, nextUpdate time.Time) rawSingleResponse {
	return rawSingleResponse{
		CertID:     certID,
		CertStatus: asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 0},
		ThisUpdate: thisUpdate,
		NextUpdate: nextUpdate,
	}
}

func unknownSingleResponse(certID rawCertID, thisUpdate, nextUpdate time.Time) rawSingleResponse {
	return rawSingleResponse{
		CertID:     certID,
		CertStatus: asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 2},
		ThisUpdate: thisUpdate,
		NextUpdate: nextUpdate,
	}
}

func revokedSingleResponse(t *testing.T, certID rawCertID, thisUpdate, nextUpdate, revokedAt time.Time, reason int) rawSingleResponse {
	t.Helper()
	infoDER, err := asn1.Marshal(revokedInfo{
		RevocationTime:   revokedAt,
		RevocationReason: asn1.Enumerated(reason),
	})
	if err != nil {
		t.Fatal(err)
	}
	var info asn1.RawValue
	if _, err := asn1.Unmarshal(infoDER, &info); err != nil {
		t.Fatal(err)
	}
	return rawSingleResponse{
		CertID: certID,
		CertStatus: asn1.RawValue{
			Class:      asn1.ClassContextSpecific,
			Tag:        1,
			IsCompound: true,
			Bytes:      info.Bytes,
		},
		ThisUpdate: thisUpdate,
		NextUpdate: nextUpdate,
	}
}

func signedResponse(t *testing.T, issuer *x509.Certificate, signerKey *rsa.PrivateKey, producedAt time.Time, entries []rawSingleResponse) []byte {
	t.Helper()
	return signedResponseBy(t, issuer, signerKey, issuer, producedAt, entries, false)
}

func signedResponseBy(t *testing.T, signer *x509.Certificate, signerKey *rsa.PrivateKey, responder *x509.Certificate, producedAt time.Time, entries []rawSingleResponse, embedSigner bool) []byte {
	t.Helper()

	responderID := asn1.RawValue{
		Class:      asn1.ClassContextSpecific,
		Tag:        1,
		IsCompound: true,
		Bytes:      responder.RawSubject,
	}
	data := responseData{
		ResponderID: responderID,
		ProducedAt:  producedAt,
		Responses:   entries,
	}
	dataDER, err := asn1.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	var tbs asn1.RawValue
	if _, err := asn1.Unmarshal(dataDER, &tbs); err != nil {
		t.Fatal(err)
	}

	digest := sha256.Sum256(tbs.FullBytes)
	signature, err := rsa.SignPKCS1v15(rand.Reader, signerKey, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	basic := basicOCSPResponse{
		TBSResponseData: tbs,
		SignatureAlgorithm: pkix.AlgorithmIdentifier{
			Algorithm:  oidSHA256WithRSA,
			Parameters: asn1.NullRawValue,
		},
		Signature: asn1.BitString{Bytes: signature, BitLength: len(signature) * 8},
	}
	if embedSigner {
		var certRaw asn1.RawValue
		if _, err := asn1.Unmarshal(signer.Raw, &certRaw); err != nil {
			t.Fatal(err)
		}
		basic.Certificates = []asn1.RawValue{certRaw}
	}
	basicDER, err := asn1.Marshal(basic)
	if err != nil {
		t.Fatal(err)
	}

	top := testOCSPResponse{
		ResponseStatus: asn1.Enumerated(Good),
		ResponseBytes: responseBytes{
			ResponseType: oidBasicOCSP,
			Response:     basicDER,
		},
	}
	der, err := asn1.Marshal(top)
	if err != nil {
		t.Fatal(err)
	}
	return der
}
