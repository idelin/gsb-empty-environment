package workloadid

import (
	"crypto/x509"
	"errors"
	"math/big"

	"prescreen.local/ps-174fa6fd5f/internal/ocsp"
)

// Revocation errors.
var (
	ErrCertRevoked          = errors.New("workloadid: certificate is revoked")
	ErrRevocationStale      = errors.New("workloadid: revocation information is stale")
	ErrRevocationGrace      = errors.New("workloadid: revocation information is within grace window")
	ErrRevocationRolledBack = errors.New("workloadid: revocation timestamp moved backwards")
	ErrOCSPUnknown          = errors.New("workloadid: OCSP responder returned unknown status")
	ErrCRLMissing           = errors.New("workloadid: CRL information unavailable")
)

// RevocationConfig controls grace windows and soft-fail behavior.
type RevocationConfig struct {
	// OCSPGrace is the time after NextUpdate during which an OCSP response is
	// still honoured while a refreshed response is being fetched.
	OCSPGrace Duration
	// CRLGrace is the equivalent grace window after a CRL NextUpdate.
	CRLGrace Duration
	// UnknownIsRevoked treats an OCSP "unknown" status as a hard denial.
	UnknownIsRevoked bool
	// RequireRevocation makes missing revocation data a hard denial instead of
	// a soft allow.
	RequireRevocation bool
	// MaxSkew bounds how far in the future revocation timestamps may appear
	// before the response is treated as a clock-rollback attack.
	MaxSkew Duration
}

// OCSPRecord is a pre-validated OCSP response bound to a certificate.
type OCSPRecord struct {
	// RawDER is the signed OCSP response.
	RawDER []byte
	// Responder is the trusted certificate that signs the response. It must
	// chain to the same trust bundle as the workload certificate.
	Responder *x509.Certificate
}

// CRLRecord is a certificate revocation list plus its issuing CA.
type CRLRecord struct {
	// RawDER is the DER-encoded X.509 v2 CRL.
	RawDER []byte
	// Issuer is the CA certificate that signed the CRL.
	Issuer *x509.Certificate
}

// ParsedCRL retains the validated CRL and its parsed entries.
type ParsedCRL struct {
	List *x509.RevocationList
}

// RevocationState is the immutable, generation-tagged revocation material held
// inside a Snapshot.
type RevocationState struct {
	// OCSP maps certificate serial number (decimal string) to its record.
	OCSP map[string]OCSPRecord
	// CRLs maps issuer subject (DER encoded) to its validated CRL.
	CRLs map[string]CRLRecord
	// parsedCRLs is populated when the snapshot is built.
	parsedCRLs map[string]*ParsedCRL
}

// build prepares cached CRL parses and verifies signatures up front so a bundle
// generation cannot be activated with malformed revocation material.
func (r *RevocationState) build() error {
	r.parsedCRLs = make(map[string]*ParsedCRL, len(r.CRLs))
	for key, record := range r.CRLs {
		list, err := x509.ParseRevocationList(record.RawDER)
		if err != nil {
			return err
		}
		if err := list.CheckSignatureFrom(record.Issuer); err != nil {
			return err
		}
		r.parsedCRLs[key] = &ParsedCRL{List: list}
	}
	return nil
}

func serialKey(serial *big.Int) string {
	if serial == nil {
		return ""
	}
	return serial.String()
}

func issuerKey(cert *x509.Certificate) string {
	return string(cert.RawIssuer)
}

// checkRevocation evaluates one certificate against the current revocation
// state. issuer is the certificate that signed cert (used to validate OCSP
// certID and select the CRL); for a self-signed root it equals cert. now is
// the verifier clock; lastObserved tracks the freshest revocation timestamp
// seen for the chain to detect clock rollback.
func checkRevocation(cert, issuer *x509.Certificate, state *RevocationState, cfg RevocationConfig, now Time, lastObserved *Time) error {
	if state == nil {
		if cfg.RequireRevocation {
			return ErrCRLMissing
		}
		return nil
	}

	// Clock-rollback interception: revocation evidence must never age relative
	// to previously observed evidence within the same verification.
	if !lastObserved.IsZero() && now.Before(*lastObserved) {
		return ErrRevocationRolledBack
	}

	if record, ok := state.OCSP[serialKey(cert.SerialNumber)]; ok {
		return checkOCSP(issuer, cert, record, cfg, now, lastObserved)
	}
	return checkCRL(cert, state, cfg, now, lastObserved)
}

func checkOCSP(issuer, cert *x509.Certificate, record OCSPRecord, cfg RevocationConfig, now Time, lastObserved *Time) error {
	resp, err := ocsp.Verify(record.RawDER, issuer, record.Responder)
	if err != nil {
		return err
	}

	// Rollback defense on the response timestamps.
	if !resp.ProducedAt.IsZero() && resp.ProducedAt.After(now.Add(cfg.MaxSkew)) {
		return ErrRevocationRolledBack
	}
	if !resp.ThisUpdate.IsZero() && resp.ThisUpdate.After(now.Add(cfg.MaxSkew)) {
		return ErrRevocationRolledBack
	}
	advance(lastObserved, resp.ProducedAt)

	switch resp.Status {
	case ocsp.Revoked:
		return ErrCertRevoked
	case ocsp.Unknown:
		if cfg.UnknownIsRevoked {
			return ErrOCSPUnknown
		}
	case ocsp.Good:
	}

	if !resp.NextUpdate.IsZero() {
		expiry := resp.NextUpdate.Add(cfg.OCSPGrace)
		if now.After(expiry) {
			return ErrRevocationStale
		}
		if now.After(resp.NextUpdate) {
			return ErrRevocationGrace
		}
	}
	return nil
}

func checkCRL(cert *x509.Certificate, state *RevocationState, cfg RevocationConfig, now Time, lastObserved *Time) error {
	key := issuerKey(cert)
	parsed, ok := state.parsedCRLs[key]
	if !ok {
		if cfg.RequireRevocation {
			return ErrCRLMissing
		}
		return nil
	}
	list := parsed.List
	advance(lastObserved, list.ThisUpdate)

	if list.ThisUpdate.After(now.Add(cfg.MaxSkew)) {
		return ErrRevocationRolledBack
	}

	for _, entry := range list.RevokedCertificateEntries {
		if entry.SerialNumber.Cmp(cert.SerialNumber) == 0 {
			return ErrCertRevoked
		}
	}

	if !list.NextUpdate.IsZero() {
		expiry := list.NextUpdate.Add(cfg.CRLGrace)
		if now.After(expiry) {
			return ErrRevocationStale
		}
		if now.After(list.NextUpdate) {
			return ErrRevocationGrace
		}
	}
	return nil
}

func advance(last *Time, candidate Time) {
	if candidate.IsZero() {
		return
	}
	if last.IsZero() || candidate.After(*last) {
		*last = candidate
	}
}
