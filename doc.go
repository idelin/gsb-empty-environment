// Package netsec provides a cache of caller-verified NSEC denial records for
// one DNS zone.
//
// Names are canonicalized to lower-case labels and ordered using DNSSEC
// canonical name order (root-side labels first).  Records are accepted only
// with a verification receipt bound to the validated record fields.  The cache
// computes an NSEC record's lifetime as the earlier of insertion time plus the
// original TTL and the RRSIG expiry time.
//
// Query reports NoData for an exact NSEC owner whose bitmap lacks the requested
// type, and for a proven empty non-terminal (a covering NSEC whose next name is
// a strict descendant).  NXDomain requires a strict covering NSEC for the query
// name and another strict covering proof for *.<closest-encloser>.  The closest
// encloser is found only from current existence evidence (NSEC owners, NSEC
// next names, and their ancestors), never by guessing from a denial interval.
// If the available evidence is incomplete, Query returns NeedUpstream.
package netsec
