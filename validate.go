package netsec

import (
	"crypto/subtle"
	"fmt"
)

const (
	// CauseUESecurityCapabilitiesMismatch is 5GMM cause #23.
	CauseUESecurityCapabilitiesMismatch byte = 23
	// CauseSecurityModeRejectedUnspecified is 5GMM cause #24.
	CauseSecurityModeRejectedUnspecified byte = 24
	// CauseProtocolErrorUnspecified is 5GMM cause #111.
	CauseProtocolErrorUnspecified byte = 111

	directionDownlink byte = 1
	nasMACLength      int  = 4
)

// NASMACFunc computes a NAS MAC. The default implementation supports 5G-IA0
// and 5G-IA2. Callers that need 5G-IA1 or 5G-IA3 can provide an implementation
// through ValidationOptions.
type NASMACFunc func(algorithm byte, key []byte, count uint32, bearer, direction byte, message []byte) ([]byte, error)

// ValidationOptions contains the security context used to validate the
// command. Count is the downlink NAS count; it is zero for the first
// Security Mode Command in a new security context. Bearer is the NAS
// connection identifier: 0 for 3GPP access and 1 for non-3GPP access.
type ValidationOptions struct {
	KAMF    []byte
	Count   uint32
	Bearer  byte
	MACFunc NASMACFunc
}

// Decision is the result of validating a Security Mode Command.
type Decision struct {
	Accepted bool
	Cause    byte
	Reason   string

	// RejectMessage is a complete plain 5GS NAS Security Mode Reject when
	// Accepted is false, and nil otherwise.
	RejectMessage []byte
}

// EncodeSecurityModeReject encodes a plain 5GS NAS Security Mode Reject with
// the given 5GMM cause.
func EncodeSecurityModeReject(cause byte) []byte {
	if cause == 0 {
		cause = CauseSecurityModeRejectedUnspecified
	}
	return []byte{
		ExtendedProtocolDiscriminator5GMM,
		SecurityHeaderPlain,
		MessageTypeSecurityModeReject,
		cause,
	}
}

func rejectDecision(cause byte, reason string) Decision {
	return Decision{
		Accepted:      false,
		Cause:         cause,
		Reason:        reason,
		RejectMessage: EncodeSecurityModeReject(cause),
	}
}

// ValidateSecurityModeCommand parses a raw Registration Request and a raw
// Security Mode Command, then validates the command against the capabilities
// that the UE actually sent.
//
// Malformed NAS input produces a Security Mode Reject decision rather than an
// error, because a UE must still respond to the malformed command. A
// configuration error, such as an invalid KAMF length, is returned as an
// error.
func ValidateSecurityModeCommand(smcRaw, registrationRequestRaw []byte, options ValidationOptions) (Decision, error) {
	command, err := ParseSecurityModeCommand(smcRaw)
	if err != nil {
		return rejectDecision(CauseProtocolErrorUnspecified, err.Error()), nil
	}
	request, err := ParseRegistrationRequest(registrationRequestRaw)
	if err != nil {
		return rejectDecision(CauseProtocolErrorUnspecified, err.Error()), nil
	}
	return ValidateParsedSecurityModeCommand(command, request, options)
}

// ValidateParsedSecurityModeCommand validates an already decoded command
// against the decoded Registration Request security state.
func ValidateParsedSecurityModeCommand(command *SecurityModeCommand, request *RegistrationRequest, options ValidationOptions) (Decision, error) {
	if command == nil {
		return rejectDecision(CauseProtocolErrorUnspecified, "nil Security Mode Command"), nil
	}
	if request == nil {
		return rejectDecision(CauseProtocolErrorUnspecified, "nil Registration Request"), nil
	}

	// A Security Mode Command that establishes a new context must use security
	// header type 3. A plain command, or one protected with the old context, is
	// not acceptable for this procedure.
	if command.SecurityHeaderType != SecurityHeaderIntegrityProtectedNewContext {
		return rejectDecision(CauseSecurityModeRejectedUnspecified,
			fmt.Sprintf("Security Mode Command is not integrity protected with a new 5G NAS security context (security header type 0x%02x)", command.SecurityHeaderType)), nil
	}
	if len(command.MessageAuthenticationCode) != nasMACLength {
		return rejectDecision(CauseProtocolErrorUnspecified, "Security Mode Command has no four-octet NAS MAC"), nil
	}

	// 5G-IA0 provides no integrity. It is only acceptable for emergency
	// registration, and even then the NAS MAC is verified as the all-zero
	// value defined by the null algorithm.
	if command.SelectedIntegrityAlgorithm == IntegrityAlgorithm5GIA0 && !request.Emergency {
		return rejectDecision(CauseSecurityModeRejectedUnspecified,
			"5G-IA0 is only allowed for emergency registration"), nil
	}

	macFunc := options.MACFunc
	if macFunc == nil {
		macFunc = ComputeNASMAC
	}

	var expectedMAC []byte
	if command.SelectedIntegrityAlgorithm == IntegrityAlgorithm5GIA0 {
		expectedMAC = []byte{0, 0, 0, 0}
	} else {
		key, err := DeriveKNASint(options.KAMF, command.SelectedIntegrityAlgorithm)
		if err != nil {
			return Decision{}, err
		}
		message := protectedMessageBytes(command)
		expectedMAC, err = macFunc(command.SelectedIntegrityAlgorithm, key, options.Count, options.Bearer, directionDownlink, message)
		if err != nil {
			return rejectDecision(CauseSecurityModeRejectedUnspecified, err.Error()), nil
		}
	}
	if len(expectedMAC) != nasMACLength ||
		subtle.ConstantTimeCompare(expectedMAC, command.MessageAuthenticationCode) != 1 {
		return rejectDecision(CauseSecurityModeRejectedUnspecified, "NAS MAC verification failed"), nil
	}

	// The replayed bytes must be exactly the bytes from the Registration
	// Request. This comparison deliberately includes the EPS encryption and
	// integrity capability octets; removing even one capability bit changes
	// the replayed value and is rejected.
	if !capabilitiesEqual(command.ReplayedUESecurityCapabilities, request.UESecurityCapabilities) {
		return rejectDecision(CauseUESecurityCapabilitiesMismatch,
			"replayed UE security capabilities differ from the Registration Request"), nil
	}

	capability, err := ParseUESecurityCapability(request.UESecurityCapabilities)
	if err != nil {
		return rejectDecision(CauseUESecurityCapabilitiesMismatch, err.Error()), nil
	}

	// Algorithm selection is also bound to the capabilities that the UE
	// actually sent. Selecting an unadvertised algorithm is a capability
	// mismatch even if the SMC's echo was made to look consistent.
	if !capability.Supports5GEA(command.SelectedCipheringAlgorithm) {
		return rejectDecision(CauseUESecurityCapabilitiesMismatch,
			fmt.Sprintf("selected 5G-EA%d was not advertised by the UE", command.SelectedCipheringAlgorithm)), nil
	}
	if !capability.Supports5GIA(command.SelectedIntegrityAlgorithm) {
		return rejectDecision(CauseUESecurityCapabilitiesMismatch,
			fmt.Sprintf("selected 5G-IA%d was not advertised by the UE", command.SelectedIntegrityAlgorithm)), nil
	}

	return Decision{Accepted: true}, nil
}

// protectedMessageBytes returns the NAS octets covered by the NAS MAC: the
// sequence number followed by the complete plain NAS message (TS 24.501,
// clause 4.4.2).
func protectedMessageBytes(command *SecurityModeCommand) []byte {
	message := make([]byte, 1, 1+len(command.PlainNAS))
	message[0] = command.SequenceNumber
	message = append(message, command.PlainNAS...)
	return message
}
