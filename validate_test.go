package netsec

import (
	"bytes"
	"encoding/hex"
	"testing"
)

var testKAMF = bytesRepeatHex("00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff")

func bytesRepeatHex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

func registrationRequestBytes(registrationType byte, capabilities []byte) []byte {
	message := []byte{
		ExtendedProtocolDiscriminator5GMM,
		SecurityHeaderPlain,
		MessageTypeRegistrationRequest,
		registrationType,
		0x00,                   // ngKSI
		0x03, 0x01, 0x02, 0x03, // 5GS mobile identity, length 3
		0x10, 0x02, 0x03, 0x04, // an optional IE before the capability IE
		IEIUESecurityCapability, byte(len(capabilities)),
	}
	message = append(message, capabilities...)
	return message
}

func securityModeCommandBytes(t *testing.T, securityHeader byte, selectedAlgorithms byte, capabilities []byte, sequenceNumber byte, count uint32) []byte {
	t.Helper()

	plainNAS := []byte{
		ExtendedProtocolDiscriminator5GMM,
		SecurityHeaderPlain,
		MessageTypeSecurityModeCommand,
		selectedAlgorithms,
		0x00, // ngKSI
		IEIUESecurityCapability, byte(len(capabilities)),
	}
	plainNAS = append(plainNAS, capabilities...)

	raw := []byte{
		ExtendedProtocolDiscriminator5GMM,
		securityHeader,
		0x00, 0x00, 0x00, 0x00, // MAC placeholder
		sequenceNumber,
	}
	raw = append(raw, plainNAS...)

	integrityAlgorithm := selectedAlgorithms & 0x0f
	var mac []byte
	var err error
	if integrityAlgorithm == IntegrityAlgorithm5GIA0 {
		mac = []byte{0, 0, 0, 0}
	} else {
		var key []byte
		key, err = DeriveKNASint(testKAMF, integrityAlgorithm)
		if err == nil {
			mac, err = ComputeNASMAC(integrityAlgorithm, key, count, 0, directionDownlink, raw[6:])
		}
	}
	if err != nil {
		t.Fatalf("build MAC: %v", err)
	}
	copy(raw[2:6], mac)
	return raw
}

func requireRejected(t *testing.T, decision Decision, err error, cause byte) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
	if decision.Accepted {
		t.Fatal("Security Mode Command was accepted, want reject")
	}
	if decision.Cause != cause {
		t.Fatalf("cause = %d (%s), want %d", decision.Cause, decision.Reason, cause)
	}
	if want := EncodeSecurityModeReject(cause); !bytes.Equal(decision.RejectMessage, want) {
		t.Fatalf("reject message = %x, want %x", decision.RejectMessage, want)
	}
}

func TestNormalSecurityModeCommandAccepted(t *testing.T) {
	capabilities := []byte{0xf0, 0x70, 0xf0, 0x70}
	request := registrationRequestBytes(0x01, capabilities)
	command := securityModeCommandBytes(t, SecurityHeaderIntegrityProtectedNewContext, 0x02, capabilities, 0x00, 0)

	decision, err := ValidateSecurityModeCommand(command, request, ValidationOptions{KAMF: testKAMF})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if !decision.Accepted {
		t.Fatalf("command rejected: cause %d: %s", decision.Cause, decision.Reason)
	}
	if decision.RejectMessage != nil {
		t.Fatalf("accepted decision carried a reject message: %x", decision.RejectMessage)
	}
}

func TestEchoedCapabilityBitRemovedRejected(t *testing.T) {
	sentCapabilities := []byte{0xf0, 0x70, 0xf0, 0x70}
	// The network removes the 5G-IA2 bit from the replayed capabilities and
	// computes a correct MAC, so only the byte-for-byte comparison can catch
	// the modification.
	replayedCapabilities := []byte{0xf0, 0x50, 0xf0, 0x70}

	request := registrationRequestBytes(0x01, sentCapabilities)
	command := securityModeCommandBytes(t, SecurityHeaderIntegrityProtectedNewContext, 0x02, replayedCapabilities, 0x00, 0)

	decision, err := ValidateSecurityModeCommand(command, request, ValidationOptions{KAMF: testKAMF})
	requireRejected(t, decision, err, CauseUESecurityCapabilitiesMismatch)
}

func TestEchoedEPSBitRemovedRejected(t *testing.T) {
	sentCapabilities := []byte{0xf0, 0x70, 0xf0, 0x70}
	replayedCapabilities := []byte{0xf0, 0x70, 0xf0, 0x60}

	request := registrationRequestBytes(0x01, sentCapabilities)
	command := securityModeCommandBytes(t, SecurityHeaderIntegrityProtectedNewContext, 0x02, replayedCapabilities, 0x00, 0)

	decision, err := ValidateSecurityModeCommand(command, request, ValidationOptions{KAMF: testKAMF})
	requireRejected(t, decision, err, CauseUESecurityCapabilitiesMismatch)
}

func TestIA0RejectedForNormalRegistration(t *testing.T) {
	// IA0 is advertised, so the rejection is specifically the emergency-only
	// policy rather than an unsupported-algorithm check.
	capabilities := []byte{0xf0, 0xf0, 0xf0, 0x70}
	request := registrationRequestBytes(0x01, capabilities)
	command := securityModeCommandBytes(t, SecurityHeaderIntegrityProtectedNewContext, 0x00, capabilities, 0x00, 0)

	decision, err := ValidateSecurityModeCommand(command, request, ValidationOptions{KAMF: testKAMF})
	requireRejected(t, decision, err, CauseSecurityModeRejectedUnspecified)
}

func TestIA0AcceptedForEmergencyRegistration(t *testing.T) {
	capabilities := []byte{0xf0, 0xf0, 0xf0, 0x70}
	request := registrationRequestBytes(0x74, capabilities) // emergency registration + FOR
	command := securityModeCommandBytes(t, SecurityHeaderIntegrityProtectedNewContext, 0x00, capabilities, 0x00, 0)

	decision, err := ValidateSecurityModeCommand(command, request, ValidationOptions{KAMF: testKAMF})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if !decision.Accepted {
		t.Fatalf("emergency IA0 command rejected: cause %d: %s", decision.Cause, decision.Reason)
	}
}

func TestInvalidNASMACRejected(t *testing.T) {
	capabilities := []byte{0xf0, 0x70, 0xf0, 0x70}
	request := registrationRequestBytes(0x01, capabilities)
	command := securityModeCommandBytes(t, SecurityHeaderIntegrityProtectedNewContext, 0x02, capabilities, 0x00, 0)
	command[2] ^= 0x01

	decision, err := ValidateSecurityModeCommand(command, request, ValidationOptions{KAMF: testKAMF})
	requireRejected(t, decision, err, CauseSecurityModeRejectedUnspecified)
}

func TestCommandWithoutNewContextIntegrityRejected(t *testing.T) {
	capabilities := []byte{0xf0, 0x70, 0xf0, 0x70}
	request := registrationRequestBytes(0x01, capabilities)
	command := securityModeCommandBytes(t, SecurityHeaderIntegrityProtected, 0x02, capabilities, 0x00, 0)

	decision, err := ValidateSecurityModeCommand(command, request, ValidationOptions{KAMF: testKAMF})
	requireRejected(t, decision, err, CauseSecurityModeRejectedUnspecified)
}

func TestUnadvertisedNullCipheringRejected(t *testing.T) {
	// 5G-EA0 is not set in the actual capability bitmap, but the SMC selects
	// it. This is the null-ciphering downgrade path.
	capabilities := []byte{0x70, 0x70, 0xf0, 0x70}
	request := registrationRequestBytes(0x01, capabilities)
	command := securityModeCommandBytes(t, SecurityHeaderIntegrityProtectedNewContext, 0x02, capabilities, 0x00, 0)

	decision, err := ValidateSecurityModeCommand(command, request, ValidationOptions{KAMF: testKAMF})
	requireRejected(t, decision, err, CauseUESecurityCapabilitiesMismatch)
}

func TestParseRegistrationRequestExtractsEmergencyAndCapabilities(t *testing.T) {
	capabilities := []byte{0xf0, 0x70, 0xf0, 0x70}
	request, err := ParseRegistrationRequest(registrationRequestBytes(0x74, capabilities))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !request.Emergency {
		t.Fatal("emergency registration was not detected")
	}
	if !bytes.Equal(request.UESecurityCapabilities, capabilities) {
		t.Fatalf("capabilities = %x, want %x", request.UESecurityCapabilities, capabilities)
	}
}

func TestKDFUsesSelectedIntegrityAlgorithm(t *testing.T) {
	keyIA2, err := DeriveKNASint(testKAMF, IntegrityAlgorithm5GIA2)
	if err != nil {
		t.Fatalf("derive IA2 key: %v", err)
	}
	keyIA1, err := DeriveKNASint(testKAMF, IntegrityAlgorithm5GIA1)
	if err != nil {
		t.Fatalf("derive IA1 key: %v", err)
	}
	if len(keyIA2) != 16 {
		t.Fatalf("key length = %d, want 16", len(keyIA2))
	}
	if bytes.Equal(keyIA1, keyIA2) {
		t.Fatal("keys derived for different algorithms must differ")
	}
}
