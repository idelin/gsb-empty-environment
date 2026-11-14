package netsec

import (
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Policy selects how a gateway treats DKIM body length counts.
type Policy string

const (
	PolicyHighTrust     Policy = "high-trust"
	PolicyCompatibility Policy = "compatibility"

	PolicyStrict = PolicyHighTrust
	PolicyCompat = PolicyCompatibility
)

// ParsePolicy accepts the policy names used by the package.
func ParsePolicy(value string) (Policy, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "high-trust", "high", "strict":
		return PolicyHighTrust, nil
	case "compatibility", "compat", "compatible", "":
		return PolicyCompatibility, nil
	default:
		return "", fmt.Errorf("unknown DKIM coverage policy %q", value)
	}
}

// Decision is the aggregate body-authentication result.
type Decision string

const (
	DecisionAuthenticated Decision = "authenticated"
	DecisionPartial       Decision = "partial"
	DecisionRejected      Decision = "rejected"
	DecisionNoSignature   Decision = "no-signature"
	DecisionIncomplete    Decision = "incomplete"
)

// Trusted reports whether the whole visible body is authenticated.
func (d Decision) Trusted() bool {
	return d == DecisionAuthenticated
}

// SignatureCoverage records one signature's claimed and authenticated range.
// The slice containing these values is never keyed by signing identity, so two
// signatures from the same domain and selector are both preserved.
type SignatureCoverage struct {
	Index                  int
	Domain                 string
	Selector               string
	Algorithm              string
	Canonicalization       string
	HeaderCanonicalization CanonMode
	BodyCanonicalization   CanonMode
	BodyLength             *int64
	Verified               bool

	CanonicalBodyBytes    int64
	CoveredCanonicalBytes int64
	CanonicalStart        int64
	CanonicalEnd          int64
	RawCovered            RawSpan
	RawDependencies       []RawSpan
	TailOnlyDiscarded     bool
	PolicyAllowed         bool
	AuthenticatedPrefix   bool
	FullyCoversBody       bool
	Reason                string
}

// HasBodyLength reports whether this signature carried l=.
func (c SignatureCoverage) HasBodyLength() bool {
	return c.BodyLength != nil
}

// AuthenticatedRange is the raw MIME range attributable to one verified
// signature. RawDependencies can be used when whitespace canonicalization
// leaves non-contiguous source bytes.
type AuthenticatedRange struct {
	Domain                 string
	Selector               string
	Algorithm              string
	Canonicalization       string
	BodyCanonicalization   CanonMode
	BodyLength             *int64
	CanonicalStart         int64
	CanonicalEnd           int64
	Raw                    RawSpan
	RawDependencies        []RawSpan
	TailOnlyDiscarded      bool
	FullyAuthenticatedBody bool
}

// Assessment is the result for every signature plus the aggregate policy
// decision.
type Assessment struct {
	Policy                Policy
	Decision              Decision
	Complete              bool
	RawBodyBytes          int64
	SimpleCanonicalBytes  int64
	RelaxedCanonicalBytes int64
	Signatures            []SignatureCoverage
	Authenticated         map[string]AuthenticatedRange
	FullBodyAuthenticated bool
	Reasons               []string
}

// AnalyzeBody canonicalizes body in both modes and evaluates all signature
// claims. The crypto verification result must already be present in sigs.
func AnalyzeBody(body []byte, sigs []SignatureVerification, policy Policy) (*Assessment, error) {
	receiver := NewBodyReceiver()
	if _, err := io.Copy(receiver, bytes.NewReader(body)); err != nil {
		return nil, err
	}
	views, err := receiver.Finish()
	if err != nil {
		return nil, err
	}
	return AssessViews(views, sigs, policy)
}

// Analyze is an alias for AnalyzeBody.
func Analyze(body []byte, sigs []SignatureVerification, policy Policy) (*Assessment, error) {
	return AnalyzeBody(body, sigs, policy)
}

// AssessReader streams one body from r and evaluates all signature claims.
func AssessReader(r io.Reader, sigs []SignatureVerification, policy Policy) (*Assessment, error) {
	receiver := NewBodyReceiver()
	if _, err := io.Copy(receiver, r); err != nil {
		return nil, err
	}
	views, err := receiver.Finish()
	if err != nil {
		return nil, err
	}
	return AssessViews(views, sigs, policy)
}

// AssessViews evaluates signatures against an already-completed stream.
func AssessViews(views *BodyViews, sigs []SignatureVerification, policy Policy) (*Assessment, error) {
	if views == nil {
		return nil, fmt.Errorf("nil body views")
	}
	if policy == "" {
		policy = PolicyCompatibility
	}
	if policy != PolicyHighTrust && policy != PolicyCompatibility {
		return nil, fmt.Errorf("unknown DKIM coverage policy %q", policy)
	}

	result := &Assessment{
		Policy:                policy,
		Complete:              views.Complete,
		RawBodyBytes:          views.RawLength,
		SimpleCanonicalBytes:  views.Simple.Len(),
		RelaxedCanonicalBytes: views.Relaxed.Len(),
		Authenticated:         make(map[string]AuthenticatedRange),
	}
	if !views.Complete {
		result.Decision = DecisionIncomplete
		result.Reasons = append(result.Reasons, "body stream is incomplete")
	}

	verifiedPrefix := false
	for index, verification := range sigs {
		coverage := evaluateSignature(index, views, verification, policy)
		result.Signatures = append(result.Signatures, coverage)
		if coverage.Verified {
			verifiedPrefix = verifiedPrefix || coverage.AuthenticatedPrefix
		}
		if coverage.FullyCoversBody {
			result.FullBodyAuthenticated = true
		}
		if coverage.Reason != "" {
			result.Reasons = append(result.Reasons, fmt.Sprintf("signature %d: %s", index, coverage.Reason))
		}
		if coverage.Verified && coverage.AuthenticatedPrefix {
			key := coverageKey(index, coverage)
			result.Authenticated[key] = AuthenticatedRange{
				Domain:                 coverage.Domain,
				Selector:               coverage.Selector,
				Algorithm:              coverage.Algorithm,
				Canonicalization:       coverage.Canonicalization,
				BodyCanonicalization:   coverage.BodyCanonicalization,
				BodyLength:             coverage.BodyLength,
				CanonicalStart:         coverage.CanonicalStart,
				CanonicalEnd:           coverage.CanonicalEnd,
				Raw:                    coverage.RawCovered,
				RawDependencies:        append([]RawSpan(nil), coverage.RawDependencies...),
				TailOnlyDiscarded:      coverage.TailOnlyDiscarded,
				FullyAuthenticatedBody: coverage.FullyCoversBody,
			}
		}
	}

	switch {
	case !views.Complete:
		result.Decision = DecisionIncomplete
	case len(sigs) == 0:
		result.Decision = DecisionNoSignature
	case result.FullBodyAuthenticated:
		result.Decision = DecisionAuthenticated
	case verifiedPrefix:
		if policy == PolicyHighTrust {
			result.Decision = DecisionRejected
		} else {
			result.Decision = DecisionPartial
		}
	default:
		result.Decision = DecisionRejected
	}
	return result, nil
}

func evaluateSignature(index int, views *BodyViews, verification SignatureVerification, policy Policy) SignatureCoverage {
	coverage := SignatureCoverage{
		Index:    index,
		Verified: verification.Verified,
	}

	sig := verification.Signature
	if sig.Raw == "" && verification.Header != "" {
		parsed, err := ParseDKIMSignature(verification.Header)
		if err != nil {
			coverage.Reason = "parse error: " + err.Error()
			coverage.PolicyAllowed = false
			return coverage
		}
		sig = parsed
	}
	if verification.Error != "" {
		coverage.Reason = "verification error: " + verification.Error
	}
	if sig.HeaderCanonicalization == "" {
		sig.HeaderCanonicalization = CanonSimple
	}
	if sig.BodyCanonicalization == "" {
		sig.BodyCanonicalization = CanonSimple
	}
	if sig.Canonicalization == "" {
		sig.Canonicalization = string(sig.HeaderCanonicalization) + "/" + string(sig.BodyCanonicalization)
	}

	coverage.Domain = sig.Domain
	coverage.Selector = sig.Selector
	coverage.Algorithm = sig.Algorithm
	coverage.Canonicalization = sig.Canonicalization
	coverage.HeaderCanonicalization = sig.HeaderCanonicalization
	coverage.BodyCanonicalization = sig.BodyCanonicalization
	coverage.BodyLength = sig.BodyLength

	if sig.Raw == "" && sig.Domain == "" && sig.Selector == "" && sig.Algorithm == "" {
		if coverage.Reason == "" {
			coverage.Reason = "missing signature"
		}
		return coverage
	}
	if verification.Error != "" {
		return coverage
	}
	if !views.Complete {
		coverage.Reason = "body stream is incomplete"
		return coverage
	}

	body := views.Simple
	if sig.BodyCanonicalization == CanonRelaxed {
		body = views.Relaxed
	} else if sig.BodyCanonicalization != CanonSimple {
		coverage.Reason = "unsupported body canonicalization"
		return coverage
	}
	coverage.CanonicalBodyBytes = body.Len()

	covered := body.Len()
	if sig.BodyLength != nil {
		if *sig.BodyLength > body.Len() {
			coverage.Reason = fmt.Sprintf("l=%d exceeds canonical body length %d", *sig.BodyLength, body.Len())
			coverage.PolicyAllowed = false
			return coverage
		}
		covered = *sig.BodyLength
	}
	coverage.CoveredCanonicalBytes = covered
	coverage.CanonicalStart = 0
	coverage.CanonicalEnd = covered

	mapped, err := body.MapPrefix(covered)
	if err != nil {
		coverage.Reason = err.Error()
		return coverage
	}
	coverage.RawCovered = RawSpan{Start: mapped.RawStart, End: mapped.RawEnd}
	coverage.RawDependencies = mapped.Dependencies
	coverage.TailOnlyDiscarded = mapped.TailOnlyDiscarded

	coverage.PolicyAllowed = true
	if sig.BodyLength != nil && policy == PolicyHighTrust {
		coverage.PolicyAllowed = false
		coverage.Reason = "high-trust policy forbids l="
	}

	coversAllCanonical := covered == body.Len()
	coverage.AuthenticatedPrefix = coverage.Verified && mapped.CanonicalBytes >= 0
	coverage.FullyCoversBody = coverage.Verified &&
		coversAllCanonical &&
		mapped.TailOnlyDiscarded &&
		coverage.PolicyAllowed
	if coverage.FullyCoversBody {
		coverage.Reason = ""
	} else if coverage.Reason == "" && !coversAllCanonical {
		coverage.Reason = fmt.Sprintf("only %d of %d canonical body bytes are covered", covered, body.Len())
	} else if coverage.Reason == "" && !mapped.TailOnlyDiscarded {
		coverage.Reason = "uncovered raw tail is not provably canonicalization-discarded"
	} else if coverage.Reason == "" && !coverage.Verified {
		coverage.Reason = "signature verification failed"
	}
	return coverage
}

func coverageKey(index int, coverage SignatureCoverage) string {
	length := "full"
	if coverage.BodyLength != nil {
		length = "l=" + strconv.FormatInt(*coverage.BodyLength, 10)
	}
	return fmt.Sprintf("%d|d=%s|s=%s|a=%s|%s", index,
		coverage.Domain, coverage.Selector, coverage.Algorithm, length)
}
