package netsec

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestValidateChain(t *testing.T) {
	headers := validChain(3)
	recorder := &recordingVerifier{}

	if err := ValidateChain(headers, recorder.verify); err != nil {
		t.Fatalf("ValidateChain() error = %v", err)
	}

	if got, want := len(recorder.sealCoverage), 3; got != want {
		t.Fatalf("seal verifications = %d, want %d", got, want)
	}
	for instance := 1; instance <= 3; instance++ {
		covered, ok := recorder.sealCoverage[instance]
		if !ok {
			t.Fatalf("ARC-Seal instance %d was not verified", instance)
		}

		want := headersAfterInstance(headers, instance)
		if !reflect.DeepEqual(covered, want) {
			t.Errorf("coverage for instance %d = %#v, want %#v", instance, covered, want)
		}
	}

	if recorder.amsInstance != 3 {
		t.Errorf("latest AMS instance = %d, want 3", recorder.amsInstance)
	}
	if recorder.amsCovered != nil {
		t.Errorf("latest AMS coverage = %#v, want nil", recorder.amsCovered)
	}
}

func TestValidateChainStructureAttacks(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func([]Header) []Header
		verify    VerifyFunc
		wantError error
	}{
		{
			name: "missing ARC set",
			mutate: func(headers []Header) []Header {
				return removeInstance(headers, 2)
			},
			wantError: ErrMissingInstance,
		},
		{
			name: "missing ARC-Message-Signature",
			mutate: func(headers []Header) []Header {
				return removeType(headers, 2, ARCMessageSignature)
			},
			wantError: ErrMissingHeader,
		},
		{
			name: "duplicate ARC set",
			mutate: func(headers []Header) []Header {
				return append(headers, instanceHeaders(headers, 2)...)
			},
			wantError: ErrDuplicateInstance,
		},
		{
			name: "duplicate ARC-Seal",
			mutate: func(headers []Header) []Header {
				seal := findInstanceType(headers, 2, ARCSeal)
				return append(headers, seal)
			},
			wantError: ErrDuplicateInstance,
		},
		{
			name: "cv none moved to later instance",
			mutate: func(headers []Header) []Header {
				return replaceCV(headers, 2, "none")
			},
			wantError: ErrInvalidCV,
		},
		{
			name: "cv pass moved to first instance",
			mutate: func(headers []Header) []Header {
				return replaceCV(headers, 1, "pass")
			},
			wantError: ErrInvalidCV,
		},
		{
			name: "cv fail rejects chain",
			mutate: func(headers []Header) []Header {
				return replaceCV(headers, 2, "fail")
			},
			wantError: ErrChainRejected,
		},
		{
			name: "missing cv tag",
			mutate: func(headers []Header) []Header {
				seal := findInstanceType(headers, 2, ARCSeal)
				seal.Value = removeTag(seal.Value, "cv")
				return replaceHeader(headers, seal)
			},
			wantError: ErrInvalidCV,
		},
		{
			name: "swapped seal signature",
			mutate: func(headers []Header) []Header {
				seal := findInstanceType(headers, 1, ARCSeal)
				seal.Value = replaceTagValue(seal.Value, "b", "seal-2")
				return replaceHeader(headers, seal)
			},
			verify:    (&recordingVerifier{}).verify,
			wantError: ErrSealVerification,
		},
		{
			name: "swapped latest AMS signature",
			mutate: func(headers []Header) []Header {
				ams := findInstanceType(headers, 3, ARCMessageSignature)
				ams.Value = replaceTagValue(ams.Value, "b", "ams-2")
				return replaceHeader(headers, ams)
			},
			verify:    (&recordingVerifier{}).verify,
			wantError: ErrMessageVerification,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			headers := test.mutate(validChain(3))
			verify := test.verify
			if verify == nil {
				verify = func(Header, []Header) error {
					t.Fatal("signature verifier called before structural failure")
					return nil
				}
			}

			err := ValidateChain(headers, verify)
			if !errors.Is(err, test.wantError) {
				t.Fatalf("ValidateChain() error = %v, want errors.Is(..., %v)", err, test.wantError)
			}
		})
	}
}

func TestValidateChainRejectsNilVerifier(t *testing.T) {
	if err := ValidateChain(validChain(1), nil); !errors.Is(err, ErrNilVerifier) {
		t.Fatalf("ValidateChain() error = %v, want %v", err, ErrNilVerifier)
	}
}

func TestValidateChainDetectsSwappedARCSetOrder(t *testing.T) {
	original := validChain(3)
	swapped := make([]Header, 0, len(original))
	swapped = append(swapped, instanceHeaders(original, 2)...)
	swapped = append(swapped, instanceHeaders(original, 1)...)
	swapped = append(swapped, instanceHeaders(original, 3)...)

	verify := func(header Header, covered []Header) error {
		instance, err := header.Instance()
		if err != nil {
			return err
		}
		if header.Type == ARCMessageSignature {
			return nil
		}
		if !reflect.DeepEqual(covered, headersAfterInstance(original, instance)) {
			return fmt.Errorf("ARC set order changed in coverage for instance %d", instance)
		}
		return nil
	}

	if err := ValidateChain(swapped, verify); !errors.Is(err, ErrSealVerification) {
		t.Fatalf("ValidateChain() error = %v, want %v", err, ErrSealVerification)
	}
}

func TestParseHeaderLine(t *testing.T) {
	header, err := ParseHeaderLine(
		"ARC-Authentication-Results: i=1; mx.example; note=\"semi; i=9\"; dkim=pass",
	)
	if err != nil {
		t.Fatalf("ParseHeaderLine() error = %v", err)
	}
	if header.Type != ARCAuthenticationResults {
		t.Errorf("header type = %q, want %q", header.Type, ARCAuthenticationResults)
	}

	instance, err := header.Instance()
	if err != nil {
		t.Fatalf("Instance() error = %v", err)
	}
	if instance != 1 {
		t.Errorf("instance = %d, want 1", instance)
	}
}

func TestParseHeaderLineRejectsUnknownType(t *testing.T) {
	if _, err := ParseHeaderLine("X-Not-ARC: i=1"); !errors.Is(err, ErrUnknownHeaderType) {
		t.Fatalf("ParseHeaderLine() error = %v, want %v", err, ErrUnknownHeaderType)
	}
}

type recordingVerifier struct {
	sealCoverage map[int][]Header
	amsInstance  int
	amsCovered   []Header
}

func (v *recordingVerifier) verify(header Header, covered []Header) error {
	instance, err := header.Instance()
	if err != nil {
		return err
	}

	tag := tagValue(header.Value, "b")
	switch header.Type {
	case ARCSeal:
		if v.sealCoverage == nil {
			v.sealCoverage = make(map[int][]Header)
		}
		v.sealCoverage[instance] = append([]Header(nil), covered...)
		if want := fmt.Sprintf("seal-%d", instance); tag != want {
			return fmt.Errorf("seal signature %q does not match instance %d", tag, instance)
		}
	case ARCMessageSignature:
		v.amsInstance = instance
		v.amsCovered = covered
		if want := fmt.Sprintf("ams-%d", instance); tag != want {
			return fmt.Errorf("AMS signature %q does not match instance %d", tag, instance)
		}
	default:
		return fmt.Errorf("unexpected verification request for %s", header.Type)
	}
	return nil
}

func validChain(n int) []Header {
	headers := make([]Header, 0, 3*n)
	for instance := 1; instance <= n; instance++ {
		cv := "pass"
		if instance == 1 {
			cv = "none"
		}
		headers = append(
			headers,
			Header{
				Type:  ARCAuthenticationResults,
				Value: fmt.Sprintf("i=%d; mx.example; dkim=pass", instance),
			},
			Header{
				Type:  ARCMessageSignature,
				Value: fmt.Sprintf("i=%d; a=rsa-sha256; b=ams-%d", instance, instance),
			},
			Header{
				Type:  ARCSeal,
				Value: fmt.Sprintf("i=%d; a=rsa-sha256; cv=%s; b=seal-%d", instance, cv, instance),
			},
		)
	}
	return headers
}

func headersAfterInstance(headers []Header, instance int) []Header {
	var covered []Header
	for _, header := range headers {
		candidateInstance, err := header.Instance()
		if err != nil {
			panic(err)
		}
		if candidateInstance >= instance {
			covered = append(covered, header)
		}
	}
	return covered
}

func instanceHeaders(headers []Header, instance int) []Header {
	var selected []Header
	for _, header := range headers {
		candidateInstance, err := header.Instance()
		if err != nil {
			panic(err)
		}
		if candidateInstance == instance {
			selected = append(selected, header)
		}
	}
	return selected
}

func removeInstance(headers []Header, instance int) []Header {
	var remaining []Header
	for _, header := range headers {
		candidateInstance, err := header.Instance()
		if err != nil {
			panic(err)
		}
		if candidateInstance != instance {
			remaining = append(remaining, header)
		}
	}
	return remaining
}

func removeType(headers []Header, instance int, kind HeaderType) []Header {
	var remaining []Header
	for _, header := range headers {
		candidateInstance, err := header.Instance()
		if err != nil {
			panic(err)
		}
		if candidateInstance != instance || header.Type != kind {
			remaining = append(remaining, header)
		}
	}
	return remaining
}

func findInstanceType(headers []Header, instance int, kind HeaderType) Header {
	for _, header := range headers {
		candidateInstance, err := header.Instance()
		if err != nil {
			panic(err)
		}
		if candidateInstance == instance && header.Type == kind {
			return header
		}
	}
	panic(fmt.Sprintf("missing %s instance %d", kind, instance))
}

func replaceHeader(headers []Header, replacement Header) []Header {
	instance, err := replacement.Instance()
	if err != nil {
		panic(err)
	}
	result := make([]Header, len(headers))
	copy(result, headers)
	for index, header := range result {
		candidateInstance, err := header.Instance()
		if err != nil {
			panic(err)
		}
		if candidateInstance == instance && header.Type == replacement.Type {
			result[index] = replacement
			return result
		}
	}
	panic(fmt.Sprintf("missing %s instance %d", replacement.Type, instance))
}

func replaceCV(headers []Header, instance int, cv string) []Header {
	seal := findInstanceType(headers, instance, ARCSeal)
	seal.Value = replaceTagValue(seal.Value, "cv", cv)
	return replaceHeader(headers, seal)
}

func replaceTagValue(value, name, replacement string) string {
	parts := strings.Split(value, ";")
	for index, part := range parts {
		key, _, ok := splitAttribute(part)
		if ok && strings.EqualFold(key, name) {
			parts[index] = " " + name + "=" + replacement
			return strings.TrimSpace(strings.Join(parts, ";"))
		}
	}
	panic(fmt.Sprintf("missing tag %q in %q", name, value))
}

func removeTag(value, name string) string {
	parts := strings.Split(value, ";")
	for index, part := range parts {
		key, _, ok := splitAttribute(part)
		if ok && strings.EqualFold(key, name) {
			return strings.TrimSpace(strings.Join(append(parts[:index], parts[index+1:]...), ";"))
		}
	}
	panic(fmt.Sprintf("missing tag %q in %q", name, value))
}

func tagValue(value, name string) string {
	for _, part := range strings.Split(value, ";") {
		key, tagValue, ok := splitAttribute(part)
		if ok && strings.EqualFold(key, name) {
			return tagValue
		}
	}
	return ""
}
