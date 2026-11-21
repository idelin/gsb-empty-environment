package sliceauth

import "fmt"

// ProtocolLayer selects the 3GPP cause-code space used for a rejection.
type ProtocolLayer string

const (
	// Layer5GMM maps to TS 24.501 section 9.11.3.2 5GMM cause codes
	// (registration/mobility and standalone S-NSSAI procedures).
	Layer5GMM ProtocolLayer = "5GMM"
	// Layer5GSM maps to TS 24.501 section 9.11.4.2 5GSM cause codes
	// (PDU session establishment/modification/handover).
	Layer5GSM ProtocolLayer = "5GSM"
)

// Cause is a standard TS 24.501 rejection cause.
type Cause struct {
	Layer ProtocolLayer
	Code  uint8
	Name  string
}

func (c Cause) String() string {
	return fmt.Sprintf("%s cause #%d (%s)", c.Layer, c.Code, c.Name)
}

// 5GMM cause codes (TS 24.501 table 9.11.3.2.1) used by this component.
const (
	Cause5GMMPlmnNotAllowed       uint8 = 11
	Cause5GMMRoamingNotAllowed    uint8 = 13
	Cause5GMMReactivationRequired uint8 = 39
	Cause5GMMNoNetworkSlices      uint8 = 62
	Cause5GMMSliceNotSubscribed   uint8 = 71
	Cause5GMMUnsupportedSlice     uint8 = 71
	Cause5GMMDNNNotSubscribed     uint8 = 72
)

// 5GSM cause codes (TS 24.501 table 9.11.4.2.1) used by this component.
const (
	Cause5GSMMissingDNN                 uint8 = 70
	Cause5GSMDNNNotSubscribed           uint8 = 70
	Cause5GSMInsufficientResources      uint8 = 67
	Cause5GSMSliceNotSupported          uint8 = 68
	Cause5GSMUnsupportedSlice           uint8 = 26
	Cause5GSMUserAuthOrDNNAuthorization uint8 = 29
	Cause5GSMSliceNotInAllowedNSSAI     uint8 = 68
)

var mmCauses = map[RejectReason]Cause{
	ReasonSliceNotSubscribed: {
		Layer: Layer5GMM, Code: Cause5GMMSliceNotSubscribed,
		Name: "S-NSSAI not subscribed",
	},
	ReasonSDMismatch: {
		Layer: Layer5GMM, Code: Cause5GMMSliceNotSubscribed,
		Name: "S-NSSAI not subscribed (SD mismatch)",
	},
	ReasonRoamingRestricted: {
		Layer: Layer5GMM, Code: Cause5GMMRoamingNotAllowed,
		Name: "Roaming not allowed in this tracking area",
	},
	ReasonSliceNotInAllowedNSSAI: {
		Layer: Layer5GMM, Code: Cause5GMMUnsupportedSlice,
		Name: "S-NSSAI not supported by the AMF",
	},
	ReasonSliceUnsupportedByTarget: {
		Layer: Layer5GMM, Code: Cause5GMMUnsupportedSlice,
		Name: "S-NSSAI not supported by the RAN/AMF",
	},
	ReasonDNNNotSubscribed: {
		Layer: Layer5GMM, Code: Cause5GMMDNNNotSubscribed,
		Name: "DNN not supported or not subscribed for the S-NSSAI",
	},
	ReasonPolicyGenerationMismatch: {
		Layer: Layer5GMM, Code: Cause5GMMReactivationRequired,
		Name: "Reactivation requested",
	},
	ReasonNoSlicesAvailable: {
		Layer: Layer5GMM, Code: Cause5GMMNoNetworkSlices,
		Name: "No network slices available",
	},
}

var gsmCauses = map[RejectReason]Cause{
	ReasonSliceNotSubscribed: {
		Layer: Layer5GSM, Code: Cause5GSMSliceNotSupported,
		Name: "S-NSSAI not supported",
	},
	ReasonSDMismatch: {
		Layer: Layer5GSM, Code: Cause5GSMSliceNotSupported,
		Name: "S-NSSAI not supported (SD mismatch)",
	},
	ReasonRoamingRestricted: {
		Layer: Layer5GSM, Code: Cause5GSMUserAuthOrDNNAuthorization,
		Name: "User authentication or authorization failed",
	},
	ReasonSliceNotInAllowedNSSAI: {
		Layer: Layer5GSM, Code: Cause5GSMSliceNotInAllowedNSSAI,
		Name: "S-NSSAI not in allowed NSSAI",
	},
	ReasonSliceUnsupportedByTarget: {
		Layer: Layer5GSM, Code: Cause5GSMUnsupportedSlice,
		Name: "Insufficient resources for specific slice and DNN",
	},
	ReasonDNNNotSubscribed: {
		Layer: Layer5GSM, Code: Cause5GSMDNNNotSubscribed,
		Name: "Missing or unknown DNN",
	},
	ReasonPolicyGenerationMismatch: {
		Layer: Layer5GSM, Code: Cause5GSMUserAuthOrDNNAuthorization,
		Name: "User authentication or authorization failed",
	},
}

func mapCause(reason RejectReason, layer ProtocolLayer) Cause {
	switch layer {
	case Layer5GSM:
		if cause, ok := gsmCauses[reason]; ok {
			return cause
		}
	case Layer5GMM:
		if cause, ok := mmCauses[reason]; ok {
			return cause
		}
	}
	return Cause{Layer: layer, Code: Cause5GSMSliceNotSupported, Name: "S-NSSAI not supported"}
}

func reject(reason RejectReason, layer ProtocolLayer, format string, args ...any) Rejection {
	return Rejection{
		Reason: reason,
		Cause:  mapCause(reason, layer),
		Detail: fmt.Sprintf(format, args...),
	}
}
