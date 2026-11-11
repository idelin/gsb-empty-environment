package sigv4chunk

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"
)

// KeyProvider resolves the signing secret for an access key. A missing key
// must return ("", false, nil); transport problems may return an error.
type KeyProvider func(accessKey string) (secret string, ok bool, err error)

// Config configures the SigV4 aws-chunked verification middleware.
type Config struct {
	// Keys resolves access keys to their signing secrets. Required.
	Keys KeyProvider
	// Now supplies the current time; nil uses time.Now. Override in tests.
	Now func() time.Time
	// MaxSkew bounds the accepted difference between server time and
	// X-Amz-Date. Defaults to 15 minutes, matching the SigV4 guidance.
	MaxSkew time.Duration
	// MaxDecodedLength caps the total accepted plaintext size. Zero means
	// unlimited; the request may still declare its own length.
	MaxDecodedLength int64
	// AllowedRegion/AllowedService restrict the credential scope. Empty
	// values accept any region or service.
	AllowedRegion  string
	AllowedService string
}

// Middleware returns an http.Handler wrapper that only forwards requests
// whose seed signature and every aws-chunked block signature verify. On any
// failure the downstream handler is never invoked, so it can never observe
// unverified data. Successful requests receive the decoded plaintext body and
// a corrected ContentLength; the chunk framing headers are stripped.
func Middleware(cfg Config, next http.Handler) http.Handler {
	if cfg.Keys == nil {
		panic("sigv4: Config.Keys is required")
	}
	if cfg.MaxSkew == 0 {
		cfg.MaxSkew = 15 * time.Minute
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := verifyStreamingRequest(cfg, r); err != nil {
			status := http.StatusForbidden
			if errors.Is(err, errInternal) || errors.Is(err, errBodyTooLarge) {
				status = http.StatusInternalServerError
				if errors.Is(err, errBodyTooLarge) {
					status = http.StatusRequestEntityTooLarge
				}
			} else if errors.Is(err, errBadRequest) {
				status = http.StatusBadRequest
			}
			http.Error(w, "Signature verification failed", status)
			return
		}
		next.ServeHTTP(w, r)
	})
}

var (
	errInternal      = errors.New("sigv4: internal error")
	errBadRequest    = errors.New("sigv4: malformed request")
	errBodyTooLarge  = errors.New("sigv4: decoded body exceeds configured limit")
	errNotChunked    = errors.New("sigv4: x-amz-content-sha256 is not the streaming marker")
	errScopeMismatch = errors.New("sigv4: credential scope does not match allowed region/service")
)

func verifyStreamingRequest(cfg Config, r *http.Request) error {
	if r.Header.Get(amzContentSHA256) != streamingPayload {
		return errors.Join(errBadRequest, errNotChunked)
	}

	auth, err := parseAuthorization(r.Header.Get("Authorization"))
	if err != nil {
		return err
	}
	if cfg.AllowedRegion != "" && auth.region != cfg.AllowedRegion {
		return errScopeMismatch
	}
	if cfg.AllowedService != "" && auth.service != cfg.AllowedService {
		return errScopeMismatch
	}

	mandatory := map[string]bool{
		amzContentSHA256: true,
		amzDateHeader:    true,
		amzDecodedLength: true,
	}
	if err := ensureSignedHeaders(r, auth.signedHeaders, mandatory); err != nil {
		return err
	}
	if err := validateAmzDate(r, auth.date, cfg.Now(), cfg.MaxSkew); err != nil {
		return err
	}

	secret, ok, err := cfg.Keys(auth.accessKey)
	if err != nil {
		return errors.Join(errInternal, err)
	}
	if !ok {
		return errSignatureMismatch
	}

	declared, err := strconv.ParseInt(r.Header.Get(amzDecodedLength), 10, 64)
	if err != nil || declared < 0 {
		return errBadRequest
	}
	if cfg.MaxDecodedLength > 0 && declared > cfg.MaxDecodedLength {
		return errBodyTooLarge
	}

	// The seed signature must verify before any chunk data is read.
	if err := verifySeedSignature(r, auth, secret, streamingPayload); err != nil {
		return err
	}

	key := signingKey(secret, auth.date, auth.region, auth.service)
	verifier := newChunkVerifier(r.Body, r.Header.Get(amzDateHeader), auth.credentialScope(), key, auth.signature, declared)
	plaintext, err := verifier.verifyAll()
	if err != nil {
		return err
	}

	// Only fully verified plaintext is exposed downstream.
	r.Body = io.NopCloser(bytes.NewReader(plaintext))
	r.ContentLength = int64(len(plaintext))
	r.Header.Del(amzContentSHA256)
	r.Header.Del(amzDecodedLength)
	return nil
}
