package netsec

import (
	"crypto"
	"crypto/dsa"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/x509"
	"encoding/asn1"
	"errors"
	"fmt"
	"math/big"
	"time"
)

var (
	OIDRSAEncryption   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 1}
	OIDRSASSAPSS       = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 10}
	OIDSHA1WithRSA     = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 5}
	OIDSHA224WithRSA   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 14}
	OIDSHA256WithRSA   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 11}
	OIDSHA384WithRSA   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 12}
	OIDSHA512WithRSA   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 13}
	OIDECDSAWithSHA1   = asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 1}
	OIDECDSAWithSHA224 = asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 3, 1}
	OIDECDSAWithSHA256 = asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 3, 2}
	OIDECDSAWithSHA384 = asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 3, 3}
	OIDECDSAWithSHA512 = asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 3, 4}
	OIDEd25519         = asn1.ObjectIdentifier{1, 3, 101, 112}
	OIDDSAWithSHA1     = asn1.ObjectIdentifier{1, 2, 840, 10040, 4, 3}
	OIDDSAWithSHA256   = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 3, 2}
	OIDMGF1            = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 8}
)

func signatureAlgorithmName(oid asn1.ObjectIdentifier) string {
	switch {
	case equalOID(oid, OIDRSAEncryption):
		return "RSA"
	case equalOID(oid, OIDRSASSAPSS):
		return "RSA-PSS"
	case equalOID(oid, OIDSHA1WithRSA):
		return "SHA1-RSA"
	case equalOID(oid, OIDSHA224WithRSA):
		return "SHA224-RSA"
	case equalOID(oid, OIDSHA256WithRSA):
		return "SHA256-RSA"
	case equalOID(oid, OIDSHA384WithRSA):
		return "SHA384-RSA"
	case equalOID(oid, OIDSHA512WithRSA):
		return "SHA512-RSA"
	case equalOID(oid, OIDECDSAWithSHA1):
		return "ECDSA-SHA1"
	case equalOID(oid, OIDECDSAWithSHA224):
		return "ECDSA-SHA224"
	case equalOID(oid, OIDECDSAWithSHA256):
		return "ECDSA-SHA256"
	case equalOID(oid, OIDECDSAWithSHA384):
		return "ECDSA-SHA384"
	case equalOID(oid, OIDECDSAWithSHA512):
		return "ECDSA-SHA512"
	case equalOID(oid, OIDEd25519):
		return "Ed25519"
	case equalOID(oid, OIDDSAWithSHA1):
		return "DSA-SHA1"
	case equalOID(oid, OIDDSAWithSHA256):
		return "DSA-SHA256"
	default:
		return ""
	}
}

func signatureHashForOID(oid asn1.ObjectIdentifier) (crypto.Hash, bool) {
	switch {
	case equalOID(oid, OIDSHA1WithRSA), equalOID(oid, OIDECDSAWithSHA1), equalOID(oid, OIDDSAWithSHA1):
		return crypto.SHA1, true
	case equalOID(oid, OIDSHA224WithRSA), equalOID(oid, OIDECDSAWithSHA224):
		return crypto.SHA224, true
	case equalOID(oid, OIDSHA256WithRSA), equalOID(oid, OIDECDSAWithSHA256), equalOID(oid, OIDDSAWithSHA256):
		return crypto.SHA256, true
	case equalOID(oid, OIDSHA384WithRSA), equalOID(oid, OIDECDSAWithSHA384):
		return crypto.SHA384, true
	case equalOID(oid, OIDSHA512WithRSA), equalOID(oid, OIDECDSAWithSHA512):
		return crypto.SHA512, true
	default:
		return 0, false
	}
}

func verifyCMSSigner(signedData cmsSignedData, info *cmsSignerInfo, signer *TokenSigner) error {
	if signer.Certificate == nil {
		return errors.New("signer certificate is not present")
	}
	digestHash, _, err := hashForOID(info.DigestAlgorithm.OID)
	if err != nil {
		return err
	}
	if !signedDataHasDigestAlgorithm(signedData.DigestAlgorithms, info.DigestAlgorithm.OID) {
		return errors.New("SignerInfo digest algorithm is absent from SignedData")
	}
	if len(info.RawSignedAttributes.Bytes) == 0 {
		return errors.New("timestamp signer has no signed attributes")
	}

	messageDigest, contentType, signingTime, err := extractSignedAttributes(info.SignedAttributes, digestHash)
	if err != nil {
		return err
	}
	if !equalOID(contentType, OIDContentTypeTSTInfo) {
		return errorf(ErrUnsupportedContentType, "signed content-type %s is not TSTInfo", contentType.String())
	}
	contentDigest, err := digestBytes(digestHash, signedData.EncapContent.Content)
	if err != nil {
		return err
	}
	if !equalBytes(contentDigest, messageDigest) {
		return errors.New("signed messageDigest does not match TSTInfo")
	}

	signedBytes, err := signedAttributesDER(info.RawSignedAttributes)
	if err != nil {
		return err
	}
	if err := verifySignatureValue(signer.Certificate, info.SignatureAlgorithm, info.DigestAlgorithm, signedBytes, info.Signature); err != nil {
		return err
	}
	signer.SigningTime = signingTime
	return nil
}

func extractSignedAttributes(attributes []Attribute, digestHash crypto.Hash) ([]byte, asn1.ObjectIdentifier, time.Time, error) {
	var messageDigest []byte
	var contentType asn1.ObjectIdentifier
	var signingTime time.Time
	var digestSeen, typeSeen, timeSeen bool
	for _, attribute := range attributes {
		switch {
		case equalOID(attribute.Type, OIDMessageDigest):
			if digestSeen || len(attribute.Values) != 1 {
				return nil, nil, time.Time{}, errors.New("invalid messageDigest signed attribute")
			}
			value, err := parseOctetString(attribute.Values[0])
			if err != nil {
				return nil, nil, time.Time{}, err
			}
			if len(value) != digestHash.Size() {
				return nil, nil, time.Time{}, fmt.Errorf("messageDigest length %d does not match %s", len(value), digestHash)
			}
			messageDigest = value
			digestSeen = true
		case equalOID(attribute.Type, OIDContentType):
			if typeSeen || len(attribute.Values) != 1 {
				return nil, nil, time.Time{}, errors.New("invalid content-type signed attribute")
			}
			value, err := parseOID(attribute.Values[0])
			if err != nil {
				return nil, nil, time.Time{}, err
			}
			contentType = value
			typeSeen = true
		case equalOID(attribute.Type, OIDSigningTime):
			if timeSeen || len(attribute.Values) != 1 {
				return nil, nil, time.Time{}, errors.New("invalid signing-time signed attribute")
			}
			var parsed time.Time
			rest, err := asn1.Unmarshal(attribute.Values[0].FullBytes, &parsed)
			if err != nil || len(rest) != 0 {
				return nil, nil, time.Time{}, errors.New("invalid signing-time value")
			}
			signingTime = parsed
			timeSeen = true
		}
	}
	if !digestSeen {
		return nil, nil, time.Time{}, errors.New("signed attributes have no messageDigest")
	}
	if !typeSeen {
		return nil, nil, time.Time{}, errors.New("signed attributes have no content-type")
	}
	return messageDigest, contentType, signingTime, nil
}

func signedDataHasDigestAlgorithm(algorithms []AlgorithmIdentifier, target asn1.ObjectIdentifier) bool {
	for _, algorithm := range algorithms {
		if equalOID(algorithm.OID, target) {
			return true
		}
	}
	return false
}

func verifySignatureValue(certificate *x509.Certificate, signatureAlgorithm, digestAlgorithm AlgorithmIdentifier, signed []byte, signature []byte) error {
	digestHash, _, err := hashForOID(digestAlgorithm.OID)
	if err != nil {
		return err
	}
	if algorithmHash, ok := signatureHashForOID(signatureAlgorithm.OID); ok && algorithmHash != digestHash {
		return fmt.Errorf("signature algorithm hash %v does not match digest algorithm %v", algorithmHash, digestHash)
	}

	switch publicKey := certificate.PublicKey.(type) {
	case *rsa.PublicKey:
		return verifyRSASignature(publicKey, signatureAlgorithm, digestHash, signed, signature)
	case *ecdsa.PublicKey:
		hash, err := signatureHashOrDigest(signatureAlgorithm.OID, digestHash)
		if err != nil {
			return err
		}
		digest, err := digestBytes(hash, signed)
		if err != nil {
			return err
		}
		if !ecdsa.VerifyASN1(publicKey, digest, signature) {
			return errors.New("ECDSA signature verification failed")
		}
		return nil
	case ed25519.PublicKey:
		if !equalOID(signatureAlgorithm.OID, OIDEd25519) {
			return errors.New("signature algorithm is not Ed25519")
		}
		if !ed25519.Verify(publicKey, signed, signature) {
			return errors.New("Ed25519 signature verification failed")
		}
		return nil
	case *dsa.PublicKey:
		hash, err := signatureHashOrDigest(signatureAlgorithm.OID, digestHash)
		if err != nil {
			return err
		}
		digest, err := digestBytes(hash, signed)
		if err != nil {
			return err
		}
		var dsaSignature struct {
			R *big.Int
			S *big.Int
		}
		rest, err := asn1.Unmarshal(signature, &dsaSignature)
		if err != nil || len(rest) != 0 || dsaSignature.R == nil || dsaSignature.S == nil {
			return errors.New("invalid DSA signature encoding")
		}
		if !dsa.Verify(publicKey, digest, dsaSignature.R, dsaSignature.S) {
			return errors.New("DSA signature verification failed")
		}
		return nil
	default:
		return fmt.Errorf("unsupported timestamp public key type %T", certificate.PublicKey)
	}
}

func signatureHashOrDigest(oid asn1.ObjectIdentifier, fallback crypto.Hash) (crypto.Hash, error) {
	if hash, ok := signatureHashForOID(oid); ok {
		return hash, nil
	}
	if equalOID(oid, OIDRSAEncryption) || equalOID(oid, OIDRSASSAPSS) {
		return fallback, nil
	}
	return 0, fmt.Errorf("unsupported signature algorithm %s", oid.String())
}

func verifyRSASignature(publicKey *rsa.PublicKey, signatureAlgorithm AlgorithmIdentifier, digestHash crypto.Hash, signed, signature []byte) error {
	switch {
	case equalOID(signatureAlgorithm.OID, OIDRSASSAPSS):
		parameters, err := parsePSSParameters(signatureAlgorithm.Parameters, digestHash)
		if err != nil {
			return err
		}
		digest, err := digestBytes(parameters.Hash, signed)
		if err != nil {
			return err
		}
		if err := rsa.VerifyPSS(publicKey, parameters.Hash, digest, signature, &rsa.PSSOptions{
			SaltLength: parameters.SaltLength,
			Hash:       parameters.Hash,
		}); err != nil {
			return fmt.Errorf("RSA-PSS signature verification failed: %w", err)
		}
		return nil
	case equalOID(signatureAlgorithm.OID, OIDRSAEncryption):
		digest, err := digestBytes(digestHash, signed)
		if err != nil {
			return err
		}
		if err := rsa.VerifyPKCS1v15(publicKey, digestHash, digest, signature); err != nil {
			return fmt.Errorf("RSA signature verification failed: %w", err)
		}
		return nil
	default:
		hash, err := signatureHashOrDigest(signatureAlgorithm.OID, digestHash)
		if err != nil {
			return err
		}
		if hash != digestHash {
			return fmt.Errorf("signature algorithm hash %v does not match digest algorithm %v", hash, digestHash)
		}
		digest, err := digestBytes(hash, signed)
		if err != nil {
			return err
		}
		if err := rsa.VerifyPKCS1v15(publicKey, hash, digest, signature); err != nil {
			return fmt.Errorf("RSA signature verification failed: %w", err)
		}
		return nil
	}
}

type pssParameters struct {
	Hash         crypto.Hash
	SaltLength   int
	TrailerField int
}

type pssASN1 struct {
	Hash         AlgorithmIdentifier
	MaskGen      AlgorithmIdentifier
	SaltLength   int
	TrailerField int `asn1:"optional,default:1"`
}

func parsePSSParameters(value asn1.RawValue, fallback crypto.Hash) (pssParameters, error) {
	hash := fallback
	saltLength := rsa.PSSSaltLengthAuto
	trailerField := 1
	if len(value.FullBytes) > 0 {
		var parameters pssASN1
		rest, err := asn1.Unmarshal(value.FullBytes, &parameters)
		if err != nil || len(rest) != 0 {
			return pssParameters{}, errors.New("invalid RSA-PSS parameters")
		}
		parsedHash, _, err := hashForOID(parameters.Hash.OID)
		if err != nil {
			return pssParameters{}, err
		}
		if !equalOID(parameters.MaskGen.OID, OIDMGF1) {
			return pssParameters{}, errors.New("unsupported RSA-PSS mask generation function")
		}
		hash = parsedHash
		saltLength = parameters.SaltLength
		trailerField = parameters.TrailerField
	}
	if trailerField != 1 {
		return pssParameters{}, errors.New("unsupported RSA-PSS trailer field")
	}
	if hash != fallback {
		return pssParameters{}, errors.New("RSA-PSS hash does not match digest algorithm")
	}
	return pssParameters{Hash: hash, SaltLength: saltLength, TrailerField: trailerField}, nil
}
