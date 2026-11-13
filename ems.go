package netsec

// NegotiateEMS applies the enforced extended_master_secret (RFC 7627)
// negotiation policy to a pair of TLS 1.2 hello messages.
//
// EMS is negotiated only when both the client and the server advertise the
// extension. If exactly one side advertises it the handshake fails instead of
// silently downgrading, and when neither side advertises it a legacy master
// secret is permitted.
func NegotiateEMS(clientAdvertisesEMS, serverAdvertisesEMS bool) (extendedMasterSecret bool, err error) {
	switch {
	case clientAdvertisesEMS && serverAdvertisesEMS:
		return true, nil
	case !clientAdvertisesEMS && !serverAdvertisesEMS:
		return false, nil
	default:
		return false, ErrEMSNegotiationMismatch
	}
}

// ValidateResumption enforces the extended_master_secret policy for a TLS 1.2
// session resumption. The negotiated EMS state must be symmetric between the
// two peers and must exactly match the flag recorded for the original session.
//
// It fails when the extension is offered by only one side, when an EMS session
// loses the flag during resumption, or when a legacy session suddenly presents
// the flag. The EMS flag is intentionally not carried in resumption tickets;
// only the server-side session state is authoritative here.
func ValidateResumption(clientAdvertisesEMS, serverAdvertisesEMS bool, session *Session) error {
	if session == nil {
		return ErrSessionUnknown
	}
	return ValidateResumptionFlag(clientAdvertisesEMS, serverAdvertisesEMS, session.ExtendedMasterSecret)
}

// ValidateResumptionFlag is the flag-only form of ValidateResumption for
// callers that hold the original session's EMS state without a full Session
// value, for example a client session cache.
func ValidateResumptionFlag(clientAdvertisesEMS, serverAdvertisesEMS, sessionExtendedMasterSecret bool) error {
	negotiated, err := NegotiateEMS(clientAdvertisesEMS, serverAdvertisesEMS)
	if err != nil {
		return err
	}
	switch {
	case sessionExtendedMasterSecret && !negotiated:
		return ErrEMSResumptionLost
	case !sessionExtendedMasterSecret && negotiated:
		return ErrEMSResumptionUnexpected
	default:
		return nil
	}
}
