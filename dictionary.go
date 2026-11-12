package netsec

// AVPKey identifies an AVP. Codes in the IETF namespace use VendorID 0.
type AVPKey struct {
	Code     uint32
	VendorID uint32
}

// AVPInfo describes an AVP understood by the proxy.
type AVPInfo struct {
	Name    string
	Grouped bool
}

// CodeFailedAVP is the AVP Code of the Failed-AVP Grouped AVP (RFC 6733).
const CodeFailedAVP uint32 = 279

// CodeResultCode is the AVP Code of the Result-Code AVP.
const CodeResultCode uint32 = 268

// IETF AVP codes understood by the decoder.
//
// A proxy only needs to know the AVPs on which it makes policy decisions, but
// a reasonable base dictionary is required to distinguish an unsupported
// mandatory AVP from an optional one. Applications may extend the dictionary
// with NewDecoderWithKnown.
func defaultDictionary() map[AVPKey]AVPInfo {
	d := make(map[AVPKey]AVPInfo, 256)
	ietf := func(code uint32, name string, grouped bool) {
		d[AVPKey{Code: code}] = AVPInfo{Name: name, Grouped: grouped}
	}

	// RADIUS attributes reused by Diameter applications (codes 1..255).
	for code, name := range radiusAVPNames {
		ietf(code, name, false)
	}

	// Diameter base protocol AVPs (RFC 6733).
	ietf(258, "Auth-Application-Id", false)
	ietf(259, "Acct-Application-Id", false)
	ietf(260, "Vendor-Specific-Application-Id", true)
	ietf(261, "Redirect-Host-Usage", false)
	ietf(262, "Redirect-Max-Cache-Time", false)
	ietf(263, "Session-Id", false)
	ietf(264, "Origin-Host", false)
	ietf(265, "Supported-Vendor-Id", false)
	ietf(266, "Vendor-Id", false)
	ietf(267, "Firmware-Version", false)
	ietf(268, "Result-Code", false)
	ietf(269, "Product-Name", false)
	ietf(270, "Session-Binding", false)
	ietf(271, "Session-Server-Failover", false)
	ietf(272, "Multi-Round-Time-Out", false)
	ietf(273, "Disconnect-Cause", false)
	ietf(274, "Auth-Request-Type", false)
	ietf(275, "Authorization-Lifetime", false)
	ietf(276, "Auth-Grace-Period", false)
	ietf(277, "Auth-Session-State", false)
	ietf(278, "Origin-State-Id", false)
	ietf(279, "Failed-AVP", true)
	ietf(280, "Proxy-Host", false)
	ietf(281, "Error-Message", false)
	ietf(282, "Route-Record", false)
	ietf(283, "Destination-Realm", false)
	ietf(284, "Proxy-Info", true)
	ietf(285, "Re-Auth-Request-Type", false)
	ietf(286, "Accounting-Session-Id", false)
	ietf(287, "Accounting-Sub-Session-Id", false)
	ietf(288, "Accounting-Input-Octets", false)
	ietf(289, "Accounting-Output-Octets", false)
	ietf(290, "Accounting-Input-Packets", false)
	ietf(291, "Accounting-Output-Packets", false)
	ietf(292, "Accounting-Auth-Method", false)
	ietf(293, "Destination-Host", false)
	ietf(294, "Accounting-Session-Time", false)
	ietf(295, "Termination-Cause", false)
	ietf(296, "Origin-Realm", false)
	ietf(297, "Experimental-Result", true)
	ietf(298, "Experimental-Result-Code", false)

	// Diameter Credit-Control Application AVPs (RFC 8506). The whole assigned
	// range is known so a valid application message is not treated as
	// unsupported; only verified Grouped entries are parsed recursively.
	for code := uint32(411); code <= 470; code++ {
		ietf(code, "Credit-Control-AVP", false)
	}
	ietf(413, "CC-Money", true)
	ietf(423, "Cost-Information", true)
	ietf(431, "Granted-Service-Unit", true)
	ietf(437, "Requested-Service-Unit", true)
	ietf(446, "Used-Service-Unit", true)
	ietf(456, "Multiple-Services-Credit-Control", true)

	// Accounting AVPs from the base protocol.
	ietf(480, "Accounting-Record-Type", false)
	ietf(483, "Accounting-Realtime-Required", false)
	ietf(485, "Accounting-Record-Number", false)

	// QoS / traffic-classification Grouped AVPs (RFC 5777).
	ietf(511, "Classifier", true)
	ietf(512, "Classifier-ID", false)
	ietf(513, "Protocol", false)
	ietf(515, "From-Spec", true)
	ietf(516, "To-Spec", true)

	return d
}

// radiusAVPNames contains the RADIUS attributes commonly carried in Diameter
// NASREQ messages. Diameter reserves AVP codes 1..255 for RADIUS compatibility.
var radiusAVPNames = map[uint32]string{
	1:  "User-Name",
	2:  "User-Password",
	3:  "CHAP-Password",
	4:  "NAS-IP-Address",
	5:  "NAS-Port",
	6:  "Service-Type",
	7:  "Framed-Protocol",
	8:  "Framed-IP-Address",
	9:  "Framed-IP-Netmask",
	10: "Framed-Routing",
	11: "Framed-Compression",
	12: "Framed-IPv6-Prefix",
	13: "Framed-Interface-Id",
	14: "Framed-IPv6-Route",
	15: "Framed-IPv6-Pool",
	18: "Reply-Message",
	22: "Framed-Route",
	23: "Framed-IPv6-Route",
	24: "Framed-IPv6-Pool",
	27: "Session-Timeout",
	28: "Idle-Timeout",
	29: "Termination-Action",
	30: "Called-Station-Id",
	31: "Calling-Station-Id",
	32: "NAS-Identifier",
	33: "Proxy-State",
	34: "Login-LAT-Service",
	35: "Login-LAT-Node",
	36: "Login-LAT-Group",
	37: "Framed-AppleTalk-Link",
	38: "Framed-AppleTalk-Network",
	39: "Framed-AppleTalk-Zone",
	40: "Acct-Status-Type",
	41: "Acct-Delay-Time",
	42: "Acct-Input-Octets",
	43: "Acct-Output-Octets",
	44: "Acct-Session-Id",
	45: "Acct-Authentic",
	46: "Acct-Session-Time",
	47: "Acct-Input-Packets",
	48: "Acct-Output-Packets",
	49: "Acct-Terminate-Cause",
	50: "Acct-Multi-Session-Id",
	51: "Acct-Link-Count",
	52: "Acct-Input-Gigawords",
	53: "Acct-Output-Gigawords",
	55: "Event-Timestamp",
	60: "CHAP-Challenge",
	61: "NAS-Port-Type",
	62: "Port-Limit",
	63: "Login-LAT-Port",
	64: "Tunnel-Type",
	65: "Tunnel-Medium-Type",
	66: "Tunnel-Client-Endpoint",
	67: "Tunnel-Server-Endpoint",
	68: "Acct-Tunnel-Connection",
	69: "Tunnel-Password",
	70: "ARAP-Password",
	71: "ARAP-Challenge-Response",
	72: "ARAP-Features",
	73: "ARAP-Zone-Access",
	74: "ARAP-Security",
	75: "ARAP-Security-Data",
	76: "Password-Retry",
	77: "Usage",
	78: "Prompt",
	79: "Connect-Info",
	80: "Configuration-Token",
	81: "Tunnel-Private-Group-ID",
	82: "Tunnel-Assignment-ID",
	83: "Tunnel-Preference",
	84: "ARAP-Challenge",
	85: "Acct-Interim-Interval",
	86: "Acct-Tunnel-Packets-Lost",
	87: "NAS-Port-Id",
	88: "Framed-Pool",
	89: "Framed-IPv6-Address",
	90: "Tunnel-Client-Auth-ID",
	91: "Tunnel-Server-Auth-ID",
}
