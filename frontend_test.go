package netsec

import (
	"errors"
	"testing"
)

func TestFrontendRoutesByInnerSNI(t *testing.T) {
	outer := validatedOuterClientHello()
	encoded := encodedInner(t, validatedInnerClientHello(0x0010, 0x002b), 0)

	frontend := &ECHFrontend{
		Decrypt: func(got *ClientHello) ([]byte, error) {
			if got.ExtensionCount(ExtensionEncryptedClientHello) != 1 {
				t.Fatalf("decrypter received outer ClientHello without ECH extension")
			}
			return encoded, nil
		},
		RetryConfigs: []byte{0xde, 0xad},
	}

	result, err := frontend.ProcessClientHello(marshalClientHello(t, outer))
	if err != nil {
		t.Fatalf("ProcessClientHello() error = %v", err)
	}
	if !result.ECHAccepted || result.ECHRejected {
		t.Fatalf("ECH flags = accepted:%v rejected:%v, want accepted", result.ECHAccepted, result.ECHRejected)
	}
	if result.ServerName != "inner.example" {
		t.Fatalf("ServerName = %q, want %q", result.ServerName, "inner.example")
	}
	if err := result.ValidateRouting(); err != nil {
		t.Fatalf("ValidateRouting() error = %v", err)
	}

	wireCH, err := ParseClientHello(result.Wire)
	if err != nil {
		t.Fatalf("ParseClientHello(result.Wire) error = %v", err)
	}
	name, err := wireCH.ServerName()
	if err != nil {
		t.Fatalf("ServerName() error = %v", err)
	}
	if name != "inner.example" {
		t.Fatalf("forwarded SNI = %q, want inner SNI", name)
	}
}

func TestFrontendDecryptionFailureUsesPublicNameAndRetryConfig(t *testing.T) {
	outer := validatedOuterClientHello()
	validInnerWire := encodedInner(t, validatedInnerClientHello(0x0010, 0x002b), 0)
	decryptErr := errors.New("authentication failed")
	retry := []byte{0x01, 0x02, 0x03}

	frontend := &ECHFrontend{
		Decrypt: func(*ClientHello) ([]byte, error) {
			return validInnerWire, decryptErr
		},
		RetryConfigs: retry,
	}
	result, err := frontend.ProcessClientHello(marshalClientHello(t, outer))
	if err != nil {
		t.Fatalf("ProcessClientHello() error = %v", err)
	}
	if result.ECHAccepted || !result.ECHRejected {
		t.Fatalf("ECH flags = accepted:%v rejected:%v, want rejected", result.ECHAccepted, result.ECHRejected)
	}
	if result.ServerName != "public.example" {
		t.Fatalf("ServerName = %q, want public name", result.ServerName)
	}
	if len(result.RetryConfigs) != len(retry) {
		t.Fatalf("RetryConfigs = %x, want %x", result.RetryConfigs, retry)
	}
	for i := range retry {
		if result.RetryConfigs[i] != retry[i] {
			t.Fatalf("RetryConfigs = %x, want %x", result.RetryConfigs, retry)
		}
	}
	name, err := result.ClientHello.ServerName()
	if err != nil {
		t.Fatalf("ServerName() error = %v", err)
	}
	if name != "public.example" {
		t.Fatalf("fallback ClientHello SNI = %q, want public name", name)
	}
	ext := result.RetryConfigExtension()
	if ext == nil || ext.Type != ExtensionEncryptedClientHello {
		t.Fatalf("RetryConfigExtension() = %+v, want encrypted_client_hello", ext)
	}
}

func TestFrontendDecryptionFailureUsesConfiguredPublicName(t *testing.T) {
	outer := testClientHello(
		serverNameExtension("attacker-visible.example"),
		extension(ExtensionEncryptedClientHello, []byte{ECHTypeOuter}),
	)
	frontend := &ECHFrontend{
		Decrypt:      func(*ClientHello) ([]byte, error) { return nil, errors.New("bad ciphertext") },
		PublicName:   "public.example",
		RetryConfigs: []byte{1},
	}
	result, err := frontend.ProcessClientHello(marshalClientHello(t, outer))
	if err != nil {
		t.Fatalf("ProcessClientHello() error = %v", err)
	}
	if result.ServerName != "public.example" {
		t.Fatalf("ServerName = %q, want configured public name", result.ServerName)
	}
}

func TestFrontendRejectsMalformedInnerAfterSuccessfulDecryption(t *testing.T) {
	outer := validatedOuterClientHello()
	badInner := encodedInner(t, validatedInnerClientHello(0x002b, 0x0010), 0)
	frontend := &ECHFrontend{
		Decrypt:      func(*ClientHello) ([]byte, error) { return badInner, nil },
		RetryConfigs: []byte{1},
	}
	result, err := frontend.ProcessClientHello(marshalClientHello(t, outer))
	if err == nil {
		t.Fatalf("ProcessClientHello() = %+v, want illegal_parameter", result)
	}
	if !errors.Is(err, ErrOutOfOrderReferencedExtension) {
		t.Fatalf("error = %v, want ErrOutOfOrderReferencedExtension", err)
	}
	if AlertForError(err) != AlertIllegalParameter {
		t.Fatalf("AlertForError() = %d, want illegal_parameter", AlertForError(err))
	}
}

func TestFrontendDoesNotFallBackToOuterSNIOnAcceptedECH(t *testing.T) {
	outer := validatedOuterClientHello()
	inner := testClientHello(
		extension(ExtensionEncryptedClientHello, []byte{ECHTypeInner}),
	)
	frontend := &ECHFrontend{
		Decrypt: func(*ClientHello) ([]byte, error) {
			return encodedInner(t, inner, 0), nil
		},
	}
	result, err := frontend.ProcessClientHello(marshalClientHello(t, outer))
	if err != nil {
		t.Fatalf("ProcessClientHello() error = %v", err)
	}
	if result.ServerName != "" {
		t.Fatalf("ServerName = %q, want empty inner SNI instead of outer SNI", result.ServerName)
	}
	if !result.ECHAccepted {
		t.Fatal("ECHAccepted = false, want true")
	}
}

func TestFrontendWithoutECHUsesOuterSNI(t *testing.T) {
	outer := testClientHello(serverNameExtension("public.example"))
	frontend := &ECHFrontend{RetryConfigs: []byte{0x01}}
	result, err := frontend.ProcessClientHello(marshalClientHello(t, outer))
	if err != nil {
		t.Fatalf("ProcessClientHello() error = %v", err)
	}
	if result.ServerName != "public.example" {
		t.Fatalf("ServerName = %q, want public.example", result.ServerName)
	}
	if result.ECHAccepted || result.ECHRejected {
		t.Fatalf("ECH flags = accepted:%v rejected:%v, want neither", result.ECHAccepted, result.ECHRejected)
	}
	if len(result.RetryConfigs) != 1 || result.RetryConfigs[0] != 0x01 {
		t.Fatalf("RetryConfigs = %x, want configured retry config", result.RetryConfigs)
	}
}
