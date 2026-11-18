// Package netsec implements parent-side acceptance of child CDS/CDNSKEY
// signals.
//
// A candidate is accepted only when every authoritative server returns the
// same CDS set, the RRsets are signed by a KSK matching the parent's current
// DS, the candidate has been observed continuously for the configured period,
// CDS and CDNSKEY records correspond to each other, and every proposed DS
// matches a published, authenticated DNSKEY. The special CDS record
// "0 0 0 00" requests DS deletion and must be the only CDS record present.
package netsec
