// Package integrity binds decoded Play Integrity verdicts to the request
// they authorize, so tokens minted for unrelated requests cannot be
// forwarded to sensitive endpoints and old tokens cannot be replayed.
package integrity

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

// DefaultMaxClockSkew is the maximum allowed difference between the
// verdict's timestampMillis and the server clock.
const DefaultMaxClockSkew = 60 * time.Second

// Verdict is the decoded payload of a Play Integrity token.
type Verdict struct {
	RequestDetails struct {
		RequestPackageName string `json:"requestPackageName"`
		RequestHash        string `json:"requestHash"`
		TimestampMillis    int64  `json:"timestampMillis"`
	} `json:"requestDetails"`
	AppIntegrity struct {
		AppRecognitionVerdict   string   `json:"appRecognitionVerdict"`
		PackageName             string   `json:"packageName"`
		CertificateSha256Digest []string `json:"certificateSha256Digest"`
	} `json:"appIntegrity"`
	DeviceIntegrity struct {
		DeviceRecognitionVerdict []string `json:"deviceRecognitionVerdict"`
	} `json:"deviceIntegrity"`
}

var (
	ErrRequestHashMismatch = errors.New("integrity: requestHash does not match request body")
	ErrPackageMismatch     = errors.New("integrity: package name mismatch")
	ErrTimestampSkew       = errors.New("integrity: verdict timestamp outside allowed clock skew")
	ErrReplay              = errors.New("integrity: verdict already used (replay)")
	ErrAppNotRecognized    = errors.New("integrity: appRecognitionVerdict is not PLAY_RECOGNIZED")
	ErrCertDigest          = errors.New("integrity: no registered certificate digest present")
	ErrDeviceIntegrity     = errors.New("integrity: MEETS_DEVICE_INTEGRITY missing")
)

// Verifier checks decoded Play Integrity verdicts against the request
// being authorized.
type Verifier struct {
	packageName string
	certDigests map[string]struct{}
	maxSkew     time.Duration
	now         func() time.Time

	mu   sync.Mutex
	seen map[seenKey]struct{}
}

type seenKey struct {
	requestHash     string
	timestampMillis int64
}

// Option customizes a Verifier.
type Option func(*Verifier)

// WithMaxClockSkew overrides the allowed timestamp skew (default 60s).
func WithMaxClockSkew(d time.Duration) Option {
	return func(v *Verifier) { v.maxSkew = d }
}

// WithClock overrides the time source (for testing).
func WithClock(now func() time.Time) Option {
	return func(v *Verifier) { v.now = now }
}

// NewVerifier builds a Verifier for the given application package name
// and the set of registered signing-certificate SHA-256 digests
// (uppercase hex, as reported by the Play Integrity API).
func NewVerifier(packageName string, certDigests []string, opts ...Option) *Verifier {
	v := &Verifier{
		packageName: packageName,
		certDigests: make(map[string]struct{}, len(certDigests)),
		maxSkew:     DefaultMaxClockSkew,
		now:         time.Now,
		seen:        make(map[seenKey]struct{}),
	}
	for _, d := range certDigests {
		v.certDigests[d] = struct{}{}
	}
	for _, opt := range opts {
		opt(v)
	}
	return v
}

// RequestHash computes the value that must appear in
// requestDetails.requestHash for the given request body: URL-safe
// base64 without padding of the body's SHA-256 digest.
func RequestHash(body []byte) string {
	sum := sha256.Sum256(body)
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// Verify decodes the verdict JSON and enforces all binding checks
// against the raw request body being authorized. On success the
// (requestHash, timestampMillis) pair is consumed and any subsequent
// verification of the same pair fails with ErrReplay.
func (v *Verifier) Verify(body, verdictJSON []byte) error {
	var verdict Verdict
	if err := json.Unmarshal(verdictJSON, &verdict); err != nil {
		return fmt.Errorf("integrity: decode verdict: %w", err)
	}
	return v.VerifyVerdict(body, &verdict)
}

// VerifyVerdict enforces all binding checks on an already-decoded verdict.
func (v *Verifier) VerifyVerdict(body []byte, verdict *Verdict) error {
	if verdict.RequestDetails.RequestHash != RequestHash(body) {
		return ErrRequestHashMismatch
	}
	if verdict.RequestDetails.RequestPackageName != v.packageName ||
		verdict.AppIntegrity.PackageName != v.packageName {
		return ErrPackageMismatch
	}

	ts := time.UnixMilli(verdict.RequestDetails.TimestampMillis)
	skew := v.now().Sub(ts)
	if skew < 0 {
		skew = -skew
	}
	if skew > v.maxSkew {
		return ErrTimestampSkew
	}

	if verdict.AppIntegrity.AppRecognitionVerdict != "PLAY_RECOGNIZED" {
		return ErrAppNotRecognized
	}

	certOK := false
	for _, d := range verdict.AppIntegrity.CertificateSha256Digest {
		if _, ok := v.certDigests[d]; ok {
			certOK = true
			break
		}
	}
	if !certOK {
		return ErrCertDigest
	}

	deviceOK := false
	for _, dv := range verdict.DeviceIntegrity.DeviceRecognitionVerdict {
		if dv == "MEETS_DEVICE_INTEGRITY" {
			deviceOK = true
			break
		}
	}
	if !deviceOK {
		return ErrDeviceIntegrity
	}

	v.mu.Lock()
	defer v.mu.Unlock()
	key := seenKey{
		requestHash:     verdict.RequestDetails.RequestHash,
		timestampMillis: verdict.RequestDetails.TimestampMillis,
	}
	if _, ok := v.seen[key]; ok {
		return ErrReplay
	}
	v.seen[key] = struct{}{}
	return nil
}
