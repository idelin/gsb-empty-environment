package arc

import (
	"errors"
	"fmt"
)

// Status is the aggregate validation result for the whole ARC chain.
type Status string

const (
	// StatusNone means no ARC headers were present. There is no chain to
	// validate or fail.
	StatusNone Status = "none"
	// StatusPass means the chain is structurally valid, every cv policy is
	// satisfied and all injected signature verifications passed.
	StatusPass Status = "pass"
	// StatusFail means a chain is present but cannot be trusted: a structural
	// defect, a cv=fail policy, or a failed verification was found.
	StatusFail Status = "fail"
)

// Result reports the outcome of [Validate].
type Result struct {
	// Status is the aggregate chain status.
	Status Status
	// N is the highest (newest) instance number, i.e. the number of ARC sets.
	// It is 0 when no ARC headers were present.
	N int
	// FailingInstance is the instance whose defect or failed verification
	// caused the failure, or 0 when the defect is not tied to one instance.
	FailingInstance int
	// Reason is a human-readable explanation of the failure.
	Reason string
}

// Verifiers carries the caller-injected cryptographic verification functions.
// Both functions receive the message body bytes that were processed by the
// signing step and the exact set of ARC headers covered by the signature,
// already in canonical on-the-wire order. Returning a non-nil error means the
// signature does not verify.
type Verifiers struct {
	// VerifyAMS verifies an ARC-Message-Signature. It is invoked only for the
	// newest instance N. The covered headers are the ARC headers of every
	// instance below N together with the signed message body.
	VerifyAMS func(ams *Header, covered []*Header, body []byte) error
	// VerifyAS verifies an ARC-Seal. It is invoked for every instance i;
	// chains carrying cv=fail are rejected before verification begins. The
	// covered headers are all ARC headers with instance number i or below,
	// excluding the ARC-Seal i itself.
	VerifyAS func(seal *Header, covered []*Header, body []byte) error
}

// Validate performs full ARC chain validation over the headers presented in
// their physical on-the-wire order, together with the message body.
//
// Structural defects (non-ARC or unparseable input, missing or duplicated
// instances, wrong ordering, illegal cv values) and policy/verification
// failures are returned as a Result with StatusFail and a nil error. A
// non-nil error is reserved for caller usage problems, such as a nil required
// verifier. An empty header slice yields StatusNone.
func Validate(lines []string, body []byte, v Verifiers) (*Result, error) {
	if len(lines) == 0 {
		return &Result{Status: StatusNone, Reason: "no ARC headers present"}, nil
	}

	headers, n, err := parseAndCheckStructure(lines)
	if err != nil {
		return failResult(0, err.Error()), nil
	}

	if err := checkWireOrder(headers, n); err != nil {
		return failResult(0, err.Error()), nil
	}

	if instance, err := checkCVPolicy(headers, n); err != nil {
		return failResult(instance, err.Error()), nil
	}

	if v.VerifyAMS == nil {
		return nil, errors.New("arc: Validate requires a non-nil Verifiers.VerifyAMS")
	}
	if v.VerifyAS == nil {
		return nil, errors.New("arc: Validate requires a non-nil Verifiers.VerifyAS")
	}

	if verr := verifyChain(headers, n, body, v); verr != nil {
		instance := 0
		if verr.header != nil {
			instance = verr.header.Instance
		}
		return failResult(instance, verr.Error()), nil
	}

	return &Result{Status: StatusPass, N: n}, nil
}

// verifyError attaches the offending header to a verification failure.
type verifyError struct {
	header *Header
	err    error
}

func (e *verifyError) Error() string {
	return e.err.Error()
}

func failResult(instance int, reason string) *Result {
	return &Result{Status: StatusFail, FailingInstance: instance, Reason: reason}
}

// parseAndCheckStructure parses every line and enforces:
//   - every line is a parseable ARC header with a positive i= tag;
//   - instances run 1..N with no gaps;
//   - each instance has exactly one AAR, one AMS and one AS.
func parseAndCheckStructure(lines []string) ([]*Header, int, error) {
	headers := make([]*Header, 0, len(lines))
	for index, line := range lines {
		header, err := ParseHeader(line)
		if err != nil {
			return nil, 0, err
		}
		header.Index = index
		headers = append(headers, header)
	}

	seen := map[int]map[Kind]bool{}
	maxInstance := 0
	for _, header := range headers {
		if header.Instance > maxInstance {
			maxInstance = header.Instance
		}
		if seen[header.Instance] == nil {
			seen[header.Instance] = map[Kind]bool{}
		}
		if seen[header.Instance][header.Kind] {
			return nil, 0, fmt.Errorf(
				"duplicate %s header at instance i=%d", header.Name, header.Instance)
		}
		seen[header.Instance][header.Kind] = true
	}

	for instance := 1; instance <= maxInstance; instance++ {
		kinds, ok := seen[instance]
		if !ok {
			return nil, 0, fmt.Errorf("missing ARC set for instance i=%d", instance)
		}
		for _, kind := range kindOrder {
			if !kinds[kind] {
				return nil, 0, fmt.Errorf(
					"instance i=%d is missing its %s header", instance, kindName(kind))
			}
		}
	}

	if len(headers) != 3*maxInstance {
		return nil, 0, fmt.Errorf(
			"expected %d ARC headers for i=1..%d, got %d",
			3*maxInstance, maxInstance, len(headers))
	}
	return headers, maxInstance, nil
}

// checkWireOrder enforces the canonical sequence AAR, AMS, AS repeated for
// instances 1, 2, ... N. Any insertion, deletion or swap of ARC sets moves a
// header off its expected slot and is rejected here.
func checkWireOrder(headers []*Header, n int) error {
	for position, header := range headers {
		wantInstance := position/3 + 1
		wantKind := kindOrder[position%3]
		if header.Instance != wantInstance || header.Kind != wantKind {
			return fmt.Errorf(
				"ARC headers out of order: position %d wants i=%d %s, found i=%d %s",
				position, wantInstance, kindName(wantKind),
				header.Instance, kindName(header.Kind))
		}
	}
	return nil
}

// checkCVPolicy enforces the chain-value rules:
//   - the ARC-Seal of i=1 must carry cv=none;
//   - every ARC-Seal with i>1 must carry cv=pass or cv=fail;
//   - any cv=fail fails the whole chain immediately.
func checkCVPolicy(headers []*Header, n int) (int, error) {
	for instance := 1; instance <= n; instance++ {
		seal := findHeader(headers, instance, AS)
		switch instance {
		case 1:
			if seal.CV != "none" {
				return instance, fmt.Errorf(
					"ARC-Seal i=1 must have cv=none, got cv=%q", seal.CV)
			}
		default:
			if seal.CV != "pass" && seal.CV != "fail" {
				return instance, fmt.Errorf(
					"ARC-Seal i=%d must have cv=pass or cv=fail, got cv=%q",
					instance, seal.CV)
			}
			if seal.CV == "fail" {
				return instance, errors.New(
					"ARC chain failed: upstream ARC-Seal carries cv=fail")
			}
		}
	}
	return 0, nil
}

// verifyChain runs the injected verifiers. The newest AMS must verify, and
// every ARC-Seal must verify over the headers its signature covers.
func verifyChain(headers []*Header, n int, body []byte, v Verifiers) *verifyError {
	newestAMS := findHeader(headers, n, AMS)
	if err := v.VerifyAMS(newestAMS, coveredBelow(headers, n), body); err != nil {
		return &verifyError{
			header: newestAMS,
			err:    fmt.Errorf("ARC-Message-Signature i=%d verification failed: %w", n, err),
		}
	}

	for instance := 1; instance <= n; instance++ {
		seal := findHeader(headers, instance, AS)
		if err := v.VerifyAS(seal, coveredUpTo(headers, instance), body); err != nil {
			return &verifyError{
				header: seal,
				err:    fmt.Errorf("ARC-Seal i=%d verification failed: %w", instance, err),
			}
		}
	}
	return nil
}

// coveredBelow returns every ARC header with an instance number strictly below
// i, in on-the-wire order: the headers covered by AMS(i).
func coveredBelow(headers []*Header, i int) []*Header {
	covered := make([]*Header, 0, 3*(i-1))
	for _, header := range headers {
		if header.Instance < i {
			covered = append(covered, header)
		}
	}
	return covered
}

// coveredUpTo returns all ARC headers with instance number i or below,
// excluding ARC-Seal i itself. This is the full set "i 及以下" that the
// ARC-Seal i signature covers.
func coveredUpTo(headers []*Header, i int) []*Header {
	covered := make([]*Header, 0, 3*i-1)
	for _, header := range headers {
		if header.Instance > i {
			continue
		}
		if header.Instance == i && header.Kind == AS {
			continue
		}
		covered = append(covered, header)
	}
	return covered
}

func findHeader(headers []*Header, instance int, kind Kind) *Header {
	for _, header := range headers {
		if header.Instance == instance && header.Kind == kind {
			return header
		}
	}
	return nil
}

func kindName(kind Kind) string {
	switch kind {
	case AAR:
		return "ARC-Authentication-Results"
	case AMS:
		return "ARC-Message-Signature"
	case AS:
		return "ARC-Seal"
	default:
		return "unknown ARC header"
	}
}
