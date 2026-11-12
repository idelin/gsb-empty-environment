package netsec

import (
	"bytes"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"fmt"
)

// KeyDescriptionOIDString is the object identifier of the Android key
// attestation KeyDescription extension.
const KeyDescriptionOIDString = "1.3.6.1.4.1.11129.2.1.17"

// KeyDescriptionOID is the parsed form of KeyDescriptionOIDString.
var KeyDescriptionOID = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 11129, 2, 1, 17}

const (
	authorizationTagPurpose     = 1
	authorizationTagAlgorithm   = 2
	authorizationTagOrigin      = 702
	authorizationTagRootOfTrust = 704
)

var (
	ErrExtensionMissing              = errors.New("key attestation extension is missing")
	ErrExtensionMalformed            = errors.New("key attestation extension is malformed")
	ErrExtensionDuplicate            = errors.New("key attestation extension is duplicated")
	ErrChallengeMismatch             = errors.New("attestation challenge does not match")
	ErrUntrustedSecurityLevel        = errors.New("attestation security level is not hardware-backed")
	ErrSoftwareEnforcedAuthorization = errors.New("authorization is software-enforced")
	ErrMissingTeeAuthorization       = errors.New("required authorization is missing from teeEnforced")
	ErrOriginNotGenerated            = errors.New("key origin is not GENERATED")
	ErrRootOfTrustInvalid            = errors.New("root of trust is not trusted")
)

// SecurityLevel identifies where key operations are enforced.
type SecurityLevel int

const (
	SecurityLevelSoftware           SecurityLevel = 0
	SecurityLevelTrustedEnvironment SecurityLevel = 1
	SecurityLevelStrongBox          SecurityLevel = 2
)

// IsHardwareBacked reports whether the security level is backed by a TEE or
// StrongBox.
func (level SecurityLevel) IsHardwareBacked() bool {
	return level == SecurityLevelTrustedEnvironment || level == SecurityLevelStrongBox
}

// Purpose is an Android key purpose from an AuthorizationList.
type Purpose int

const (
	PurposeEncrypt   Purpose = 0
	PurposeDecrypt   Purpose = 1
	PurposeSign      Purpose = 2
	PurposeVerify    Purpose = 3
	PurposeDerive    Purpose = 4
	PurposeWrap      Purpose = 5
	PurposeAgreeKey  Purpose = 6
	PurposeAttestKey Purpose = 7
)

// Origin identifies how a key came into existence.
type Origin int

const (
	OriginGenerated Origin = 0
	OriginImported  Origin = 1
)

// VerifiedBootState is the verified boot state reported in RootOfTrust.
type VerifiedBootState int

const (
	VerifiedBootVerified   VerifiedBootState = 0
	VerifiedBootSelfSigned VerifiedBootState = 1
	VerifiedBootUnverified VerifiedBootState = 2
	VerifiedBootFailed     VerifiedBootState = 3
)

// AuthorizationList is the subset of an Android AuthorizationList needed by
// device registration. Pointer and slice fields preserve the presence of
// optional ASN.1 fields.
type AuthorizationList struct {
	PurposePresent bool
	Purposes       []Purpose
	Algorithm      *int
	Origin         *Origin
	RootOfTrust    *RootOfTrust
}

// RootOfTrust describes the verified boot state protected by key attestation.
type RootOfTrust struct {
	VerifiedBootKey   []byte
	DeviceLocked      bool
	VerifiedBootState VerifiedBootState
	VerifiedBootHash  []byte
}

// KeyDescription is the decoded Android key attestation extension.
type KeyDescription struct {
	AttestationVersion       int
	AttestationSecurityLevel SecurityLevel
	KeymasterVersion         int
	KeymasterSecurityLevel   SecurityLevel
	AttestationChallenge     []byte
	UniqueID                 []byte
	SoftwareEnforced         AuthorizationList
	TEEEnforced              AuthorizationList
}

// ParseCertificateKeyDescription parses the KeyDescription extension from a
// leaf certificate. Certificate-chain validation must be performed by the
// caller before using this function.
func ParseCertificateKeyDescription(cert *x509.Certificate) (*KeyDescription, error) {
	if cert == nil {
		return nil, fmt.Errorf("%w: certificate is nil", ErrExtensionMissing)
	}

	var extension *pkix.Extension
	for i := range cert.Extensions {
		current := &cert.Extensions[i]
		if !current.Id.Equal(KeyDescriptionOID) {
			continue
		}
		if extension != nil {
			return nil, ErrExtensionDuplicate
		}
		extension = current
	}
	if extension == nil {
		return nil, ErrExtensionMissing
	}

	return ParseKeyDescription(extension.Value)
}

// ParseKeyDescription parses the DER value of an Android KeyDescription
// extension. der must contain the extension value, not the enclosing X.509
// extension OCTET STRING.
func ParseKeyDescription(der []byte) (*KeyDescription, error) {
	sequence, err := parseOneDER(der, "KeyDescription")
	if err != nil {
		return nil, err
	}
	if sequence.Class != asn1.ClassUniversal || sequence.Tag != asn1.TagSequence || !sequence.IsCompound {
		return nil, malformed("KeyDescription is not a SEQUENCE")
	}

	fields, err := sequenceElements(sequence, "KeyDescription", 8)
	if err != nil {
		return nil, err
	}
	if len(fields) != 8 {
		return nil, malformed("KeyDescription has %d fields, want 8", len(fields))
	}

	attestationVersion, err := decodeInteger(fields[0], "attestationVersion")
	if err != nil {
		return nil, err
	}
	attestationSecurityLevel, err := decodeEnumerated(fields[1], "attestationSecurityLevel")
	if err != nil {
		return nil, err
	}
	keymasterVersion, err := decodeInteger(fields[2], "keymasterVersion")
	if err != nil {
		return nil, err
	}
	keymasterSecurityLevel, err := decodeEnumerated(fields[3], "keymasterSecurityLevel")
	if err != nil {
		return nil, err
	}
	attestationChallenge, err := decodeOctetString(fields[4], "attestationChallenge")
	if err != nil {
		return nil, err
	}
	uniqueID, err := decodeOctetString(fields[5], "uniqueId")
	if err != nil {
		return nil, err
	}
	softwareEnforced, err := parseAuthorizationList(fields[6], "softwareEnforced")
	if err != nil {
		return nil, err
	}
	teeEnforced, err := parseAuthorizationList(fields[7], "teeEnforced")
	if err != nil {
		return nil, err
	}

	return &KeyDescription{
		AttestationVersion:       attestationVersion,
		AttestationSecurityLevel: SecurityLevel(attestationSecurityLevel),
		KeymasterVersion:         keymasterVersion,
		KeymasterSecurityLevel:   SecurityLevel(keymasterSecurityLevel),
		AttestationChallenge:     attestationChallenge,
		UniqueID:                 uniqueID,
		SoftwareEnforced:         softwareEnforced,
		TEEEnforced:              teeEnforced,
	}, nil
}

// Validate checks the security properties required for device registration.
// The caller must have already verified the certificate chain and must pass
// the challenge that was issued for this registration attempt.
func (description *KeyDescription) Validate(challenge []byte) error {
	if description == nil {
		return malformed("KeyDescription is nil")
	}
	if !bytes.Equal(description.AttestationChallenge, challenge) {
		return ErrChallengeMismatch
	}
	if !description.AttestationSecurityLevel.IsHardwareBacked() {
		return fmt.Errorf("%w: attestationSecurityLevel=%d", ErrUntrustedSecurityLevel, description.AttestationSecurityLevel)
	}
	if !description.KeymasterSecurityLevel.IsHardwareBacked() {
		return fmt.Errorf("%w: keymasterSecurityLevel=%d", ErrUntrustedSecurityLevel, description.KeymasterSecurityLevel)
	}

	software := description.SoftwareEnforced
	if software.PurposePresent || software.Algorithm != nil || software.Origin != nil || software.RootOfTrust != nil {
		return fmt.Errorf("%w: purpose, algorithm, origin, or rootOfTrust", ErrSoftwareEnforcedAuthorization)
	}

	tee := description.TEEEnforced
	if !tee.PurposePresent || len(tee.Purposes) == 0 {
		return fmt.Errorf("%w: purpose", ErrMissingTeeAuthorization)
	}
	if tee.Algorithm == nil {
		return fmt.Errorf("%w: algorithm", ErrMissingTeeAuthorization)
	}
	if tee.Origin == nil {
		return fmt.Errorf("%w: origin", ErrMissingTeeAuthorization)
	}
	if *tee.Origin != OriginGenerated {
		return fmt.Errorf("%w: origin=%d", ErrOriginNotGenerated, *tee.Origin)
	}
	if tee.RootOfTrust == nil {
		return fmt.Errorf("%w: rootOfTrust", ErrMissingTeeAuthorization)
	}
	if tee.RootOfTrust.VerifiedBootState != VerifiedBootVerified {
		return fmt.Errorf("%w: verifiedBootState=%d", ErrRootOfTrustInvalid, tee.RootOfTrust.VerifiedBootState)
	}
	if !tee.RootOfTrust.DeviceLocked {
		return fmt.Errorf("%w: deviceLocked=false", ErrRootOfTrustInvalid)
	}

	return nil
}

// ValidateKeyAttestation parses and validates the leaf certificate's Android
// key attestation extension. Certificate-chain validation is intentionally
// left to the caller.
func ValidateKeyAttestation(cert *x509.Certificate, challenge []byte) (*KeyDescription, error) {
	description, err := ParseCertificateKeyDescription(cert)
	if err != nil {
		return nil, err
	}
	if err := description.Validate(challenge); err != nil {
		return nil, err
	}
	return description, nil
}

func parseAuthorizationList(sequence asn1.RawValue, name string) (AuthorizationList, error) {
	if sequence.Class != asn1.ClassUniversal || sequence.Tag != asn1.TagSequence || !sequence.IsCompound {
		return AuthorizationList{}, malformed("%s is not a SEQUENCE", name)
	}

	var result AuthorizationList
	seenTags := make(map[int]struct{})
	remaining := sequence.Bytes
	for len(remaining) != 0 {
		var field asn1.RawValue
		rest, err := asn1.Unmarshal(remaining, &field)
		if err != nil {
			return AuthorizationList{}, malformed("cannot decode %s field: %v", name, err)
		}
		remaining = rest

		if field.Class != asn1.ClassContextSpecific {
			return AuthorizationList{}, malformed("%s contains a non-context-specific field", name)
		}
		if _, exists := seenTags[field.Tag]; exists {
			return AuthorizationList{}, malformed("%s contains duplicate tag %d", name, field.Tag)
		}
		seenTags[field.Tag] = struct{}{}

		switch field.Tag {
		case authorizationTagPurpose:
			if !field.IsCompound {
				return AuthorizationList{}, malformed("%s purpose tag is not EXPLICIT", name)
			}
			purposes, err := parseSetOfIntegers(field.Bytes, name+".purpose")
			if err != nil {
				return AuthorizationList{}, err
			}
			result.PurposePresent = true
			result.Purposes = purposes

		case authorizationTagAlgorithm:
			if !field.IsCompound {
				return AuthorizationList{}, malformed("%s algorithm tag is not EXPLICIT", name)
			}
			algorithm, err := decodeExplicitInteger(field.Bytes, name+".algorithm")
			if err != nil {
				return AuthorizationList{}, err
			}
			result.Algorithm = &algorithm

		case authorizationTagOrigin:
			if !field.IsCompound {
				return AuthorizationList{}, malformed("%s origin tag is not EXPLICIT", name)
			}
			origin, err := decodeExplicitInteger(field.Bytes, name+".origin")
			if err != nil {
				return AuthorizationList{}, err
			}
			value := Origin(origin)
			result.Origin = &value

		case authorizationTagRootOfTrust:
			if !field.IsCompound {
				return AuthorizationList{}, malformed("%s rootOfTrust tag is not EXPLICIT", name)
			}
			rootOfTrust, err := parseRootOfTrust(field.Bytes, name+".rootOfTrust")
			if err != nil {
				return AuthorizationList{}, err
			}
			result.RootOfTrust = rootOfTrust
		}
	}

	return result, nil
}

func parseSetOfIntegers(der []byte, name string) ([]Purpose, error) {
	set, err := parseOneDER(der, name)
	if err != nil {
		return nil, err
	}
	if set.Class != asn1.ClassUniversal || set.Tag != asn1.TagSet || !set.IsCompound {
		return nil, malformed("%s is not a SET OF INTEGER", name)
	}

	var purposes []Purpose
	remaining := set.Bytes
	for len(remaining) != 0 {
		var item asn1.RawValue
		rest, err := asn1.Unmarshal(remaining, &item)
		if err != nil {
			return nil, malformed("cannot decode %s value: %v", name, err)
		}
		remaining = rest
		value, err := decodeInteger(item, name)
		if err != nil {
			return nil, err
		}
		purposes = append(purposes, Purpose(value))
	}

	return purposes, nil
}

func parseRootOfTrust(der []byte, name string) (*RootOfTrust, error) {
	sequence, err := parseOneDER(der, name)
	if err != nil {
		return nil, err
	}
	if sequence.Class != asn1.ClassUniversal || sequence.Tag != asn1.TagSequence || !sequence.IsCompound {
		return nil, malformed("%s is not a SEQUENCE", name)
	}

	fields, err := sequenceElements(sequence, name, 4)
	if err != nil {
		return nil, err
	}
	if len(fields) < 3 {
		return nil, malformed("%s has only %d fields", name, len(fields))
	}

	verifiedBootKey, err := decodeOctetString(fields[0], name+".verifiedBootKey")
	if err != nil {
		return nil, err
	}
	deviceLocked, err := decodeBoolean(fields[1], name+".deviceLocked")
	if err != nil {
		return nil, err
	}
	verifiedBootState, err := decodeEnumerated(fields[2], name+".verifiedBootState")
	if err != nil {
		return nil, err
	}

	var verifiedBootHash []byte
	if len(fields) == 4 {
		verifiedBootHash, err = decodeOctetString(fields[3], name+".verifiedBootHash")
		if err != nil {
			return nil, err
		}
	}

	return &RootOfTrust{
		VerifiedBootKey:   verifiedBootKey,
		DeviceLocked:      deviceLocked,
		VerifiedBootState: VerifiedBootState(verifiedBootState),
		VerifiedBootHash:  verifiedBootHash,
	}, nil
}

func sequenceElements(sequence asn1.RawValue, name string, maximum int) ([]asn1.RawValue, error) {
	var elements []asn1.RawValue
	remaining := sequence.Bytes
	for len(remaining) != 0 {
		var element asn1.RawValue
		rest, err := asn1.Unmarshal(remaining, &element)
		if err != nil {
			return nil, malformed("cannot decode %s field: %v", name, err)
		}
		remaining = rest
		elements = append(elements, element)
		if len(elements) > maximum {
			return nil, malformed("%s has more than %d fields", name, maximum)
		}
	}
	return elements, nil
}

func parseOneDER(der []byte, name string) (asn1.RawValue, error) {
	var value asn1.RawValue
	rest, err := asn1.Unmarshal(der, &value)
	if err != nil {
		return asn1.RawValue{}, malformed("cannot decode %s: %v", name, err)
	}
	if len(rest) != 0 {
		return asn1.RawValue{}, malformed("%s has trailing data", name)
	}
	return value, nil
}

func decodeExplicitInteger(der []byte, name string) (int, error) {
	value, err := parseOneDER(der, name)
	if err != nil {
		return 0, err
	}
	return decodeInteger(value, name)
}

func decodeInteger(value asn1.RawValue, name string) (int, error) {
	if value.Class != asn1.ClassUniversal || value.Tag != asn1.TagInteger || value.IsCompound {
		return 0, malformed("%s is not an INTEGER", name)
	}
	var result int
	if _, err := asn1.Unmarshal(value.FullBytes, &result); err != nil {
		return 0, malformed("cannot decode %s: %v", name, err)
	}
	return result, nil
}

func decodeEnumerated(value asn1.RawValue, name string) (int, error) {
	if value.Class != asn1.ClassUniversal || value.Tag != asn1.TagEnum || value.IsCompound {
		return 0, malformed("%s is not an ENUMERATED", name)
	}
	var result asn1.Enumerated
	if _, err := asn1.Unmarshal(value.FullBytes, &result); err != nil {
		return 0, malformed("cannot decode %s: %v", name, err)
	}
	return int(result), nil
}

func decodeOctetString(value asn1.RawValue, name string) ([]byte, error) {
	if value.Class != asn1.ClassUniversal || value.Tag != asn1.TagOctetString || value.IsCompound {
		return nil, malformed("%s is not an OCTET STRING", name)
	}
	var result []byte
	if _, err := asn1.Unmarshal(value.FullBytes, &result); err != nil {
		return nil, malformed("cannot decode %s: %v", name, err)
	}
	return result, nil
}

func decodeBoolean(value asn1.RawValue, name string) (bool, error) {
	if value.Class != asn1.ClassUniversal || value.Tag != asn1.TagBoolean || value.IsCompound {
		return false, malformed("%s is not a BOOLEAN", name)
	}
	var result bool
	if _, err := asn1.Unmarshal(value.FullBytes, &result); err != nil {
		return false, malformed("cannot decode %s: %v", name, err)
	}
	return result, nil
}

func malformed(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrExtensionMalformed, fmt.Sprintf(format, args...))
}
