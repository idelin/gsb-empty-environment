// Package s4u2proxy implements the KDC-side authorization decision for
// S4U2Proxy (constrained delegation) service-ticket requests.
//
// A compromised front-end service can abuse constrained delegation to
// impersonate arbitrary users against back ends it was never granted
// access to. Evaluate enforces the checks a KDC must perform before
// issuing the delegated ticket:
//
//   - the PAC client info must match the additional ticket;
//   - accounts marked sensitive/not-delegatable, or members of the
//     Protected Users group, must be refused;
//   - the additional (evidence) ticket must be forwardable;
//   - a ticket obtained via S4U2Self is only forwardable when the
//     service is trusted for protocol transition
//     (TrustedToAuthForDelegation);
//   - the target SPN must appear in the service account's
//     AllowedToDelegateTo list, or the back end's RBCD descriptor
//     (msDS-AllowedToActOnBehalfOfOtherIdentity) must name the service.
package s4u2proxy

import "strings"

// Ticket describes the additional (evidence) ticket presented with the
// S4U2Proxy request, typically the service ticket obtained via S4U2Self
// or a forwarded TGT/session ticket.
type Ticket struct {
	// Forwardable is the ticket's FORWARDABLE flag.
	Forwardable bool
	// FromS4U2Self reports whether the ticket was minted through an
	// S4U2Self protocol-transition exchange.
	FromS4U2Self bool
	// ClientName and ClientRealm identify the client the ticket was
	// issued to (cname/crealm).
	ClientName  string
	ClientRealm string
}

// PAC carries the client identity asserted inside the ticket's
// Privilege Attribute Certificate.
type PAC struct {
	ClientName  string
	ClientRealm string
}

// UserAccount is the domain account being impersonated.
type UserAccount struct {
	Name string
	// NotDelegatable mirrors the USER_NOT_DELEGATED userAccountControl
	// bit ("Account is sensitive and cannot be delegated").
	NotDelegatable bool
	// MemberOfProtectedUsers reports membership in the Protected Users
	// group, which forbids delegation of the account.
	MemberOfProtectedUsers bool
}

// ServiceAccount is the (potentially compromised) service requesting
// the delegated ticket.
type ServiceAccount struct {
	Name string
	// AllowedToDelegateTo mirrors msDS-AllowedToDelegateTo: the SPNs
	// this service may delegate to.
	AllowedToDelegateTo []string
	// TrustedToAuthForDelegation mirrors the
	// TRUSTED_TO_AUTH_FOR_DELEGATION bit: protocol transition is
	// enabled, so S4U2Self evidence tickets may be forwardable.
	TrustedToAuthForDelegation bool
}

// Backend is the target service the delegated ticket is requested for.
type Backend struct {
	// SPN is the target service principal name.
	SPN string
	// AllowedToActOnBehalfOf lists the principals named by the back
	// end's resource-based constrained delegation descriptor
	// (msDS-AllowedToActOnBehalfOfOtherIdentity).
	AllowedToActOnBehalfOf []string
}

// Request bundles everything the KDC needs to decide an S4U2Proxy
// service-ticket request.
type Request struct {
	User             UserAccount
	Service          ServiceAccount
	Backend          Backend
	AdditionalTicket Ticket
	PAC              PAC
}

// Decision is the outcome of evaluating a request.
type Decision struct {
	Allowed bool
	Reason  string
}

// Evaluate applies the S4U2Proxy authorization checks in order and
// reports whether the delegated ticket may be issued.
func Evaluate(r Request) Decision {
	if !pacMatchesTicket(r.PAC, r.AdditionalTicket) {
		return deny("PAC client info does not match the additional ticket")
	}
	if r.User.NotDelegatable {
		return deny("user account is marked as not delegatable")
	}
	if r.User.MemberOfProtectedUsers {
		return deny("user is a member of Protected Users and cannot be delegated")
	}
	if r.AdditionalTicket.FromS4U2Self && !r.Service.TrustedToAuthForDelegation {
		return deny("S4U2Self ticket is forwardable only when protocol transition is enabled for the service")
	}
	if !r.AdditionalTicket.Forwardable {
		return deny("additional ticket is not forwardable")
	}
	if !spnAuthorized(r.Service, r.Backend) {
		return deny("target SPN is not in AllowedToDelegateTo and the back end's RBCD descriptor does not list the service")
	}
	return Decision{Allowed: true, Reason: "delegation authorized"}
}

func deny(reason string) Decision {
	return Decision{Allowed: false, Reason: reason}
}

func pacMatchesTicket(pac PAC, t Ticket) bool {
	return strings.EqualFold(pac.ClientName, t.ClientName) &&
		strings.EqualFold(pac.ClientRealm, t.ClientRealm)
}

func spnAuthorized(svc ServiceAccount, be Backend) bool {
	for _, spn := range svc.AllowedToDelegateTo {
		if strings.EqualFold(spn, be.SPN) {
			return true
		}
	}
	for _, principal := range be.AllowedToActOnBehalfOf {
		if strings.EqualFold(principal, svc.Name) {
			return true
		}
	}
	return false
}
