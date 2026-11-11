package ocspcheck

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"math/big"
	"time"
)

// ocspStatus identifies the certificateStatus choice placed in a
// SingleResponse.
type ocspStatus int

const (
	statusGood ocspStatus = iota
	statusRevoked
	statusUnknown
)

// tbsForMarshal is the wire shape of ResponseData used only by the test
// responder. It deliberately omits the parser-only RawTBSCertificate field.
type tbsForMarshal struct {
	Version            int `asn1:"optional,default:0,explicit,tag:0"`
	ResponderID        asn1.RawValue
	ProducedAt         time.Time
	Responses          []singleResponse
	ResponseExtensions []pkix.Extension `asn1:"explicit,tag:1,optional"`
}

// responseInput describes one synthetic OCSP response to build.
type responseInput struct {
	issuer     *x509.Certificate
	subject    *x509.Certificate
	signerCert *x509.Certificate
	signerKey  crypto.PrivateKey

	hashOID    asn1.ObjectIdentifier
	hashFunc   crypto.Hash
	sigOID     asn1.ObjectIdentifier
	pss        bool
	useByKey   bool
	embedCerts []*x509.Certificate
	status     ocspStatus
	thisUpdate time.Time
	nextUpdate time.Time
	omitNext   bool
	producedAt time.Time

	// Mutations.
	wrongSerial   *big.Int
	wrongNameHash []byte
	wrongKeyHash  []byte
	corruptSig    bool
	responseType  asn1.ObjectIdentifier
}

func defaultResponseInput(p *testPKI, signerCert *x509.Certificate, signerKey crypto.PrivateKey) responseInput {
	return responseInput{
		issuer:     p.caCert,
		subject:    p.leafCert,
		signerCert: signerCert,
		signerKey:  signerKey,
		hashOID:    oidHashSHA1,
		hashFunc:   crypto.SHA1,
		sigOID:     oidSigECDSAWithSHA256,
		thisUpdate: p.now.Add(-time.Minute),
		nextUpdate: p.now.Add(time.Hour),
		producedAt: p.now.Add(-time.Minute),
		embedCerts: []*x509.Certificate{signerCert},
	}
}

// buildOCSPResponse constructs a complete, DER-encoded OCSP envelope.
func buildOCSPResponse(in responseInput) []byte {
	certID := buildCertID(in)

	var statusChoice asn1.RawValue
	switch in.status {
	case statusGood:
		statusChoice = asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 0}
	case statusUnknown:
		statusChoice = asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 2}
	case statusRevoked:
		revTime, _ := asn1.Marshal(in.thisUpdate.Add(-2 * time.Hour))
		revChoice := asn1.RawValue{
			Class: asn1.ClassContextSpecific, Tag: 1, IsCompound: true,
			Bytes: revTime,
		}
		statusChoice = mustMarshalRawValue(revChoice)
	}

	singleFields := []interface{}{certID, statusChoice, in.thisUpdate}
	if !in.omitNext {
		nextDER, _ := asn1.Marshal(in.nextUpdate)
		nextChoice := asn1.RawValue{
			Class: asn1.ClassContextSpecific, Tag: 0, IsCompound: true,
			Bytes: nextDER,
		}
		singleFields = append(singleFields, mustMarshalRawValue(nextChoice))
	}
	singleDER, err := asn1.Marshal(singleFields)
	if err != nil {
		panic(err)
	}
	var single singleResponse
	if _, err := asn1.Unmarshal(singleDER, &single); err != nil {
		panic(err)
	}

	tbs := tbsForMarshal{
		ResponderID: buildResponderID(in.signerCert, in.useByKey),
		ProducedAt:  in.producedAt,
		Responses:   []singleResponse{single},
	}
	tbsDER, err := asn1.Marshal(tbs)
	if err != nil {
		panic(err)
	}
	var rawTBS asn1.RawValue
	if _, err := asn1.Unmarshal(tbsDER, &rawTBS); err != nil {
		panic(err)
	}

	signature := signTBS(rawTBS.FullBytes, in)
	if in.corruptSig {
		signature[0] ^= 0xff
	}

	basic := basicOCSPResponseDER(rawTBS, in.sigOID, signature, in.embedCerts)

	envelope := ocspResponse{
		Status: statusSuccessful,
		ResponseBytes: []responseBytes{{
			ResponseType: effectiveResponseType(in.responseType),
			Response: asn1.RawValue{
				Class: asn1.ClassUniversal, Tag: asn1.TagOctetString,
				IsCompound: false, Bytes: basic,
			},
		}},
	}
	der, err := asn1.Marshal(envelope)
	if err != nil {
		panic(err)
	}
	return der
}

func effectiveResponseType(override asn1.ObjectIdentifier) asn1.ObjectIdentifier {
	if override != nil {
		return override
	}
	return oidBasicResponse
}

func buildCertID(in responseInput) certID {
	serial := in.subject.SerialNumber
	if in.wrongSerial != nil {
		serial = in.wrongSerial
	}

	nameHash := hashBytes(in.hashFunc, in.issuer.RawSubject)
	if in.wrongNameHash != nil {
		nameHash = in.wrongNameHash
	}
	keyBytes, err := subjectPublicKeyBytes(in.issuer)
	if err != nil {
		panic(err)
	}
	keyHash := hashBytes(in.hashFunc, keyBytes)
	if in.wrongKeyHash != nil {
		keyHash = in.wrongKeyHash
	}

	return certID{
		HashAlgorithm: pkix.AlgorithmIdentifier{Algorithm: in.hashOID},
		NameHash:      nameHash,
		KeyHash:       keyHash,
		SerialNumber:  serial,
	}
}

func hashBytes(hash crypto.Hash, data []byte) []byte {
	h := hash.New()
	h.Write(data)
	return h.Sum(nil)
}

// mustMarshalRawValue re-marshals a RawValue so its FullBytes (tag, length,
// value) are populated with a correct length encoding.
func mustMarshalRawValue(rv asn1.RawValue) asn1.RawValue {
	der, err := asn1.Marshal(rv)
	if err != nil {
		panic(err)
	}
	var out asn1.RawValue
	if _, err := asn1.Unmarshal(der, &out); err != nil {
		panic(err)
	}
	return out
}

func buildResponderID(cert *x509.Certificate, byKey bool) asn1.RawValue {
	if byKey {
		keyBytes, err := subjectPublicKeyBytes(cert)
		if err != nil {
			panic(err)
		}
		sum := sha1.Sum(keyBytes)
		return mustMarshalRawValue(asn1.RawValue{
			Class: asn1.ClassContextSpecific, Tag: 2,
			Bytes: sum[:],
		})
	}
	var name asn1.RawValue
	if _, err := asn1.Unmarshal(cert.RawSubject, &name); err != nil {
		panic(err)
	}
	return mustMarshalRawValue(asn1.RawValue{
		Class: asn1.ClassContextSpecific, Tag: 1,
		Bytes: name.Bytes,
	})
}

func signTBS(tbs []byte, in responseInput) []byte {
	switch key := in.signerKey.(type) {
	case *ecdsa.PrivateKey:
		hashFunc := sigAlgorithms[in.sigOID.String()].hash
		h := hashFunc.New()
		h.Write(tbs)
		sig, err := ecdsa.SignASN1(rand.Reader, key, h.Sum(nil))
		if err != nil {
			panic(err)
		}
		return sig
	case *rsa.PrivateKey:
		if in.pss {
			h := crypto.SHA256.New()
			h.Write(tbs)
			sig, err := rsa.SignPSS(rand.Reader, key, crypto.SHA256, h.Sum(nil), nil)
			if err != nil {
				panic(err)
			}
			return sig
		}
		hashFunc := sigAlgorithms[in.sigOID.String()].hash
		h := hashFunc.New()
		h.Write(tbs)
		sig, err := rsa.SignPKCS1v15(rand.Reader, key, hashFunc, h.Sum(nil))
		if err != nil {
			panic(err)
		}
		return sig
	default:
		panic("unsupported signer key type")
	}
}

func basicOCSPResponseDER(tbs asn1.RawValue, sigOID asn1.ObjectIdentifier, signature []byte, embedded []*x509.Certificate) []byte {
	type basicResponse struct {
		TBSResponseData    asn1.RawValue
		SignatureAlgorithm pkix.AlgorithmIdentifier
		Signature          asn1.BitString
		Certs              [][]byte `asn1:"explicit,tag:0,optional"`
	}
	var certDERs [][]byte
	for _, cert := range embedded {
		certDERs = append(certDERs, cert.Raw)
	}
	der, err := asn1.Marshal(basicResponse{
		TBSResponseData:    tbs,
		SignatureAlgorithm: pkix.AlgorithmIdentifier{Algorithm: sigOID},
		Signature:          asn1.BitString{Bytes: signature, BitLength: len(signature) * 8},
		Certs:              certDERs,
	})
	if err != nil {
		panic(err)
	}
	return der
}
