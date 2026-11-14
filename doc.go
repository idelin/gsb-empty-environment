// Package netsec provides subscription-authoritative network-slice
// authorization for a Go session management function (SMF).
//
// The package treats every S-NSSAI set received from a registration, PDU
// session, source network, old SMF context, or target access network as
// untrusted input. Authorization always starts from the subscription policy
// table. SST and SD form an exact compound identity; an SD is a three-octet
// value and is never inferred from a shorter string.
//
// A handover decision is the intersection of the current session/request
// candidates, the subscription, the registration allowed set (when supplied),
// and the target access network's allowed set. AMF reselection, session
// continuity, OldAllowedSlices, and OldSMFContext do not create authority:
// their slices are rechecked before they can appear in Decision.Allowed.
package netsec
