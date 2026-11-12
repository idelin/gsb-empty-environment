// Package netsec provides post-handshake DANE TLSA validation for TLS
// delivery clients.  It implements the DANE-EE and DANE-TA certificate usages
// from RFC 6698, with both TLSA selectors and the Full, SHA-256 and SHA-512
// matching types.
//
// The caller is responsible for obtaining and DNSSEC-validating the TLSA
// record before handing it to this package.  VerifyConnection deliberately
// performs only the DANE association check: DANE-EE does not consult certificate
// names or validity periods, while DANE-TA requires a matching certificate in
// the verified (or, when unavailable, presented) chain.
package netsec
