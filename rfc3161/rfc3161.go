// Package rfc3161 verifies RFC 3161 TimeStampToken (TST) tokens against the
// values of an original TimeStampReq and a local trust policy.
//
// Verification binds a token to the exact data it is supposed to protect:
// the message imprint must match the digest of the signed data, the nonce
// must match the request, and the token policy must be on a caller-supplied
// whitelist. The signing certificate is additionally required to carry a
// single, critical id-kp-timeStamping extended key usage, and the claimed
// genTime (including its accuracy window) must fall inside the certificate
// validity period and must not be in the future.
package rfc3161

import (
	"crypto"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"math/big"
	"strconv"
	"strings"
	"time"
)

// Sentinel errors wrapping the concrete failure of a verification step.
var (
	ErrInvalidToken   = errors.New("rfc3161: malformed time stamp token")
	ErrMessageImprint = errors.New("rfc3161: message imprint mismatch")
	ErrNonce          = errors.New("rfc3161: nonce mismatch")
	ErrPolicy         = errors.New("rfc3161: token policy not whitelisted")
	ErrEKU            = errors.New("rfc3161: signer certificate extended key usage invalid")
	ErrTime           = errors.New("rfc3161: time stamp time outside allowed window")
	ErrSignature      = errors.New("rfc3161: token signature verification failed")
	ErrChain          = errors.New("rfc3161: signer certificate chain verification failed")
)

var (
	// RFC 5652 CMS object and attribute identifiers.
	oidSignedData  = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 2}
	oidContentType = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 3}
	oidMessageDgst = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 4}

	// id-ct-TSTInfo (RFC 3161 section 2.1).
	oidTSTInfo = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 16, 1, 4}

	// id-ce-extKeyUsage and id-kp-timeStamping.
	oidExtKeyUsage    = asn1.ObjectIdentifier{2, 5, 29, 37}
	oidKPTimeStamping = asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 3, 8}

	// Signature algorithm identifiers.
	oidRSAEncryption   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 1}
	oidRSAPSS          = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 10}
	oidMGF1            = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 8}
	oidECDSAWithSHA1   = asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 1}
	oidECDSAWithSHA224 = asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 3, 1}
	oidECDSAWithSHA256 = asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 3, 2}
	oidECDSAWithSHA384 = asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 3, 3}
	oidECDSAWithSHA512 = asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 3, 4}
	oidEd25519         = asn1.ObjectIdentifier{1, 3, 101, 112}
)

var hashOIDs = map[crypto.Hash]asn1.ObjectIdentifier{
	crypto.SHA1:       {1, 3, 14, 3, 2, 26},
	crypto.SHA224:     {2, 16, 840, 1, 101, 3, 4, 2, 4},
	crypto.SHA256:     {2, 16, 840, 1, 101, 3, 4, 2, 1},
	crypto.SHA384:     {2, 16, 840, 1, 101, 3, 4, 2, 2},
	crypto.SHA512:     {2, 16, 840, 1, 101, 3, 4, 2, 3},
	crypto.SHA512_224: {2, 16, 840, 1, 101, 3, 4, 2, 5},
	crypto.SHA512_256: {2, 16, 840, 1, 101, 3, 4, 2, 6},
	crypto.SHA3_224:   {2, 16, 840, 1, 101, 3, 4, 2, 8},
	crypto.SHA3_256:   {2, 16, 840, 1, 101, 3, 4, 2, 9},
	crypto.SHA3_384:   {2, 16, 840, 1, 101, 3, 4, 2, 10},
	crypto.SHA3_512:   {2, 16, 840, 1, 101, 3, 4, 2, 11},
}

var oidHashes = reverseHashOIDs()

// ecdsaHashOIDs maps ECDSA-with-hash signature algorithm OIDs (RFC 5753) to
// the hash they bind; these are separate from the digest-algorithm OIDs.
var ecdsaHashOIDs = map[string]crypto.Hash{
	oidECDSAWithSHA1.String():   crypto.SHA1,
	oidECDSAWithSHA224.String(): crypto.SHA224,
	oidECDSAWithSHA256.String(): crypto.SHA256,
	oidECDSAWithSHA384.String(): crypto.SHA384,
	oidECDSAWithSHA512.String(): crypto.SHA512,
}

func reverseHashOIDs() map[string]crypto.Hash {
	m := make(map[string]crypto.Hash, len(hashOIDs))
	for h, oid := range hashOIDs {
		m[oid.String()] = h
	}
	return m
}

// Options carries the expected request values and trust material used to
// verify a TimeStampToken.
type Options struct {
	// HashAlgorithm is the digest algorithm used to produce HashedMessage and
	// the one required in the token's messageImprint.
	HashAlgorithm crypto.Hash

	// HashedMessage is the digest of the data the time stamp is expected to
	// cover (for example the digest of the signed artifact being verified).
	HashedMessage []byte

	// Nonce is the nonce sent in the TimeStampReq. A non-nil value requires an
	// identical token nonce; a nil value requires the token to omit it.
	Nonce *big.Int

	// PolicyWhitelist lists the dotted-decimal OIDs of acceptable TSA policies.
	// It must be non-empty: verification never accepts an unspecified policy.
	PolicyWhitelist []string

	// Roots holds the trusted TSA root certificates. When nil, chain
	// validation against a trust anchor is skipped; all other checks still
	// apply.
	Roots *x509.CertPool

	// CurrentTime is the verification instant. The zero value means time.Now.
	CurrentTime time.Time
}

// Info describes a successfully verified TimeStampToken.
type Info struct {
	// GenTime is the time claimed by the TSA.
	GenTime time.Time

	// Accuracy is the uncertainty window declared by the token; zero when
	// the token omits the accuracy field.
	Accuracy time.Duration

	// GenTimeUpper is GenTime plus Accuracy, the latest instant the token can
	// truthfully claim.
	GenTimeUpper time.Time

	// Policy is the dotted-decimal OID of the policy under which the token was
	// issued.
	Policy string

	// SerialNumber is the unique serial assigned by the TSA.
	SerialNumber *big.Int

	// Signer is the parsed TSA signing certificate from the token's
	// certificate bag.
	Signer *x509.Certificate
}

// TSTInfo and related structures from RFC 3161 section 2.4.2.

type tstInfo struct {
	Version        int
	Policy         asn1.ObjectIdentifier
	MessageImprint messageImprint
	SerialNumber   *big.Int
	GenTime        time.Time
	Accuracy       accuracy         `asn1:"optional"`
	Ordering       bool             `asn1:"optional"`
	Nonce          *big.Int         `asn1:"optional"`
	TSA            asn1.RawValue    `asn1:"optional,tag:0"`
	Extensions     []pkix.Extension `asn1:"optional,tag:1"`
}

type messageImprint struct {
	HashAlgorithm pkix.AlgorithmIdentifier
	HashedMessage []byte
}

type accuracy struct {
	Seconds int64 `asn1:"optional"`
	Millis  int   `asn1:"optional,tag:0"`
	Micros  int   `asn1:"optional,tag:1"`
}

func (opts Options) validate() (map[string]asn1.ObjectIdentifier, error) {
	if _, ok := hashOIDs[opts.HashAlgorithm]; !ok {
		return nil, errors.New("rfc3161: unsupported hash algorithm")
	}
	if !opts.HashAlgorithm.Available() {
		return nil, errors.New("rfc3161: hash algorithm is not linked into the binary")
	}
	if len(opts.HashedMessage) != opts.HashAlgorithm.Size() {
		return nil, errors.New("rfc3161: hashed message length does not match the hash algorithm")
	}
	if len(opts.PolicyWhitelist) == 0 {
		return nil, errors.New("rfc3161: policy whitelist must not be empty")
	}
	policies := make(map[string]asn1.ObjectIdentifier, len(opts.PolicyWhitelist))
	for _, s := range opts.PolicyWhitelist {
		parsed, err := parseOID(s)
		if err != nil {
			return nil, errors.New("rfc3161: invalid policy OID in whitelist: " + s)
		}
		policies[parsed.String()] = parsed
	}
	return policies, nil
}

func parseOID(s string) (asn1.ObjectIdentifier, error) {
	var oid asn1.ObjectIdentifier
	parts := strings.Split(s, ".")
	for _, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 {
			return nil, errors.New("invalid OID arc: " + part)
		}
		oid = append(oid, value)
	}
	if len(oid) < 2 {
		return nil, errors.New("OID needs at least two arcs")
	}
	return oid, nil
}
