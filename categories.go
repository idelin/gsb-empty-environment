package netsec

// PublicCategory is one of the small, fixed set of failure classes the
// embedded host is allowed to observe across its boundary. Every public
// code occupies the same wire width so that response length cannot reveal
// which internal handshake stage failed.
type PublicCategory string

const (
	// PublicHandshakeFailed reports a generic transport handshake failure.
	PublicHandshakeFailed PublicCategory = "EHSK"
	// PublicAuthFailed reports a generic authentication or authorization
	// failure without distinguishing which internal check rejected it.
	PublicAuthFailed PublicCategory = "EAUT"
	// PublicUnavailable reports that the service is temporarily unable to
	// serve the request.
	PublicUnavailable PublicCategory = "ESVC"
	// PublicInternal is the fallback class for every failure without an
	// explicit public mapping.
	PublicInternal PublicCategory = "EINT"
)

// IsValid reports whether c belongs to the allow-listed public categories.
func (c PublicCategory) IsValid() bool {
	switch c {
	case PublicHandshakeFailed, PublicAuthFailed, PublicUnavailable, PublicInternal:
		return true
	default:
		return false
	}
}

// internalCategory is the detailed failure class retained inside the
// service. Internal categories never cross the host boundary and are only
// exposed to the redacting logger for local diagnostics.
type internalCategory string

const (
	stageVersionNegotiation internalCategory = "stage:version-negotiation"
	stageCipherSuites       internalCategory = "stage:cipher-suite-selection"
	stageCertificate        internalCategory = "stage:certificate-validation"
	stageKeyExchange        internalCategory = "stage:key-exchange"
	stageClientIdentity     internalCategory = "stage:client-identity"
	stagePreSharedKey       internalCategory = "stage:pre-shared-key"
	stageTokenBinding       internalCategory = "stage:token-binding"
	stageAuthorization      internalCategory = "stage:authorization"
	stageRateLimited        internalCategory = "stage:rate-limited"
	stageBackendUnavailable internalCategory = "stage:backend-unavailable"
	stagePanicRecovered     internalCategory = "stage:panic-recovered"
	stageUnknown            internalCategory = "stage:unknown"
)

func (c internalCategory) isInternal() bool {
	switch c {
	case stageVersionNegotiation,
		stageCipherSuites,
		stageCertificate,
		stageKeyExchange,
		stageClientIdentity,
		stagePreSharedKey,
		stageTokenBinding,
		stageAuthorization,
		stageRateLimited,
		stageBackendUnavailable,
		stagePanicRecovered,
		stageUnknown:
		return true
	default:
		return false
	}
}

// publicMapping is the single allow-list deciding which internal stage may
// surface as which public class. Anything absent from this map collapses to
// PublicInternal, so a newly added internal stage can never leak by default.
var publicMapping = map[internalCategory]PublicCategory{
	stageVersionNegotiation: PublicHandshakeFailed,
	stageCipherSuites:       PublicHandshakeFailed,
	stageCertificate:        PublicHandshakeFailed,
	stageKeyExchange:        PublicHandshakeFailed,
	stageClientIdentity:     PublicAuthFailed,
	stagePreSharedKey:       PublicAuthFailed,
	stageTokenBinding:       PublicAuthFailed,
	stageAuthorization:      PublicAuthFailed,
	stageRateLimited:        PublicUnavailable,
	stageBackendUnavailable: PublicUnavailable,
	// stagePanicRecovered and stageUnknown intentionally have no mapping.
}

func (c internalCategory) public() PublicCategory {
	if mapped, ok := publicMapping[c]; ok {
		return mapped
	}
	return PublicInternal
}
