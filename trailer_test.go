package netsec

import (
	"errors"
	"net/http"
	"reflect"
	"testing"
)

func signedHeaderAndTrailers(t *testing.T, digest DigestFunc, trailerValues ...string) (http.Header, http.Header, []byte) {
	t.Helper()
	header := http.Header{
		"Content-Type": {"application/json"},
		"Trailer":      {"Digest"},
	}
	trailers := http.Header{"Digest": trailerValues}
	signature, err := SignHTTP(header, trailers, digest)
	if err != nil {
		t.Fatalf("SignHTTP() error = %v", err)
	}
	return header, trailers, signature
}

func TestDeclareTrailers(t *testing.T) {
	header := http.Header{}
	if err := DeclareTrailers(header, "Digest", "x-checksum", "Digest"); err != nil {
		t.Fatalf("DeclareTrailers() error = %v", err)
	}
	if got := header.Values(TrailerHeader); !reflect.DeepEqual(got, []string{"Digest, X-Checksum"}) {
		t.Fatalf("Trailer values = %#v", got)
	}

	declared, err := DeclaredTrailers(header)
	if err != nil {
		t.Fatalf("DeclaredTrailers() error = %v", err)
	}
	if !reflect.DeepEqual(declared, []string{"Digest", "X-Checksum"}) {
		t.Fatalf("DeclaredTrailers() = %#v", declared)
	}

	// A non-canonical map key is still part of the declaration.
	lower := http.Header{"trailer": {"Digest"}}
	declared, err = DeclaredTrailers(lower)
	if err != nil {
		t.Fatalf("DeclaredTrailers(lower) error = %v", err)
	}
	if !reflect.DeepEqual(declared, []string{"Digest"}) {
		t.Fatalf("DeclaredTrailers(lower) = %#v", declared)
	}
}

func TestDeclareTrailersRejectsForbiddenFields(t *testing.T) {
	for _, name := range []string{
		"Authorization",
		"Proxy-Authorization",
		"Signature",
		"Host",
		"Forwarded",
		"X-Forwarded-For",
		"X-Forwarded-By",
		"X-Original-Host",
		"X-Rewrite-URI",
		"Content-Length",
		"Transfer-Encoding",
		"Trailer",
	} {
		t.Run(name, func(t *testing.T) {
			header := http.Header{}
			if err := DeclareTrailers(header, name); !errors.Is(err, ErrForbiddenTrailer) {
				t.Fatalf("DeclareTrailers(%q) error = %v, want %v", name, err, ErrForbiddenTrailer)
			}
			if len(header) != 0 {
				t.Fatalf("header mutated after error: %#v", header)
			}
		})
	}

	header := http.Header{"Trailer": {"Authorization"}}
	if _, err := DeclaredTrailers(header); !errors.Is(err, ErrForbiddenTrailer) {
		t.Fatalf("DeclaredTrailers() error = %v, want %v", err, ErrForbiddenTrailer)
	}
}

func TestValidateTrailersRejectsUndeclaredField(t *testing.T) {
	header := http.Header{"Trailer": {"Digest"}}
	trailers := http.Header{
		"Digest":  {"sha-256=abc"},
		"X-Extra": {"not declared"},
	}
	if err := ValidateTrailers(header, trailers); !errors.Is(err, ErrUndeclaredTrailer) {
		t.Fatalf("ValidateTrailers() error = %v, want %v", err, ErrUndeclaredTrailer)
	}
}

func TestValidateTrailersRejectsHeaderTrailerCollision(t *testing.T) {
	header := http.Header{
		"Trailer": {"Digest"},
		"Digest":  {"authenticated-header-value"},
	}
	trailers := http.Header{"Digest": {"trailer-value"}}
	if err := ValidateTrailers(header, trailers); !errors.Is(err, ErrTrailerHeaderCollision) {
		t.Fatalf("ValidateTrailers() error = %v, want %v", err, ErrTrailerHeaderCollision)
	}

	sameValueHeader := http.Header{
		"Trailer": {"Digest"},
		"Digest":  {"same-value"},
	}
	if err := ValidateTrailers(sameValueHeader, http.Header{"Digest": {"same-value"}}); !errors.Is(err, ErrTrailerHeaderCollision) {
		t.Fatalf("ValidateTrailers(identical values) error = %v, want %v", err, ErrTrailerHeaderCollision)
	}

	// The collision is case-insensitive.
	lower := http.Header{
		"Trailer": {"digest"},
		"digest":  {"authenticated-header-value"},
	}
	if err := ValidateTrailers(lower, trailers); !errors.Is(err, ErrTrailerHeaderCollision) {
		t.Fatalf("ValidateTrailers(lower) error = %v, want %v", err, ErrTrailerHeaderCollision)
	}
}

func TestValidateTrailersRejectsTruncatedTrailer(t *testing.T) {
	header := http.Header{"Trailer": {"Digest"}}
	if err := ValidateTrailers(header, nil); !errors.Is(err, ErrMissingDeclaredTrailer) {
		t.Fatalf("ValidateTrailers() error = %v, want %v", err, ErrMissingDeclaredTrailer)
	}
	if err := ValidateTrailers(header, http.Header{"Digest": {}}); !errors.Is(err, ErrMissingDeclaredTrailer) {
		t.Fatalf("ValidateTrailers(empty values) error = %v, want %v", err, ErrMissingDeclaredTrailer)
	}
}

func TestValidateTrailersAllowsDuplicateInstances(t *testing.T) {
	header := http.Header{"Trailer": {"Digest"}}
	trailers := http.Header{"Digest": {"first", "second"}}
	if err := ValidateTrailers(header, trailers); err != nil {
		t.Fatalf("ValidateTrailers() error = %v", err)
	}
}

func TestValidateTrailersRequiresDeclaration(t *testing.T) {
	header := http.Header{}
	trailers := http.Header{"Digest": {"sha-256=abc"}}

	if err := ValidateTrailers(header, trailers); !errors.Is(err, ErrUndeclaredTrailer) {
		t.Fatalf("ValidateTrailers() error = %v, want %v", err, ErrUndeclaredTrailer)
	}
	if err := ValidateParsedTrailers(header, trailers); err != nil {
		t.Fatalf("ValidateParsedTrailers() error = %v, want net/http-compatible inference", err)
	}
}

func TestMergeTrailersWithCapturedDeclaration(t *testing.T) {
	digest := hmacDigest([]byte("test-key"))
	header := http.Header{"Content-Type": {"text/plain"}}
	trailers := http.Header{"Digest": {"sha-256=abc"}}

	signHeader := header.Clone()
	signHeader.Set(TrailerHeader, "Digest")
	signature, err := SignHTTP(signHeader, trailers, digest)
	if err != nil {
		t.Fatalf("SignHTTP() error = %v", err)
	}

	merged, err := MergeTrailersWithDeclaration(header, trailers, []string{"Digest"}, signature, digest)
	if err != nil {
		t.Fatalf("MergeTrailersWithDeclaration() error = %v", err)
	}
	if got := merged.Get("Digest"); got != "sha-256=abc" {
		t.Fatalf("merged Digest = %q", got)
	}

	merged, err = MergeTrailersWithDeclaration(signHeader, trailers, nil, signature, digest)
	if err != nil {
		t.Fatalf("MergeTrailersWithDeclaration(explicit header) error = %v", err)
	}
	if got := merged.Get("Digest"); got != "sha-256=abc" {
		t.Fatalf("merged Digest = %q", got)
	}

	undeclared := http.Header{"Digest": {"sha-256=abc"}, "X-Extra": {"no"}}
	if merged, err := MergeTrailersWithDeclaration(header, undeclared, []string{"Digest"}, signature, digest); !errors.Is(err, ErrUndeclaredTrailer) {
		t.Fatalf("MergeTrailersWithDeclaration(undeclared) error = %v, want %v", err, ErrUndeclaredTrailer)
	} else if merged != nil {
		t.Fatalf("MergeTrailersWithDeclaration(undeclared) returned %#v", merged)
	}

	if _, err := MergeTrailersWithDeclaration(header, trailers, []string{"Digest, X-Extra"}, signature, digest); !errors.Is(err, ErrInvalidFieldName) {
		t.Fatalf("MergeTrailersWithDeclaration(invalid declaration) error = %v, want %v", err, ErrInvalidFieldName)
	}
}

func TestValidateSectionsMatchesHTTPPolicy(t *testing.T) {
	header := Section{
		{Name: "Trailer", Value: "Digest"},
		{Name: "Digest", Value: "authenticated"},
	}
	trailers := Section{
		{Name: "Digest", Value: "trailer"},
	}
	if err := ValidateSections(header, trailers); !errors.Is(err, ErrTrailerHeaderCollision) {
		t.Fatalf("ValidateSections() error = %v, want %v", err, ErrTrailerHeaderCollision)
	}

	if err := ValidateSections(
		Section{{Name: "Trailer", Value: "Digest"}},
		Section{{Name: "X-Extra", Value: "no"}},
	); !errors.Is(err, ErrUndeclaredTrailer) {
		t.Fatalf("ValidateSections(undeclared) error = %v, want %v", err, ErrUndeclaredTrailer)
	}
	if err := ValidateSections(
		Section{{Name: "Trailer", Value: "Digest"}},
		nil,
	); !errors.Is(err, ErrMissingDeclaredTrailer) {
		t.Fatalf("ValidateSections(missing) error = %v, want %v", err, ErrMissingDeclaredTrailer)
	}
	if err := ValidateSections(
		Section{{Name: "Trailer", Value: "Authorization"}},
		Section{{Name: "Authorization", Value: "secret"}},
	); !errors.Is(err, ErrForbiddenTrailer) {
		t.Fatalf("ValidateSections(forbidden) error = %v, want %v", err, ErrForbiddenTrailer)
	}
}

func TestMergeTrailersMergesAuthenticatedFields(t *testing.T) {
	digest := hmacDigest([]byte("test-key"))
	header, trailers, signature := signedHeaderAndTrailers(t, digest, "sha-256=:one:", "sha-256=:two:")
	headerBefore := header.Clone()
	trailersBefore := trailers.Clone()

	merged, err := MergeTrailers(header, trailers, signature, digest)
	if err != nil {
		t.Fatalf("MergeTrailers() error = %v", err)
	}
	if got := merged.Values("Digest"); !reflect.DeepEqual(got, []string{"sha-256=:one:", "sha-256=:two:"}) {
		t.Fatalf("merged Digest = %#v", got)
	}
	if got := merged.Get(TrailerHeader); got != "" {
		t.Fatalf("merged Trailer = %q, want removed", got)
	}
	if got := merged.Get("Content-Type"); got != "application/json" {
		t.Fatalf("merged Content-Type = %q", got)
	}
	if !reflect.DeepEqual(header, headerBefore) {
		t.Fatal("MergeTrailers() mutated header input")
	}
	if !reflect.DeepEqual(trailers, trailersBefore) {
		t.Fatal("MergeTrailers() mutated trailers input")
	}
}

func TestMergeSignedTrailers(t *testing.T) {
	digest := hmacDigest([]byte("test-key"))
	header, trailers, signature := signedHeaderAndTrailers(t, digest, "sha-256=:signed:")
	header.Set(SignatureHeader, EncodeSignature(signature))

	merged, err := MergeSignedTrailers(header, trailers, digest)
	if err != nil {
		t.Fatalf("MergeSignedTrailers() error = %v", err)
	}
	if got := merged.Get("Digest"); got != "sha-256=:signed:" {
		t.Fatalf("merged Digest = %q", got)
	}
}

func TestSectionHTTPHeaderPreservesDuplicateOrder(t *testing.T) {
	section := Section{
		{Name: "X-A", Value: "one"},
		{Name: "X-A", Value: "two"},
	}
	header := section.HTTPHeader()
	if got := header.Values("X-A"); !reflect.DeepEqual(got, []string{"one", "two"}) {
		t.Fatalf("HTTPHeader() X-A = %#v", got)
	}
}

func TestMergeTrailersNeverReturnsUnauthenticatedResult(t *testing.T) {
	digest := hmacDigest([]byte("test-key"))
	validHeader, validTrailers, _ := signedHeaderAndTrailers(t, digest, "sha-256=:valid:")

	tests := []struct {
		name         string
		header       http.Header
		trailers     http.Header
		signHeader   http.Header
		signTrailers http.Header
		signature    []byte
		explicitSig  bool
		wantErr      error
	}{
		{
			name: "header and trailer share a name",
			header: http.Header{
				"Trailer": {"Digest"},
				"Digest":  {"authenticated"},
			},
			trailers: http.Header{"Digest": {"trailer"}},
			wantErr:  ErrTrailerHeaderCollision,
		},
		{
			name:     "undeclared trailer",
			header:   http.Header{"Trailer": {"Digest"}},
			trailers: http.Header{"Digest": {"ok"}, "X-Extra": {"no"}},
			wantErr:  ErrUndeclaredTrailer,
		},
		{
			name:     "truncated trailer section",
			header:   http.Header{"Trailer": {"Digest"}},
			trailers: nil,
			wantErr:  ErrMissingDeclaredTrailer,
		},
		{
			name:         "tampered trailer",
			header:       validHeader,
			trailers:     http.Header{"Digest": {"sha-256=:tampered:"}},
			signHeader:   validHeader,
			signTrailers: validTrailers,
			wantErr:      ErrSignatureMismatch,
		},
		{
			name:        "empty signature",
			header:      validHeader,
			trailers:    validTrailers,
			signature:   nil,
			explicitSig: true,
			wantErr:     ErrEmptySignature,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			signature := test.signature
			if !test.explicitSig {
				signHeader := test.header
				if test.signHeader != nil {
					signHeader = test.signHeader
				}
				signTrailers := test.trailers
				if test.signTrailers != nil {
					signTrailers = test.signTrailers
				}
				var err error
				signature, err = SignHTTP(signHeader, signTrailers, digest)
				if err != nil {
					t.Fatalf("SignHTTP() error = %v", err)
				}
			}
			headerBefore := test.header.Clone()
			trailersBefore := test.trailers.Clone()
			merged, err := MergeTrailers(test.header, test.trailers, signature, digest)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("MergeTrailers() error = %v, want %v", err, test.wantErr)
			}
			if merged != nil {
				t.Fatalf("MergeTrailers() returned a partially merged result: %#v", merged)
			}
			if !reflect.DeepEqual(test.header, headerBefore) {
				t.Fatal("MergeTrailers() mutated header input on error")
			}
			if !reflect.DeepEqual(test.trailers, trailersBefore) {
				t.Fatal("MergeTrailers() mutated trailers input on error")
			}
		})
	}
}

func TestMergeSectionsPreservesOrder(t *testing.T) {
	digest := hmacDigest([]byte("test-key"))
	header := Section{
		{Name: "Trailer", Value: "Digest"},
		{Name: "X-A", Value: "1"},
	}
	trailers := Section{
		{Name: "Digest", Value: "one"},
		{Name: "Digest", Value: "two"},
	}
	signature := mustSign(t, header, trailers, digest)

	merged, err := MergeSections(header, trailers, signature, digest)
	if err != nil {
		t.Fatalf("MergeSections() error = %v", err)
	}
	want := Section{
		{Name: "X-A", Value: "1"},
		{Name: "Digest", Value: "one"},
		{Name: "Digest", Value: "two"},
	}
	if !reflect.DeepEqual(merged, want) {
		t.Fatalf("MergeSections() = %#v, want %#v", merged, want)
	}
}
