package netsec

import "fmt"

// RefreshTokenRecord is the server-side record associated with a refresh
// token. CertificateFingerprint is the x5t#S256 recorded when the refresh
// token was issued; an empty value means the refresh token was not
// certificate-bound.
type RefreshTokenRecord struct {
	CertificateFingerprint string
}

// RotationPair identifies an explicitly authorized certificate change.
type RotationPair struct {
	Old string
	New string
}

// RotationTable is the caller's allow-list of (old fingerprint, new
// fingerprint) pairs. A pair must be present for a certificate-bound refresh
// token to be redeemed with a different client certificate.
type RotationTable map[RotationPair]struct{}

// Allows reports whether the exact old-to-new rotation pair is registered.
// A nil table allows nothing.
func (t RotationTable) Allows(oldFingerprint, newFingerprint string) bool {
	if t == nil {
		return false
	}
	_, ok := t[RotationPair{Old: oldFingerprint, New: newFingerprint}]
	return ok
}

// Add registers an old-to-new certificate rotation pair.
func (t RotationTable) Add(oldFingerprint, newFingerprint string) {
	t[RotationPair{Old: oldFingerprint, New: newFingerprint}] = struct{}{}
}

// ValidateRefreshBinding authorizes redemption of a refresh token and returns
// the cnf to place in the newly issued access token.
//
// The new binding always comes from the client certificate on this connection.
// If the refresh token record has a fingerprint, the connection's certificate
// must produce the same fingerprint. A different certificate is allowed only
// when grants contains the (old, new) pair.
func ValidateRefreshBinding(record RefreshTokenRecord, conn Connection, grants RotationTable) (Cnf, error) {
	oldFingerprint := record.CertificateFingerprint
	if oldFingerprint != "" {
		if err := ValidateX5TS256(oldFingerprint); err != nil {
			return Cnf{}, fmt.Errorf("refresh token record: %w", err)
		}
	}

	if !conn.HasClientCertificate() {
		if oldFingerprint == "" {
			// The refresh token itself was not bound; the resulting access
			// token is likewise unbound.
			return Cnf{}, nil
		}
		return Cnf{}, ErrNoClientCertificate
	}

	newFingerprint, err := conn.CertificateFingerprint()
	if err != nil {
		return Cnf{}, err
	}
	if oldFingerprint == "" {
		return Cnf{X5TS256: newFingerprint}, nil
	}

	match, err := fingerprintsEqual(oldFingerprint, newFingerprint)
	if err != nil {
		return Cnf{}, err
	}
	if !match && !grants.Allows(oldFingerprint, newFingerprint) {
		return Cnf{}, fmt.Errorf("%w: old %s, new %s", ErrRotationNotGranted, oldFingerprint, newFingerprint)
	}
	return Cnf{X5TS256: newFingerprint}, nil
}

// RefreshBinding is an alias for ValidateRefreshBinding.
func RefreshBinding(record RefreshTokenRecord, conn Connection, grants RotationTable) (Cnf, error) {
	return ValidateRefreshBinding(record, conn, grants)
}
