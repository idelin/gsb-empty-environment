package netsec

import (
	"fmt"
	"strings"
)

// FailureKind describes why a verification case failed.
type FailureKind uint8

const (
	// FailureWrongResult means the comparison returned an incorrect boolean.
	FailureWrongResult FailureKind = iota
	// FailureAccessMismatch means the byte access sequence depended on input
	// contents for a fixed pair of lengths.
	FailureAccessMismatch
	// FailurePanic means the comparison panicked or could not be run.
	FailurePanic
)

func (k FailureKind) String() string {
	switch k {
	case FailureWrongResult:
		return "wrong result"
	case FailureAccessMismatch:
		return "access mismatch"
	case FailurePanic:
		return "panic"
	default:
		return fmt.Sprintf("failure(%d)", uint8(k))
	}
}

// Failure records one failed verification case.
type Failure struct {
	Kind             FailureKind
	Case             string
	Baseline         string
	Detail           string
	Want             bool
	Got              bool
	Accesses         []Access
	BaselineAccesses []Access
}

// Report is the result of running the constant-time access checks.
type Report struct {
	Cases    int
	Failures []Failure
}

// Passed reports whether every case completed without leaking or returning
// an incorrect result.
func (r Report) Passed() bool {
	return len(r.Failures) == 0
}

// Err returns nil when the report passed, or a VerificationError containing
// the complete report otherwise.
func (r Report) Err() error {
	if r.Passed() {
		return nil
	}
	return &VerificationError{Report: r}
}

// VerificationError reports one or more constant-time check failures.
type VerificationError struct {
	Report Report
}

func (e *VerificationError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "constant-time comparison verification failed: %d failure(s) in %d case(s)",
		len(e.Report.Failures), e.Report.Cases)

	const shown = 5
	for i, failure := range e.Report.Failures {
		if i == shown {
			fmt.Fprintf(&b, "\n- ... and %d more", len(e.Report.Failures)-shown)
			break
		}
		fmt.Fprintf(&b, "\n- [%s] %s: %s", failure.Kind, failure.Case, failure.Detail)
	}
	return b.String()
}

// Verify rejects a comparison whose byte access sequence depends on input
// contents, or whose result is incorrect. It returns nil when the comparison
// passes every generated case.
func Verify(compare CompareFunc) error {
	return Analyze(compare).Err()
}

// Analyze runs the verification matrix and returns a structured report.
func Analyze(compare CompareFunc) Report {
	var report Report
	if compare == nil {
		report.Failures = append(report.Failures, Failure{
			Kind:   FailurePanic,
			Case:   "setup",
			Detail: "nil comparison function",
		})
		return report
	}

	for _, group := range verificationGroups() {
		var baseline *observation
		for _, testCase := range group.cases {
			report.Cases++
			got, accesses, err := Trace(compare, testCase.a, testCase.b)
			if err != nil {
				report.Failures = append(report.Failures, Failure{
					Kind:     FailurePanic,
					Case:     testCase.name,
					Detail:   err.Error(),
					Want:     testCase.want,
					Accesses: accesses,
				})
				continue
			}

			if got != testCase.want {
				report.Failures = append(report.Failures, Failure{
					Kind:     FailureWrongResult,
					Case:     testCase.name,
					Detail:   fmt.Sprintf("got %t, want %t", got, testCase.want),
					Want:     testCase.want,
					Got:      got,
					Accesses: accesses,
				})
			}

			if baseline == nil {
				baseline = &observation{
					name:     testCase.name,
					accesses: accesses,
				}
				continue
			}

			if !sameAccesses(baseline.accesses, accesses) {
				report.Failures = append(report.Failures, Failure{
					Kind:             FailureAccessMismatch,
					Case:             testCase.name,
					Baseline:         baseline.name,
					Detail:           traceDifference(baseline.accesses, accesses),
					Want:             testCase.want,
					Got:              got,
					Accesses:         accesses,
					BaselineAccesses: baseline.accesses,
				})
			}
		}
	}

	return report
}

type observation struct {
	name     string
	accesses []Access
}

func sameAccesses(a, b []Access) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func traceDifference(baseline, actual []Access) string {
	limit := len(baseline)
	if len(actual) < limit {
		limit = len(actual)
	}

	step := 0
	for step < limit && baseline[step] == actual[step] {
		step++
	}

	switch {
	case step < len(baseline) && step < len(actual):
		return fmt.Sprintf("step %d read %s, baseline read %s; got %d access(es), baseline %d",
			step, actual[step], baseline[step], len(actual), len(baseline))
	case step < len(actual):
		return fmt.Sprintf("extra access at step %d: %s; got %d access(es), baseline %d",
			step, actual[step], len(actual), len(baseline))
	case step < len(baseline):
		return fmt.Sprintf("missing access at step %d: baseline read %s; got %d access(es), baseline %d",
			step, baseline[step], len(actual), len(baseline))
	default:
		return "access sequences differ"
	}
}

type traceCase struct {
	name string
	a    []byte
	b    []byte
	want bool
}

type traceGroup struct {
	name  string
	cases []traceCase
}

func verificationGroups() []traceGroup {
	var groups []traceGroup
	groups = append(groups, equalLengthGroups()...)
	groups = append(groups, unequalLengthGroups()...)
	groups = append(groups, emptyInputGroups()...)
	return groups
}

func equalLengthGroups() []traceGroup {
	lengths := []int{1, 2, 3, 4, 8, 16, 32}
	groups := make([]traceGroup, 0, len(lengths))
	for _, length := range lengths {
		group := traceGroup{name: fmt.Sprintf("equal length %d", length)}
		group.cases = append(group.cases,
			matchingCase(length, 0x11),
			matchingCase(length, 0xa5),
		)
		for position := 0; position < length; position++ {
			group.cases = append(group.cases,
				mismatchCase(length, position, false, 0x11, 0x01),
				mismatchCase(length, position, false, 0xa5, 0x80),
				mismatchCase(length, position, true, 0x11, 0x01),
				mismatchCase(length, position, true, 0xa5, 0x80),
			)
		}
		groups = append(groups, group)
	}
	return groups
}

func unequalLengthGroups() []traceGroup {
	lengths := []int{1, 2, 3, 4, 8, 16, 32}
	groups := make([]traceGroup, 0, len(lengths)*(len(lengths)-1))
	for _, aLength := range lengths {
		for _, bLength := range lengths {
			if aLength == bLength {
				continue
			}

			name := fmt.Sprintf("unequal lengths %d/%d", aLength, bLength)
			group := traceGroup{name: name}
			group.cases = append(group.cases,
				unequalCase(name, aLength, bLength, 0x11),
				unequalCase(name, aLength, bLength, 0xa5),
				unequalMismatchCase(name, aLength, bLength, false),
				unequalMismatchCase(name, aLength, bLength, true),
			)
			groups = append(groups, group)
		}
	}
	return groups
}

func emptyInputGroups() []traceGroup {
	groups := []traceGroup{{
		name: "empty/empty",
		cases: []traceCase{{
			name: "empty/0/0",
			a:    []byte{},
			b:    []byte{},
			want: true,
		}},
	}}

	for _, length := range []int{1, 2, 8, 32} {
		aEmpty := traceGroup{name: fmt.Sprintf("empty/%d", length)}
		bEmpty := traceGroup{name: fmt.Sprintf("%d/empty", length)}
		for variant, seed := range []byte{0x11, 0xa5, 0x00, 0xff} {
			content := pattern(length, seed)
			aEmpty.cases = append(aEmpty.cases, traceCase{
				name: fmt.Sprintf("empty/%d/variant%d", length, variant),
				a:    []byte{},
				b:    content,
				want: false,
			})
			bEmpty.cases = append(bEmpty.cases, traceCase{
				name: fmt.Sprintf("%d/empty/variant%d", length, variant),
				a:    content,
				b:    []byte{},
				want: false,
			})
		}
		groups = append(groups, aEmpty, bEmpty)
	}
	return groups
}

func matchingCase(length int, seed byte) traceCase {
	content := pattern(length, seed)
	return traceCase{
		name: fmt.Sprintf("equal/%d/match/%02x", length, seed),
		a:    content,
		b:    clone(content),
		want: true,
	}
}

func mismatchCase(length, position int, flipA bool, seed, delta byte) traceCase {
	a := pattern(length, seed)
	b := clone(a)
	if flipA {
		a[position] ^= delta
	} else {
		b[position] ^= delta
	}
	side := "b"
	if flipA {
		side = "a"
	}
	return traceCase{
		name: fmt.Sprintf("equal/%d/mismatch@%d/%s/%02x", length, position, side, seed),
		a:    a,
		b:    b,
		want: false,
	}
}

func unequalCase(name string, aLength, bLength int, seed byte) traceCase {
	return traceCase{
		name: fmt.Sprintf("%s/match-overlap/%02x", name, seed),
		a:    pattern(aLength, seed),
		b:    pattern(bLength, seed),
		want: false,
	}
}

func unequalMismatchCase(name string, aLength, bLength int, flipA bool) traceCase {
	a := pattern(aLength, 0x11)
	b := pattern(bLength, 0x11)
	overlap := aLength
	if bLength < overlap {
		overlap = bLength
	}

	if overlap > 0 {
		position := overlap - 1
		if flipA {
			a[position] ^= 0x80
		} else {
			b[position] ^= 0x01
		}
	}

	side := "b"
	if flipA {
		side = "a"
	}
	position := overlap - 1
	if overlap == 0 {
		position = -1
	}
	return traceCase{
		name: fmt.Sprintf("%s/mismatch@%d/%s", name, position, side),
		a:    a,
		b:    b,
		want: false,
	}
}

func pattern(length int, seed byte) []byte {
	result := make([]byte, length)
	for i := range result {
		result[i] = seed ^ byte(i*29)
	}
	return result
}

func clone(data []byte) []byte {
	return append([]byte(nil), data...)
}
