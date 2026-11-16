package netsec

import (
	"bytes"
	"errors"
)

// 5GMM cause values used in SECURITY MODE REJECT (TS 24.501 section 9.11.3.2).
const (
	// CauseUECapabilitiesMismatch (#23) is used when the replayed UE security
	// capabilities do not match those sent in the Registration Request.
	CauseUECapabilitiesMismatch byte = 23
	// CauseSecurityModeRejectedUnspecified (#24) is used for null integrity
	// outside emergency services or unsupported algorithm selections.
	CauseSecurityModeRejectedUnspecified byte = 24
	// CauseMACFailure (#20) is used when the NAS-MAC verification fails.
	CauseMACFailure byte = 20
)

// ErrSecurityModeReject signals that the SMC must be answered with a SECURITY
// MODE REJECT. The Cause value carries the 5GMM cause to put in the reject.
type ErrSecurityModeReject struct {
	Cause byte
	// Reject is the fully encoded plain NAS SECURITY MODE REJECT message
	// (extended protocol discriminator header), ready to send.
	Reject []byte
	reason string
}

func (e *ErrSecurityModeReject) Error() string { return e.reason }

// SMCConfig carries the local UE state needed to validate a Security Mode
// Command.
type SMCConfig struct {
	// KAMF is the 256-bit AMF key established during primary authentication.
	KAMF []byte
	// UESecurityCapabilities is the exact value octets of the UE security
	// capabilities IE sent in the Registration Request (use
	// ExtractUESecurityCapabilities to obtain them), including EPS capabilities.
	UESecurityCapabilities []byte
	// ExpectedSequenceNumber is the NAS COUNT sequence number the UE expects on
	// the first integrity protected message with the new security context.
	ExpectedSequenceNumber byte
	// EmergencyRegistration reports whether the current registration is an
	// emergency registration; it is the only case where NIA0 is permitted.
	EmergencyRegistration bool
}

// SMCResult describes a successfully validated Security Mode Command.
type SMCResult struct {
	SelectedNEA byte
	SelectedNIA byte
	// SequenceNumber is the NAS COUNT sequence number carried by the SMC; use
	// it together with the NAS overflow counter (zero for the first message)
	// when installing the new security context.
	SequenceNumber byte
	KNasInt        []byte
	KNasEnc        []byte
}

// VerifySecurityModeCommand validates a downlink NAS Security Mode Command
// against the UE's registration state, implementing the UE-side checks of
// TS 33.501 section 6.7.2 and TS 24.501 section 5.4.2.3:

//  1. The message must be integrity protected (plain or ciphered-before-context
//     SMCs are rejected).
//  2. Selected 5G-IA0 is only accepted for emergency registrations.
//  3. The NAS-MAC must verify against the MAC recomputed from K_AMF-derived
//     K_nasint for every non-null integrity algorithm.
//  4. The replayed UE security capabilities must be byte-identical to the
//     capabilities sent in the Registration Request, including the nested EPS
//     NAS security algorithms.
//
// On failure it returns *ErrSecurityModeReject whose Reject field contains the
// encoded SECURITY MODE REJECT to transmit.
func VerifySecurityModeCommand(message []byte, cfg SMCConfig) (*SMCResult, error) {
	reject := func(cause byte, reason string) error {
		return &ErrSecurityModeReject{
			Cause:  cause,
			Reject: BuildSecurityModeReject(cause),
			reason: reason,
		}
	}

	pm, err := ParseProtectedMessage(message)
	if err != nil {
		return nil, reject(CauseSecurityModeRejectedUnspecified,
			"netsec: SMC is not an integrity protected NAS message: "+err.Error())
	}

	smc, err := ParseSecurityModeCommand(pm.Plaintext)
	if err != nil {
		return nil, reject(CauseSecurityModeRejectedUnspecified, err.Error())
	}

	// 5G-IA0 is permitted for emergency registrations only. For emergency NIA0
	// the NAS-MAC is defined as zero and cannot be verified (TS 33.501 6.7.2).
	if smc.SelectedNIA == NIA0 {
		if !cfg.EmergencyRegistration {
			return nil, reject(CauseSecurityModeRejectedUnspecified,
				"netsec: null integrity algorithm 5G-IA0 selected outside emergency registration")
		}
	} else {
		if pm.SequenceNumber != cfg.ExpectedSequenceNumber {
			return nil, reject(CauseMACFailure,
				"netsec: SMC sequence number mismatch")
		}
		alg, ok := integrityAlgorithms[smc.SelectedNIA]
		if !ok {
			return nil, reject(CauseSecurityModeRejectedUnspecified,
				"netsec: selected NAS integrity algorithm is not supported")
		}
		kNasInt, err := DeriveNASIntegrityKey(cfg.KAMF, smc.SelectedNIA)
		if err != nil {
			return nil, reject(CauseSecurityModeRejectedUnspecified, err.Error())
		}
		count := uint32(pm.SequenceNumber)
		expectedMAC, err := alg.ComputeMAC(kNasInt[:16], count, DirectionDownlink, pm.Plaintext)
		if err != nil {
			return nil, reject(CauseSecurityModeRejectedUnspecified, err.Error())
		}
		if !equalMAC(expectedMAC, pm.MAC) {
			return nil, reject(CauseMACFailure,
				"netsec: NAS-MAC verification failed")
		}
	}

	// Exact echo check: every capability octet, including nested EPS NAS
	// security algorithms, must match what the UE actually sent.
	if len(smc.ReplayedUECapabilities) == 0 {
		return nil, reject(CauseUECapabilitiesMismatch,
			"netsec: SMC carries no replayed UE security capabilities")
	}
	if !bytes.Equal(smc.ReplayedUECapabilities, cfg.UESecurityCapabilities) {
		return nil, reject(CauseUECapabilitiesMismatch,
			"netsec: replayed UE security capabilities differ from the registration request")
	}

	result := &SMCResult{
		SelectedNEA:    smc.SelectedNEA,
		SelectedNIA:    smc.SelectedNIA,
		SequenceNumber: pm.SequenceNumber,
	}
	// Emergency NIA0 still installs the (null) keys; derive them lazily for the
	// non-null case so callers get ready-to-use key material.
	if smc.SelectedNIA != NIA0 {
		result.KNasInt, _ = DeriveNASIntegrityKey(cfg.KAMF, smc.SelectedNIA)
		result.KNasEnc, err = DeriveNASEncryptionKey(cfg.KAMF, smc.SelectedNEA)
		if err != nil {
			return nil, reject(CauseSecurityModeRejectedUnspecified, err.Error())
		}
	}
	return result, nil
}

// BuildSecurityModeReject encodes a plain NAS SECURITY MODE REJECT message
// (TS 24.501 section 8.2.26): extended protocol discriminator (2 octets),
// message type, then the 5GMM cause as a 2-octet TV IE (IEI 0x59).
func BuildSecurityModeReject(cause byte) []byte {
	return []byte{
		PD5GMM, // extended protocol discriminator
		0x00,   // security header type: plain NAS message
		MsgSecurityModeReject,
		0x59, // 5GMM cause IEI
		cause,
	}
}

// AsRejectError unwraps *ErrSecurityModeReject from a verification error.
func AsRejectError(err error) (*ErrSecurityModeReject, bool) {
	var reject *ErrSecurityModeReject
	if errors.As(err, &reject) {
		return reject, true
	}
	return nil, false
}
