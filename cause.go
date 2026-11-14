package netsec

import "fmt"

// CauseCode is a 5GSM or 5GMM NAS cause value. The session-management
// authorization path uses 5GSM causes; registration-level no-slice failures
// also expose 5GMM cause #62 through CauseNoNetworkSlicesAvailable.
type CauseCode uint8

const (
	// 5GMM cause #13: Roaming not allowed in this tracking area.
	CauseRoamingNotAllowedInTrackingArea CauseCode = 13
	CauseRoamingNotAllowed               CauseCode = CauseRoamingNotAllowedInTrackingArea

	// 5GSM cause #27: Missing or unknown DNN.
	CauseMissingOrUnknownDNN CauseCode = 27

	// 5GSM cause #29: User authentication or authorization failed.
	CauseUserAuthenticationOrAuthorizationFailed CauseCode = 29

	// 5GSM cause #31: Request rejected, unspecified.
	CauseRequestRejectedUnspecified CauseCode = 31
	CauseUnspecified                CauseCode = CauseRequestRejectedUnspecified

	// 5GSM cause #32: Service option not supported. This is used when a
	// requested S-NSSAI is not in the registration or target access-network
	// allowed sets.
	CauseServiceOptionNotSupported CauseCode = 32

	// 5GSM cause #33: Requested service option not subscribed. This is used
	// for an S-NSSAI, or an SST/SD pair, outside the subscription.
	CauseRequestedServiceOptionNotSubscribed CauseCode = 33
	CauseSliceNotSubscribed                  CauseCode = CauseRequestedServiceOptionNotSubscribed

	// 5GSM cause #38: Network failure.
	CauseNetworkFailure CauseCode = 38

	// 5GMM cause #62: No network slices available.
	CauseNoNetworkSlicesAvailable CauseCode = 62

	// 5GSM cause #67/#69: Insufficient resources for a specific slice and DNN,
	// or for a specific slice.
	CauseInsufficientResourcesForSpecificSliceAndDNN CauseCode = 67
	CauseInsufficientResourcesForSpecificSlice       CauseCode = 69
)

func (c CauseCode) String() string {
	switch c {
	case CauseRoamingNotAllowedInTrackingArea:
		return "5GMM cause #13: roaming not allowed in this tracking area"
	case CauseMissingOrUnknownDNN:
		return "5GSM cause #27: missing or unknown DNN"
	case CauseUserAuthenticationOrAuthorizationFailed:
		return "5GSM cause #29: user authentication or authorization failed"
	case CauseRequestRejectedUnspecified:
		return "5GSM cause #31: request rejected, unspecified"
	case CauseServiceOptionNotSupported:
		return "5GSM cause #32: service option not supported"
	case CauseRequestedServiceOptionNotSubscribed:
		return "5GSM cause #33: requested service option not subscribed"
	case CauseNetworkFailure:
		return "5GSM cause #38: network failure"
	case CauseNoNetworkSlicesAvailable:
		return "5GMM cause #62: no network slices available"
	case CauseInsufficientResourcesForSpecificSliceAndDNN:
		return "5GSM cause #67: insufficient resources for specific slice and DNN"
	case CauseInsufficientResourcesForSpecificSlice:
		return "5GSM cause #69: insufficient resources for specific slice"
	default:
		return fmt.Sprintf("NAS cause #%d", uint8(c))
	}
}

// RejectionReason is a stable, machine-readable explanation for a denied
// S-NSSAI.
type RejectionReason string

const (
	ReasonInvalidSNSSAI                 RejectionReason = "invalid_snssai"
	ReasonSliceNotSubscribed            RejectionReason = "slice_not_subscribed"
	ReasonSDMismatch                    RejectionReason = "sd_mismatch"
	ReasonSubscriptionPolicyConflict    RejectionReason = "subscription_policy_conflict"
	ReasonRoamingRestricted             RejectionReason = "roaming_restricted"
	ReasonRegistrationNotAllowed        RejectionReason = "registration_not_allowed"
	ReasonTargetAccessNetworkNotAllowed RejectionReason = "target_access_network_not_allowed"
	ReasonDNNMismatch                   RejectionReason = "dnn_mismatch"
	ReasonQoSGenerationMismatch         RejectionReason = "qos_generation_mismatch"
	ReasonNoCandidateSlices             RejectionReason = "no_candidate_slices"
	ReasonInheritedContextOnly          RejectionReason = "inherited_context_only"
)
