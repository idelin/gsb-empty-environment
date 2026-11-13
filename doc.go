// Package netsec parses and enforces the TLS certificate_authorities
// extension. If the extension is present, a leaf certificate is usable only
// when its DER-encoded issuer exactly matches one of the advertised
// distinguished names. Matching certificates still undergo normal X.509 path
// validation.
package netsec
