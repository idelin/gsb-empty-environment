package remoteattest

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"fmt"
)

// Finding severity / code constants reported by the policy engine.
const (
	FindingToMToU          = "TOMTOU_ZERO_HASH"
	FindingWhitelistMiss   = "WHITELIST_MISS"
	FindingSignatureBroken = "IMA_SIG_INVALID"
	FindingPCRDigest       = "PCR_DIGEST_MISMATCH"
	FindingPCRSelection    = "PCR_SELECTION_MISSING"
)

var (
	// ErrPolicyViolation wraps one or more policy findings. The verifier
	// always treats a finding as platform-insecure.
	ErrPolicyViolation = NewPolicyError("remoteattest: attestation policy violation")
	// ErrPCRDigestMismatch is returned when the replayed PCR digest does not
	// equal the signed quote pcrDigest.
	ErrPCRDigestMismatch = NewPolicyError("remoteattest: replayed PCR digest does not match signed quote")
)

// PolicyError carries the ordered findings produced during verification.
type PolicyError struct {
	message  string
	Findings []Finding
}

// NewPolicyError creates an empty PolicyError with the given message.
func NewPolicyError(message string) *PolicyError {
	return &PolicyError{message: message}
}

// Error implements the error interface.
func (e *PolicyError) Error() string {
	if len(e.Findings) == 0 {
		return e.message
	}
	return fmt.Sprintf("%s: %d finding(s), first=%s/%s", e.message, len(e.Findings), e.Findings[0].Code, e.Findings[0].Path)
}

// Finding describes one policy failure for a single IMA entry (or the quote).
type Finding struct {
	Code   string
	Path   string
	Detail string
}

// Whitelist is the trusted executable baseline. AllowedDigests maps the
// hex-encoded SHA-256 file digest to the set of paths allowed to carry it.
// An empty path set allows the digest on any path.
type Whitelist struct {
	AllowedDigests map[string][]string
}

// Contains reports whether digest/path is covered by the baseline.
func (w *Whitelist) Contains(digest []byte, path string) bool {
	if w == nil {
		return false
	}
	paths, ok := w.AllowedDigests[hex.EncodeToString(digest)]
	if !ok {
		return false
	}
	if len(paths) == 0 {
		return true
	}
	for _, p := range paths {
		if p == path {
			return true
		}
	}
	return false
}

// Config bundles every verifier input.
type Config struct {
	AttestBlob    []byte
	SignatureBlob []byte
	IMALog        []byte
	ExpectedNonce []byte

	AKCert        *x509.Certificate
	Intermediates []*x509.Certificate
	Roots         *x509.CertPool

	// Baseline every measured executable must match.
	Baseline *Whitelist
	// ImaSigKeys verify ima-sig file signatures (ECDSA-P256 over the
	// SHA-256 file digest carried in d-ng).
	ImaSigKeys []*ecdsa.PublicKey

	// PCRBank is the reconstructed SHA-256 TPM PCR bank supplied by the
	// host integration: firmware/bootloader/kernel measurements for the
	// PCRs outside IMA (typically 0-9), while PCR 10 is overwritten by the
	// IMA replay. Every PCR selected by the quote must be present. When
	// nil, only PCRs actually extended by the IMA log are available.
	PCRBank map[uint32][]byte

	// RequiredPCRs lists the PCR indices the quote's pcrSelect must cover.
	// It defeats bitmap-shifting attacks that quote an unrelated/zero PCR
	// instead of the IMA measurement PCR. Defaults to {10} when nil.
	RequiredPCRs []uint32
}

// Report is the structured verification result.
type Report struct {
	Attestation *Attestation
	Replay      *ReplayResult
	Findings    []Finding
}

// Secure reports that no policy finding was produced.
func (r *Report) Secure() bool { return len(r.Findings) == 0 }

// Verify runs the full remote attestation verification pipeline:
//  1. decode/verify the signed quote and bind nonce + AK chain,
//  2. parse, hash-recompute and replay the IMA log,
//  3. compare the replayed PCR digest with the signed pcrDigest,
//  4. enforce the whitelist baseline and ima-sig file signatures,
//  5. flag any ToMToU/open-writers zero-hash entry as platform-insecure.
//
// A non-nil error is returned for hard parsing/crypto failures; policy
// failures are returned as *PolicyError with the populated Report.
func Verify(cfg *Config) (*Report, error) {
	if cfg == nil {
		return nil, fmt.Errorf("remoteattest: nil config")
	}

	att, err := VerifyQuote(cfg.AttestBlob, cfg.SignatureBlob, cfg.ExpectedNonce,
		cfg.AKCert, cfg.Intermediates, cfg.Roots)
	if err != nil {
		return nil, err
	}

	replay, err := ReplayIMALog(cfg.IMALog)
	if err != nil {
		return nil, err
	}

	report := &Report{Attestation: att, Replay: replay}
	policyErr := NewPolicyError(ErrPolicyViolation.Error())

	required := cfg.RequiredPCRs
	if required == nil {
		required = []uint32{10}
	}
	for _, pcr := range required {
		if !att.PCRSelection.Contains(pcr) {
			report.Findings = append(report.Findings, Finding{
				Code:   FindingPCRSelection,
				Detail: fmt.Sprintf("quote pcrSelect does not cover required PCR %d", pcr),
			})
		}
	}

	bank := make(map[uint32][]byte)
	for idx, v := range cfg.PCRBank {
		bank[idx] = v
	}
	// IMA replay values are authoritative for the PCRs the log extends.
	for idx, v := range replay.PCRs {
		bank[idx] = v
	}

	computedDigest, err := PCRDigest(bank, att.PCRSelection)
	if err != nil {
		return report, err
	}
	if !equalBytes(computedDigest, att.PCRDigest) {
		report.Findings = append(report.Findings, Finding{
			Code:   FindingPCRDigest,
			Detail: fmt.Sprintf("replayed=%x signed=%x", computedDigest, att.PCRDigest),
		})
	}

	for _, entry := range replay.Entries {
		if entry.ZeroHash {
			report.Findings = append(report.Findings, Finding{
				Code:   FindingToMToU,
				Path:   entry.Path,
				Detail: "all-zero template/file hash: open-writers ToMToU violation, extended 0xFF..FF",
			})
		}

		if !cfg.Baseline.Contains(entry.FileDigest, entry.Path) {
			report.Findings = append(report.Findings, Finding{
				Code:   FindingWhitelistMiss,
				Path:   entry.Path,
				Detail: "file digest not present in trusted baseline: " + hex.EncodeToString(entry.FileDigest),
			})
		}

		if entry.TemplateName == TemplateIMASIG {
			if err := verifyImaSignature(entry, cfg.ImaSigKeys); err != nil {
				report.Findings = append(report.Findings, Finding{
					Code:   FindingSignatureBroken,
					Path:   entry.Path,
					Detail: err.Error(),
				})
			}
		}
	}

	if !report.Secure() {
		policyErr.Findings = report.Findings
		return report, policyErr
	}
	return report, nil
}

// verifyImaSignature checks the ECDSA-P256 signature carried by an ima-sig
// entry against the SHA-256 file digest, accepting any trusted key.
func verifyImaSignature(entry *IMAEntry, keys []*ecdsa.PublicKey) error {
	if len(entry.Signature) == 0 {
		return fmt.Errorf("ima-sig entry has no signature payload")
	}
	if len(keys) == 0 {
		return fmt.Errorf("no trusted ima-sig verification keys configured")
	}
	digest := sha256.Sum256(entry.FileDigest)
	for _, key := range keys {
		if key == nil || key.Curve == nil {
			continue
		}
		if ecdsa.VerifyASN1(key, digest[:], entry.Signature) {
			return nil
		}
	}
	return fmt.Errorf("signature not valid under any trusted key")
}
