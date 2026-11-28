package pathverify

import (
	"crypto/x509"
	"testing"
)

func baseOptions(leaf *x509.Certificate, roots, inters *CertPool) Options {
	return Options{
		Leaf:          leaf,
		Roots:         roots,
		Intermediates: inters,
		EKU:           []ExtKeyUsage{ExtKeyUsageServerAuth},
		DNSName:       "service.example.com",
		CurrentTime:   AtTime(fixedNow),
	}
}

func noHostname(leaf *x509.Certificate, roots, inters *CertPool) Options {
	opts := baseOptions(leaf, roots, inters)
	opts.DNSName = ""
	opts.EKU = nil
	return opts
}

func expectReason(t *testing.T, leaf *x509.Certificate, roots, inters *CertPool, reason Reason) error {
	t.Helper()
	return expectReasonOpts(baseOptions(leaf, roots, inters), reason)
}

func expectReasonOpts(opts Options, reason Reason) error {
	_, err := Verify(opts)
	if err == nil {
		return &failT{reason: reason, msg: "expected failure but verification succeeded"}
	}
	if IsReason(err, reason) {
		return nil
	}
	// 顶层通常是 ReasonNoValidPath；真正的策略原因记录在各候选链 Attempt 中。
	if e, ok := err.(*Error); ok {
		for _, a := range e.Attempts {
			if a.Err != nil && a.Err.Reason == reason {
				return nil
			}
		}
	}
	return &failT{reason: reason, msg: "expected reason " + string(reason) + " but got: " + err.Error()}
}

type failT struct {
	reason Reason
	msg    string
}

func (e *failT) Error() string { return e.msg }
