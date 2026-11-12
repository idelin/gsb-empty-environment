package netsec

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"math/big"
	"testing"
	"time"
)

type rootOfTrustSpec struct {
	verifiedBootKey   []byte
	deviceLocked      bool
	verifiedBootState VerifiedBootState
	verifiedBootHash  []byte
}

type authorizationSpec struct {
	purposes  []int
	algorithm *int
	origin    *int
	root      *rootOfTrustSpec
}

type keyDescriptionSpec struct {
	attestationSecurity SecurityLevel
	keymasterSecurity   SecurityLevel
	challenge           []byte
	software            authorizationSpec
	tee                 authorizationSpec
}

func TestValidateKeyAttestation(t *testing.T) {
	challenge := []byte("server-registration-challenge")
	algorithm := 3
	generated := int(OriginGenerated)
	imported := int(OriginImported)
	teeClaims := authorizationSpec{
		purposes:  []int{int(PurposeSign)},
		algorithm: &algorithm,
		origin:    &generated,
		root: &rootOfTrustSpec{
			verifiedBootKey:   []byte("verified-boot-key"),
			deviceLocked:      true,
			verifiedBootState: VerifiedBootVerified,
			verifiedBootHash:  []byte("boot-hash"),
		},
	}

	tests := []struct {
		name    string
		spec    keyDescriptionSpec
		wantErr error
	}{
		{
			name: "valid TEE authorization list",
			spec: keyDescriptionSpec{
				attestationSecurity: SecurityLevelTrustedEnvironment,
				keymasterSecurity:   SecurityLevelTrustedEnvironment,
				challenge:           challenge,
				tee:                 teeClaims,
			},
		},
		{
			name: "valid StrongBox authorization list",
			spec: keyDescriptionSpec{
				attestationSecurity: SecurityLevelStrongBox,
				keymasterSecurity:   SecurityLevelStrongBox,
				challenge:           challenge,
				tee:                 teeClaims,
			},
		},
		{
			name: "software attestation security level",
			spec: keyDescriptionSpec{
				attestationSecurity: SecurityLevelSoftware,
				keymasterSecurity:   SecurityLevelTrustedEnvironment,
				challenge:           challenge,
				tee:                 teeClaims,
			},
			wantErr: ErrUntrustedSecurityLevel,
		},
		{
			name: "software keymaster security level",
			spec: keyDescriptionSpec{
				attestationSecurity: SecurityLevelTrustedEnvironment,
				keymasterSecurity:   SecurityLevelSoftware,
				challenge:           challenge,
				tee:                 teeClaims,
			},
			wantErr: ErrUntrustedSecurityLevel,
		},
		{
			name: "claims moved outside teeEnforced",
			spec: keyDescriptionSpec{
				attestationSecurity: SecurityLevelTrustedEnvironment,
				keymasterSecurity:   SecurityLevelTrustedEnvironment,
				challenge:           challenge,
				software:            teeClaims,
			},
			wantErr: ErrSoftwareEnforcedAuthorization,
		},
		{
			name: "root of trust outside teeEnforced",
			spec: keyDescriptionSpec{
				attestationSecurity: SecurityLevelTrustedEnvironment,
				keymasterSecurity:   SecurityLevelTrustedEnvironment,
				challenge:           challenge,
				software: authorizationSpec{
					root: teeClaims.root,
				},
				tee: authorizationSpec{
					purposes:  teeClaims.purposes,
					algorithm: teeClaims.algorithm,
					origin:    teeClaims.origin,
				},
			},
			wantErr: ErrSoftwareEnforcedAuthorization,
		},
		{
			name: "challenge mismatch",
			spec: keyDescriptionSpec{
				attestationSecurity: SecurityLevelTrustedEnvironment,
				keymasterSecurity:   SecurityLevelTrustedEnvironment,
				challenge:           []byte("different-challenge"),
				tee:                 teeClaims,
			},
			wantErr: ErrChallengeMismatch,
		},
		{
			name: "imported origin",
			spec: keyDescriptionSpec{
				attestationSecurity: SecurityLevelTrustedEnvironment,
				keymasterSecurity:   SecurityLevelTrustedEnvironment,
				challenge:           challenge,
				tee: authorizationSpec{
					purposes:  teeClaims.purposes,
					algorithm: teeClaims.algorithm,
					origin:    &imported,
					root:      teeClaims.root,
				},
			},
			wantErr: ErrOriginNotGenerated,
		},
		{
			name: "unverified boot state",
			spec: keyDescriptionSpec{
				attestationSecurity: SecurityLevelTrustedEnvironment,
				keymasterSecurity:   SecurityLevelTrustedEnvironment,
				challenge:           challenge,
				tee: authorizationSpec{
					purposes:  teeClaims.purposes,
					algorithm: teeClaims.algorithm,
					origin:    teeClaims.origin,
					root: &rootOfTrustSpec{
						deviceLocked:      true,
						verifiedBootState: VerifiedBootUnverified,
					},
				},
			},
			wantErr: ErrRootOfTrustInvalid,
		},
		{
			name: "device is unlocked",
			spec: keyDescriptionSpec{
				attestationSecurity: SecurityLevelTrustedEnvironment,
				keymasterSecurity:   SecurityLevelTrustedEnvironment,
				challenge:           challenge,
				tee: authorizationSpec{
					purposes:  teeClaims.purposes,
					algorithm: teeClaims.algorithm,
					origin:    teeClaims.origin,
					root: &rootOfTrustSpec{
						deviceLocked:      false,
						verifiedBootState: VerifiedBootVerified,
					},
				},
			},
			wantErr: ErrRootOfTrustInvalid,
		},
		{
			name: "missing tee purpose",
			spec: keyDescriptionSpec{
				attestationSecurity: SecurityLevelTrustedEnvironment,
				keymasterSecurity:   SecurityLevelTrustedEnvironment,
				challenge:           challenge,
				tee: authorizationSpec{
					algorithm: teeClaims.algorithm,
					origin:    teeClaims.origin,
					root:      teeClaims.root,
				},
			},
			wantErr: ErrMissingTeeAuthorization,
		},
		{
			name: "missing tee algorithm",
			spec: keyDescriptionSpec{
				attestationSecurity: SecurityLevelTrustedEnvironment,
				keymasterSecurity:   SecurityLevelTrustedEnvironment,
				challenge:           challenge,
				tee: authorizationSpec{
					purposes: teeClaims.purposes,
					origin:   teeClaims.origin,
					root:     teeClaims.root,
				},
			},
			wantErr: ErrMissingTeeAuthorization,
		},
		{
			name: "missing tee origin",
			spec: keyDescriptionSpec{
				attestationSecurity: SecurityLevelTrustedEnvironment,
				keymasterSecurity:   SecurityLevelTrustedEnvironment,
				challenge:           challenge,
				tee: authorizationSpec{
					purposes:  teeClaims.purposes,
					algorithm: teeClaims.algorithm,
					root:      teeClaims.root,
				},
			},
			wantErr: ErrMissingTeeAuthorization,
		},
		{
			name: "missing tee root of trust",
			spec: keyDescriptionSpec{
				attestationSecurity: SecurityLevelTrustedEnvironment,
				keymasterSecurity:   SecurityLevelTrustedEnvironment,
				challenge:           challenge,
				tee: authorizationSpec{
					purposes:  teeClaims.purposes,
					algorithm: teeClaims.algorithm,
					origin:    teeClaims.origin,
				},
			},
			wantErr: ErrMissingTeeAuthorization,
		},
		{
			name: "empty purpose tag in softwareEnforced",
			spec: keyDescriptionSpec{
				attestationSecurity: SecurityLevelTrustedEnvironment,
				keymasterSecurity:   SecurityLevelTrustedEnvironment,
				challenge:           challenge,
				software: authorizationSpec{
					purposes: []int{},
				},
				tee: teeClaims,
			},
			wantErr: ErrSoftwareEnforcedAuthorization,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			certificate := certificateWithKeyDescription(t, marshalKeyDescription(test.spec))
			_, err := ValidateKeyAttestation(certificate, challenge)
			if test.wantErr == nil {
				if err != nil {
					t.Fatalf("ValidateKeyAttestation() error = %v", err)
				}
				return
			}
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("ValidateKeyAttestation() error = %v, want errors.Is(%v)", err, test.wantErr)
			}
		})
	}
}

func TestValidateKeyAttestationChallengeIsByteExact(t *testing.T) {
	certificate := certificateWithKeyDescription(t, marshalKeyDescription(validKeyDescriptionSpec([]byte("challenge"), SecurityLevelTrustedEnvironment)))
	if _, err := ValidateKeyAttestation(certificate, []byte("ChallengE")); !errors.Is(err, ErrChallengeMismatch) {
		t.Fatalf("ValidateKeyAttestation() error = %v, want %v", err, ErrChallengeMismatch)
	}
}

func TestParseKeyDescriptionRejectsMalformedDER(t *testing.T) {
	valid := marshalKeyDescription(validKeyDescriptionSpec([]byte("challenge"), SecurityLevelTrustedEnvironment))
	if _, err := ParseKeyDescription(append(valid, 0)); !errors.Is(err, ErrExtensionMalformed) {
		t.Fatalf("ParseKeyDescription() error = %v, want %v", err, ErrExtensionMalformed)
	}
	if _, err := ParseKeyDescription([]byte{0x30, 0x00}); !errors.Is(err, ErrExtensionMalformed) {
		t.Fatalf("ParseKeyDescription() error = %v, want %v", err, ErrExtensionMalformed)
	}
}

func TestParseCertificateKeyDescriptionMissingExtension(t *testing.T) {
	certificate := certificateWithExtensions(t, nil)
	if _, err := ParseCertificateKeyDescription(certificate); !errors.Is(err, ErrExtensionMissing) {
		t.Fatalf("ParseCertificateKeyDescription() error = %v, want %v", err, ErrExtensionMissing)
	}
}

func TestParseCertificateKeyDescriptionDuplicateExtension(t *testing.T) {
	description := marshalKeyDescription(validKeyDescriptionSpec([]byte("challenge"), SecurityLevelTrustedEnvironment))
	certificate := &x509.Certificate{Extensions: []pkix.Extension{
		{Id: KeyDescriptionOID, Value: description},
		{Id: KeyDescriptionOID, Value: description},
	}}
	if _, err := ParseCertificateKeyDescription(certificate); !errors.Is(err, ErrExtensionDuplicate) {
		t.Fatalf("ParseCertificateKeyDescription() error = %v, want %v", err, ErrExtensionDuplicate)
	}
}

func validKeyDescriptionSpec(challenge []byte, level SecurityLevel) keyDescriptionSpec {
	algorithm := 3
	generated := int(OriginGenerated)
	return keyDescriptionSpec{
		attestationSecurity: level,
		keymasterSecurity:   level,
		challenge:           challenge,
		tee: authorizationSpec{
			purposes:  []int{int(PurposeSign)},
			algorithm: &algorithm,
			origin:    &generated,
			root: &rootOfTrustSpec{
				verifiedBootKey:   []byte("verified-boot-key"),
				deviceLocked:      true,
				verifiedBootState: VerifiedBootVerified,
			},
		},
	}
}

func certificateWithKeyDescription(t *testing.T, description []byte) *x509.Certificate {
	t.Helper()
	return certificateWithExtensions(t, []pkix.Extension{{
		Id:    KeyDescriptionOID,
		Value: description,
	}})
}

func certificateWithExtensions(t *testing.T, extensions []pkix.Extension) *x509.Certificate {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ecdsa.GenerateKey() error = %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:    big.NewInt(1),
		Subject:         pkix.Name{CommonName: "attestation-test-leaf"},
		NotBefore:       time.Now().Add(-time.Hour),
		NotAfter:        time.Now().Add(time.Hour),
		KeyUsage:        x509.KeyUsageDigitalSignature,
		ExtraExtensions: extensions,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatalf("x509.CreateCertificate() error = %v", err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("x509.ParseCertificate() error = %v", err)
	}
	return certificate
}

func marshalKeyDescription(spec keyDescriptionSpec) []byte {
	var content []byte
	content = append(content, marshalInteger(3)...)
	content = append(content, marshalEnumerated(int(spec.attestationSecurity))...)
	content = append(content, marshalInteger(4)...)
	content = append(content, marshalEnumerated(int(spec.keymasterSecurity))...)
	content = append(content, marshalOctetString(spec.challenge)...)
	content = append(content, marshalOctetString(nil)...)
	content = append(content, marshalAuthorizationList(spec.software)...)
	content = append(content, marshalAuthorizationList(spec.tee)...)
	return wrapDER([]byte{0x30}, content)
}

func marshalAuthorizationList(spec authorizationSpec) []byte {
	var content []byte
	if spec.purposes != nil {
		var setContent []byte
		for _, purpose := range spec.purposes {
			setContent = append(setContent, marshalInteger(purpose)...)
		}
		content = append(content, wrapDER(contextTag(authorizationTagPurpose), wrapDER([]byte{0x31}, setContent))...)
	}
	if spec.algorithm != nil {
		content = append(content, wrapDER(contextTag(authorizationTagAlgorithm), marshalInteger(*spec.algorithm))...)
	}
	if spec.origin != nil {
		content = append(content, wrapDER(contextTag(authorizationTagOrigin), marshalInteger(*spec.origin))...)
	}
	if spec.root != nil {
		content = append(content, wrapDER(contextTag(authorizationTagRootOfTrust), marshalRootOfTrust(spec.root))...)
	}
	return wrapDER([]byte{0x30}, content)
}

func marshalRootOfTrust(spec *rootOfTrustSpec) []byte {
	var content []byte
	content = append(content, marshalOctetString(spec.verifiedBootKey)...)
	content = append(content, marshalBoolean(spec.deviceLocked)...)
	content = append(content, marshalEnumerated(int(spec.verifiedBootState))...)
	if spec.verifiedBootHash != nil {
		content = append(content, marshalOctetString(spec.verifiedBootHash)...)
	}
	return wrapDER([]byte{0x30}, content)
}

func marshalInteger(value int) []byte {
	der, err := asn1.Marshal(value)
	if err != nil {
		panic(err)
	}
	return der
}

func marshalEnumerated(value int) []byte {
	der, err := asn1.Marshal(asn1.Enumerated(value))
	if err != nil {
		panic(err)
	}
	return der
}

func marshalOctetString(value []byte) []byte {
	der, err := asn1.Marshal(value)
	if err != nil {
		panic(err)
	}
	return der
}

func marshalBoolean(value bool) []byte {
	der, err := asn1.Marshal(value)
	if err != nil {
		panic(err)
	}
	return der
}

func wrapDER(tag []byte, content []byte) []byte {
	result := append([]byte{}, tag...)
	result = append(result, derLength(len(content))...)
	result = append(result, content...)
	return result
}

func derLength(length int) []byte {
	if length < 0x80 {
		return []byte{byte(length)}
	}
	var value []byte
	for remaining := length; remaining > 0; remaining >>= 8 {
		value = append([]byte{byte(remaining)}, value...)
	}
	return append([]byte{0x80 | byte(len(value))}, value...)
}

func contextTag(tag int) []byte {
	if tag < 31 {
		return []byte{0xa0 | byte(tag)}
	}
	var encoded []byte
	for value := tag; value > 0; value >>= 7 {
		encoded = append([]byte{byte(value & 0x7f)}, encoded...)
	}
	for i := 0; i < len(encoded)-1; i++ {
		encoded[i] |= 0x80
	}
	return append([]byte{0xbf}, encoded...)
}

func TestContextTagEncoding(t *testing.T) {
	if !bytes.Equal(contextTag(702), []byte{0xbf, 0x85, 0x3e}) {
		t.Fatalf("contextTag(702) = %x", contextTag(702))
	}
	if !bytes.Equal(contextTag(704), []byte{0xbf, 0x85, 0x40}) {
		t.Fatalf("contextTag(704) = %x", contextTag(704))
	}
}
