// Package netsec implements a double opt-in subscription guard for mailing
// lists. Confirmation tokens are bound to an address, list, requester and
// expiry, are consumed at most once, and never authorize delivery by
// themselves. Complaints and unsubscribes revoke memberships and pending
// confirmations so that callers can gate every send through AuthorizeDelivery.
package netsec
