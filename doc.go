// Package netsec validates X.509 certificate policy processing for a
// certificate path.
//
// Paths are ordered from the end-entity certificate to the trust anchor, as
// with crypto/x509. The policy processor implements the algorithm from RFC
// 5280 as updated by RFC 9618. In particular, when the caller requires an
// explicit policy, a path whose final policy set is empty is rejected.
package netsec
