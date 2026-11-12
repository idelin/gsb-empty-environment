package netsec

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// MaxCreatedSkew is the maximum permitted absolute difference between the
// verifier clock and the signature's created parameter.
const MaxCreatedSkew = 60 * time.Second

// Result describes the signature that successfully authenticated the request.
type Result struct {
	SignatureLabel    string
	KeyID             string
	Algorithm         Algorithm
	Created           time.Time
	Expires           *time.Time
	CoveredComponents []string
}

type verifyConfig struct {
	now            time.Time
	fixedNow       bool
	maxCreatedSkew time.Duration
	targetURI      string
	body           []byte
	bodySet        bool
}

// Option configures request verification.
type Option func(*verifyConfig) error

// WithNow fixes the verifier clock, which is useful for deterministic gateway
// tests. Without it, verification uses time.Now.
func WithNow(now time.Time) Option {
	return func(config *verifyConfig) error {
		if now.IsZero() {
			return fmt.Errorf("netsec: WithNow requires a non-zero time")
		}
		config.now = now
		config.fixedNow = true
		return nil
	}
}

// WithMaxCreatedSkew lowers the default 60-second created timestamp tolerance.
// Values above MaxCreatedSkew are rejected so callers cannot weaken the
// package's required replay window.
func WithMaxCreatedSkew(skew time.Duration) Option {
	return func(config *verifyConfig) error {
		if skew <= 0 || skew > MaxCreatedSkew {
			return fmt.Errorf("netsec: max created skew must be in (0, %s]", MaxCreatedSkew)
		}
		if skew%time.Second != 0 {
			return fmt.Errorf("netsec: max created skew must be a whole number of seconds")
		}
		config.maxCreatedSkew = skew
		return nil
	}
}

// WithTargetURI supplies the externally visible absolute target URI when the
// gateway receives a relative request target. The gateway should derive this
// from trusted proxy configuration, not from attacker-controlled forwarding
// headers.
func WithTargetURI(targetURI string) Option {
	return func(config *verifyConfig) error {
		if targetURI == "" {
			return fmt.Errorf("netsec: WithTargetURI requires a non-empty URI")
		}
		if strings.ContainsAny(targetURI, "\r\n") {
			return fmt.Errorf("%w: target URI", ErrHeaderValue)
		}
		config.targetURI = targetURI
		return nil
	}
}

// WithBody supplies an already-read body instead of consuming req.Body. Use
// []byte{} for a known empty body.
func WithBody(body []byte) Option {
	return func(config *verifyConfig) error {
		config.body = append([]byte(nil), body...)
		config.bodySet = true
		return nil
	}
}

func newVerifyConfig(options ...Option) (verifyConfig, error) {
	config := verifyConfig{
		maxCreatedSkew: MaxCreatedSkew,
	}
	for _, option := range options {
		if option == nil {
			continue
		}
		if err := option(&config); err != nil {
			return verifyConfig{}, err
		}
	}
	return config, nil
}

func (c verifyConfig) currentTime() time.Time {
	if c.fixedNow {
		return c.now
	}
	return time.Now()
}

// Verifier holds a trusted key registry and reusable verification options.
type Verifier struct {
	registry KeyRegistry
	config   verifyConfig
}

// NewVerifier creates a reusable verifier.
func NewVerifier(registry KeyRegistry, options ...Option) (*Verifier, error) {
	if registry == nil {
		return nil, fmt.Errorf("%w: nil key registry", ErrInvalidKey)
	}
	config, err := newVerifyConfig(options...)
	if err != nil {
		return nil, err
	}
	return &Verifier{registry: registry, config: config}, nil
}

// VerifyRequest validates one RFC 9421 request signature.
func VerifyRequest(request *http.Request, registry KeyRegistry, options ...Option) (*Result, error) {
	if registry == nil {
		return nil, fmt.Errorf("%w: nil key registry", ErrInvalidKey)
	}
	config, err := newVerifyConfig(options...)
	if err != nil {
		return nil, err
	}
	return verifyRequest(request, registry, config)
}

// VerifyRequest validates one RFC 9421 request signature with the verifier's
// configured registry and options.
func (v *Verifier) VerifyRequest(request *http.Request) (*Result, error) {
	if v == nil {
		return nil, fmt.Errorf("%w: nil verifier", ErrInvalidKey)
	}
	return verifyRequest(request, v.registry, v.config)
}

func verifyRequest(request *http.Request, registry KeyRegistry, config verifyConfig) (*Result, error) {
	if request == nil || request.URL == nil {
		return nil, fmt.Errorf("%w: nil request", ErrMalformedSignature)
	}

	inputsValues, hasInputs, err := requestHeaderValues(request.Header, "Signature-Input")
	if err != nil {
		return nil, err
	}
	outputsValues, hasOutputs, err := requestHeaderValues(request.Header, "Signature")
	if err != nil {
		return nil, err
	}
	if !hasInputs || !hasOutputs {
		return nil, ErrMissingSignature
	}

	inputs, err := parseSignatureInputs(inputsValues)
	if err != nil {
		return nil, err
	}
	signatures, err := parseSignatureValues(outputsValues)
	if err != nil {
		return nil, err
	}

	body, err := requestBody(request, config)
	if err != nil {
		return nil, err
	}
	if err := verifyContentDigest(request, body); err != nil {
		return nil, err
	}
	targetURI, err := requestTargetURI(request, config.targetURI)
	if err != nil {
		return nil, err
	}

	labels := make([]string, 0, len(inputs))
	for label := range inputs {
		labels = append(labels, label)
	}
	sort.Strings(labels)

	var verificationErrors []error
	for _, label := range labels {
		input := inputs[label]
		signature, ok := signatures[label]
		if !ok {
			verificationErrors = append(verificationErrors, fmt.Errorf(
				"%w: signature label %q has no Signature value", ErrMalformedSignature, label))
			continue
		}
		result, err := verifySingleSignature(request, registry, config, input, signature, body, targetURI)
		if err == nil {
			return result, nil
		}
		verificationErrors = append(verificationErrors, err)
	}
	if len(verificationErrors) == 0 {
		return nil, ErrMissingSignature
	}
	return nil, errors.Join(verificationErrors...)
}

func verifySingleSignature(
	request *http.Request,
	registry KeyRegistry,
	config verifyConfig,
	input signatureInput,
	signature []byte,
	body []byte,
	targetURI string,
) (*Result, error) {
	keyID, hasKeyID, err := stringParameter(input.params, "keyid")
	if err != nil {
		return nil, err
	}
	if !hasKeyID || keyID == "" {
		return nil, fmt.Errorf("%w: keyid is required", ErrMalformedSignature)
	}

	registered, ok := registry.Lookup(keyID)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownKey, keyID)
	}
	if err := validateKey(registered.Algorithm, registered.Key); err != nil {
		return nil, err
	}

	claimedAlgorithm, hasAlgorithm, err := stringParameter(input.params, "alg")
	if err != nil {
		return nil, err
	}
	if hasAlgorithm && Algorithm(claimedAlgorithm) != registered.Algorithm {
		return nil, fmt.Errorf("%w: request claims %q, registry says %q",
			ErrAlgorithmMismatch, claimedAlgorithm, registered.Algorithm)
	}

	covered := make(map[string]struct{})
	coveredNames := make([]string, 0, len(input.components))
	for _, component := range input.components {
		name := normalizedComponentName(component.name)
		if len(component.params) != 0 {
			return nil, fmt.Errorf(
				"%w: unsupported parameters on component %q",
				ErrMalformedSignature, component.name)
		}
		if _, duplicate := covered[name]; duplicate {
			return nil, fmt.Errorf("%w: duplicate covered component %q",
				ErrMalformedSignature, component.name)
		}
		covered[name] = struct{}{}
		coveredNames = append(coveredNames, name)
	}
	for _, required := range []string{"@method", "@target-uri", "content-digest"} {
		if _, ok := covered[required]; !ok {
			return nil, fmt.Errorf("%w: %s", ErrMissingCoveredComponent, required)
		}
	}
	if _, ok := covered["@signature-params"]; ok {
		return nil, fmt.Errorf("%w: @signature-params must not be enumerated",
			ErrMalformedSignature)
	}

	created, hasCreated, err := integerParameter(input.params, "created")
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTimestamp, err)
	}
	if !hasCreated {
		return nil, fmt.Errorf("%w: created parameter is required", ErrTimestamp)
	}
	nowUnix := config.currentTime().Unix()
	maxSkewSeconds := int64(config.maxCreatedSkew / time.Second)
	switch {
	case created > nowUnix+maxSkewSeconds:
		return nil, fmt.Errorf("%w: created is too far in the future", ErrTimestamp)
	case created < nowUnix-maxSkewSeconds:
		return nil, fmt.Errorf("%w: created is too old", ErrTimestamp)
	}

	var expires *int64
	expiresValue, hasExpires, err := integerParameter(input.params, "expires")
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTimestamp, err)
	}
	if hasExpires {
		if expiresValue < created {
			return nil, fmt.Errorf("%w: expires precedes created", ErrMalformedSignature)
		}
		if nowUnix > expiresValue {
			return nil, fmt.Errorf("%w: expires=%d", ErrExpired, expiresValue)
		}
		expires = &expiresValue
	}

	signatureBase, err := buildSignatureBase(request, targetURI, input)
	if err != nil {
		return nil, err
	}
	if err := verifySignature(registered.Algorithm, registered.Key, signatureBase, signature); err != nil {
		return nil, fmt.Errorf("%w: label %q", err, input.label)
	}

	result := &Result{
		SignatureLabel:    input.label,
		KeyID:             keyID,
		Algorithm:         registered.Algorithm,
		Created:           time.Unix(created, 0).UTC(),
		CoveredComponents: coveredNames,
	}
	if expires != nil {
		expiresTime := time.Unix(*expires, 0).UTC()
		result.Expires = &expiresTime
	}
	return result, nil
}

func requestBody(request *http.Request, config verifyConfig) ([]byte, error) {
	if config.bodySet {
		return config.body, nil
	}
	if request.Body == nil {
		return []byte{}, nil
	}
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, fmt.Errorf("netsec: read request body: %w", err)
	}
	request.Body = io.NopCloser(bytes.NewReader(body))
	return body, nil
}

func verifyContentDigest(request *http.Request, body []byte) error {
	values, ok, err := requestHeaderValues(request.Header, "Content-Digest")
	if err != nil {
		return fmt.Errorf("%w: %v", ErrContentDigest, err)
	}
	if !ok {
		return fmt.Errorf("%w: Content-Digest is required", ErrContentDigest)
	}
	digests, err := parseContentDigest(values)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrContentDigest, err)
	}
	expected, ok := digests["sha-256"]
	if !ok {
		return fmt.Errorf("%w: sha-256 digest is required", ErrContentDigest)
	}
	if len(expected) != sha256.Size {
		return fmt.Errorf("%w: sha-256 digest has length %d", ErrContentDigest, len(expected))
	}
	actual := sha256.Sum256(body)
	if subtle.ConstantTimeCompare(expected, actual[:]) != 1 {
		return fmt.Errorf("%w: request body does not match Content-Digest", ErrContentDigest)
	}
	return nil
}
