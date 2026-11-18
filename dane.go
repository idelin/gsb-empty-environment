package netsec

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"strings"
)

var (
	// ErrNoTLSARecords means no supplied TLSA record had fields this
	// package implements, so nothing could be evaluated.
	ErrNoTLSARecords = errors.New("netsec: no usable TLSA records")
	// ErrNoMatch means every usable TLSA record was evaluated and none
	// matched the presented certificate chain.
	ErrNoMatch = errors.New("netsec: no TLSA record matched the certificate chain")
)

// VerifyConnection runs DANE verification against the certificates captured
// from a completed TLS handshake. The TLSA records must already have passed
// DNSSEC validation. It is a convenience wrapper around VerifyChain using
// tls.ConnectionState.PeerCertificates (the verified chain is intentionally not
// required: DANE-EE never relies on the platform verifier).
func VerifyConnection(state tls.ConnectionState, records []TLSARecord) error {
	return VerifyChain(state.PeerCertificates, records)
}

// VerifyChain performs DANE verification for a chain presented by the peer.
// peerCertificates is state.PeerCertificates from crypto/tls: the leaf first,
// followed by any intermediates. The records are the DNSSEC-validated TLSA
// record set for the target name.
//
// DANE-EE (usage 3) matches only the leaf certificate and performs no DNS
// name or validity-period check. DANE-TA (usage 2) requires a matching CA
// certificate in the chain and only accepts a leaf that builds a path to that
// trust anchor. When several records are present, any single match passes and
// the chain is rejected only when every usable record fails.
func VerifyChain(peerCertificates []*x509.Certificate, records []TLSARecord) error {
	if len(peerCertificates) == 0 {
		return errors.New("netsec: peer presented no certificates")
	}

	leaf := peerCertificates[0]
	usable := 0
	var failures []string

	for _, record := range records {
		if !record.usable() {
			continue
		}
		usable++

		switch record.Usage {
		case UsageDANEEE:
			if MatchCertificate(leaf, record) {
				return nil
			}
			failures = append(failures, fmt.Sprintf(
				"DANE-EE selector=%d matching=%d: leaf certificate mismatch",
				record.Selector, record.MatchingType))
		case UsageDANETA:
			if err := verifyTrustAnchor(peerCertificates, record); err != nil {
				failures = append(failures, err.Error())
			} else {
				return nil
			}
		}
	}

	if usable == 0 {
		return ErrNoTLSARecords
	}
	return fmt.Errorf("%w (%d record(s) evaluated): %s",
		ErrNoMatch, usable, strings.Join(failures, "; "))
}

// verifyTrustAnchor enforces DANE-TA semantics. The matching certificate must
// be a CA present in the chain (the leaf end-entity cert is never a trust
// anchor), and the leaf must validate through a path rooted at that anchor.
// Anchoring the pool at the matched certificate prevents a peer from
// bypassing the pinned TA by presenting some other valid chain.
func verifyTrustAnchor(chain []*x509.Certificate, record TLSARecord) error {
	leaf := chain[0]

	var anchor *x509.Certificate
	for _, cert := range chain[1:] {
		if !cert.IsCA {
			continue
		}
		if MatchCertificate(cert, record) {
			anchor = cert
			break
		}
	}
	if anchor == nil {
		return fmt.Errorf("DANE-TA selector=%d matching=%d: no matching CA trust anchor in chain",
			record.Selector, record.MatchingType)
	}

	roots := x509.NewCertPool()
	roots.AddCert(anchor)

	intermediates := x509.NewCertPool()
	for _, cert := range chain[1:] {
		if cert.Equal(anchor) {
			continue
		}
		intermediates.AddCert(cert)
	}

	opts := x509.VerifyOptions{
		Roots:         roots,
		Intermediates: intermediates,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	}
	if _, err := leaf.Verify(opts); err != nil {
		return fmt.Errorf("DANE-TA selector=%d matching=%d: leaf does not chain to pinned anchor: %w",
			record.Selector, record.MatchingType, err)
	}
	return nil
}
