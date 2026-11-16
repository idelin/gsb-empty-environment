// Package netsec implements SRv6 Segment Routing Header (SRH) admission
// checks for a forwarding-plane trust-domain boundary, following RFC 8754.
//
// AdmitExternalSRH is the main entry point.  It walks the IPv6 extension
// header chain, and if an SRH is present, requires a valid HMAC-SHA256 TLV
// with a nonzero Key ID before allowing the packet into the domain.
package netsec
