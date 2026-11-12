package netsec

import (
	"errors"
	"testing"
)

func TestReconstructClientHelloInnerExpandsOuterExtensions(t *testing.T) {
	outer := validatedOuterClientHello()
	inner := validatedInnerClientHello(0x0010, 0x002b)
	inner.SessionID = []byte{0x11, 0x22, 0x33}

	got, err := ReconstructClientHelloInner(
		marshalClientHello(t, outer),
		encodedInner(t, inner, 3),
	)
	if err != nil {
		t.Fatalf("ReconstructClientHelloInner() error = %v", err)
	}

	wantTypes := []uint16{ExtensionServerName, 0x0010, 0x002b, ExtensionEncryptedClientHello}
	if len(got.Extensions) != len(wantTypes) {
		t.Fatalf("len(Extensions) = %d, want %d", len(got.Extensions), len(wantTypes))
	}
	for i, want := range wantTypes {
		if got.Extensions[i].Type != want {
			t.Fatalf("Extensions[%d].Type = 0x%04x, want 0x%04x", i, got.Extensions[i].Type, want)
		}
	}
	if got.Extensions[1].Data[0] != 'o' || got.Extensions[2].Data[0] != 'o' {
		t.Fatal("expanded extensions did not use ClientHelloOuter values")
	}
	if string(got.SessionID) != string(outer.SessionID) {
		t.Fatalf("SessionID = %x, want outer session ID %x", got.SessionID, outer.SessionID)
	}
	name, err := got.ServerName()
	if err != nil {
		t.Fatalf("ServerName() error = %v", err)
	}
	if name != "inner.example" {
		t.Fatalf("ServerName() = %q, want %q", name, "inner.example")
	}
}

func TestReconstructClientHelloInnerAllowsNonContiguousOuterExtensions(t *testing.T) {
	got, err := ReconstructClientHelloInner(
		marshalClientHello(t, validatedOuterClientHello()),
		encodedInner(t, validatedInnerClientHello(0x000a, 0x002b), 0),
	)
	if err != nil {
		t.Fatalf("ReconstructClientHelloInner() error = %v", err)
	}
	wantTypes := []uint16{ExtensionServerName, 0x000a, 0x002b, ExtensionEncryptedClientHello}
	for i, want := range wantTypes {
		if got.Extensions[i].Type != want {
			t.Fatalf("Extensions[%d].Type = 0x%04x, want 0x%04x", i, got.Extensions[i].Type, want)
		}
	}
}

func TestReconstructClientHelloInnerRejectsOutOfOrderReferences(t *testing.T) {
	_, err := ReconstructClientHelloInner(
		marshalClientHello(t, validatedOuterClientHello()),
		encodedInner(t, validatedInnerClientHello(0x002b, 0x0010), 0),
	)
	if !errors.Is(err, ErrOutOfOrderReferencedExtension) {
		t.Fatalf("error = %v, want ErrOutOfOrderReferencedExtension", err)
	}
}

func TestReconstructClientHelloInnerRejectsSelfReferenceCycle(t *testing.T) {
	_, err := ReconstructClientHelloInner(
		marshalClientHello(t, validatedOuterClientHello()),
		encodedInner(t, validatedInnerClientHello(ExtensionECHOuterExtensions), 0),
	)
	if !errors.Is(err, ErrForbiddenSelfReference) {
		t.Fatalf("error = %v, want ErrForbiddenSelfReference", err)
	}
}

func TestReconstructClientHelloInnerRejectsECHReference(t *testing.T) {
	_, err := ReconstructClientHelloInner(
		marshalClientHello(t, validatedOuterClientHello()),
		encodedInner(t, validatedInnerClientHello(ExtensionEncryptedClientHello), 0),
	)
	if !errors.Is(err, ErrForbiddenECHReference) {
		t.Fatalf("error = %v, want ErrForbiddenECHReference", err)
	}
}

func TestReconstructClientHelloInnerRejectsOuterSNIReference(t *testing.T) {
	_, err := ReconstructClientHelloInner(
		marshalClientHello(t, validatedOuterClientHello()),
		encodedInner(t, validatedInnerClientHello(ExtensionServerName), 0),
	)
	if !errors.Is(err, ErrForbiddenServerNameReference) {
		t.Fatalf("error = %v, want ErrForbiddenServerNameReference", err)
	}
}

func TestReconstructClientHelloInnerRejectsOuterExtensionsInOuter(t *testing.T) {
	outer := testClientHello(
		extension(ExtensionECHOuterExtensions, outerExtensionsPayload(0x0010)),
		extension(0x0010, []byte("outer-10")),
		extension(ExtensionEncryptedClientHello, []byte{ECHTypeOuter}),
	)
	_, err := ReconstructClientHelloInner(
		marshalClientHello(t, outer),
		encodedInner(t, validatedInnerClientHello(0x0010), 0),
	)
	if !errors.Is(err, ErrECHOuterExtensionsInOuter) {
		t.Fatalf("error = %v, want ErrECHOuterExtensionsInOuter", err)
	}
}

func TestReconstructClientHelloInnerRejectsMissingReference(t *testing.T) {
	_, err := ReconstructClientHelloInner(
		marshalClientHello(t, validatedOuterClientHello()),
		encodedInner(t, validatedInnerClientHello(0x1234), 0),
	)
	if !errors.Is(err, ErrMissingReferencedExtension) {
		t.Fatalf("error = %v, want ErrMissingReferencedExtension", err)
	}
}

func TestReconstructClientHelloInnerRejectsDuplicateReference(t *testing.T) {
	_, err := ReconstructClientHelloInner(
		marshalClientHello(t, validatedOuterClientHello()),
		encodedInner(t, validatedInnerClientHello(0x0010, 0x0010), 0),
	)
	if !errors.Is(err, ErrDuplicateReferencedExtension) {
		t.Fatalf("error = %v, want ErrDuplicateReferencedExtension", err)
	}
}

func TestReconstructClientHelloInnerRejectsDuplicateAfterExpansion(t *testing.T) {
	inner := testClientHello(
		serverNameExtension("inner.example"),
		extension(0x0010, []byte("inner-10")),
		extension(ExtensionECHOuterExtensions, outerExtensionsPayload(0x0010)),
		extension(ExtensionEncryptedClientHello, []byte{ECHTypeInner}),
	)
	_, err := ReconstructClientHelloInner(
		marshalClientHello(t, validatedOuterClientHello()),
		encodedInner(t, inner, 0),
	)
	if !errors.Is(err, ErrDuplicateExtension) {
		t.Fatalf("error = %v, want ErrDuplicateExtension", err)
	}
}

func TestReconstructClientHelloInnerRejectsDuplicateInnerExtension(t *testing.T) {
	inner := validatedInnerClientHello(0x0010)
	inner.Extensions = append(inner.Extensions, extension(0x0010, []byte("duplicate")))
	_, err := ReconstructClientHelloInner(
		marshalClientHello(t, validatedOuterClientHello()),
		encodedInner(t, inner, 0),
	)
	if !errors.Is(err, ErrDuplicateExtension) {
		t.Fatalf("error = %v, want ErrDuplicateExtension", err)
	}
}

func TestReconstructClientHelloInnerRequiresInnerECHType(t *testing.T) {
	inner := testClientHello(
		serverNameExtension("inner.example"),
		extension(ExtensionEncryptedClientHello, []byte{ECHTypeOuter}),
	)
	_, err := ReconstructClientHelloInner(
		marshalClientHello(t, validatedOuterClientHello()),
		encodedInner(t, inner, 0),
	)
	if !errors.Is(err, ErrMalformedECHInner) {
		t.Fatalf("error = %v, want ErrMalformedECHInner", err)
	}
}

func TestReconstructClientHelloInnerRequiresECHInnerExtension(t *testing.T) {
	inner := testClientHello(serverNameExtension("inner.example"))
	_, err := ReconstructClientHelloInner(
		marshalClientHello(t, validatedOuterClientHello()),
		encodedInner(t, inner, 0),
	)
	if !errors.Is(err, ErrMissingECHInner) {
		t.Fatalf("error = %v, want ErrMissingECHInner", err)
	}
}

func TestReconstructClientHelloInnerRejectsNonZeroPadding(t *testing.T) {
	inner := validatedInnerClientHello(0x0010)
	encoded := encodedInner(t, inner, 2)
	encoded[len(encoded)-1] = 1
	_, err := ReconstructClientHelloInner(
		marshalClientHello(t, validatedOuterClientHello()),
		encoded,
	)
	if !errors.Is(err, ErrNonZeroInnerPadding) {
		t.Fatalf("error = %v, want ErrNonZeroInnerPadding", err)
	}
}

func TestReconstructClientHelloInnerRequiresAtLeastOneReference(t *testing.T) {
	inner := validatedInnerClientHello()
	_, err := ReconstructClientHelloInner(
		marshalClientHello(t, validatedOuterClientHello()),
		encodedInner(t, inner, 0),
	)
	if !errors.Is(err, ErrMalformedOuterExtensions) {
		t.Fatalf("error = %v, want ErrMalformedOuterExtensions", err)
	}
}
