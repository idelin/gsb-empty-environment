package netsec

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"testing"
)

func makeStatusList(t *testing.T, purpose string, bits []byte) []byte {
	t.Helper()

	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	if _, err := zw.Write(bits); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(compressed.Bytes())
	return []byte(fmt.Sprintf(`{"credentialSubject":{"statusPurpose":%q,"encodedList":%q}}`, purpose, encoded))
}

func TestParseBitstringStatusListAndRevoked(t *testing.T) {
	// Index 0 is the high bit of byte 0; index 15 is the low bit of byte 1.
	raw := makeStatusList(t, "revocation", []byte{0b10000000, 0b00000001})

	list, err := ParseBitstringStatusList(raw)
	if err != nil {
		t.Fatalf("ParseBitstringStatusList: %v", err)
	}
	if list.Purpose != "revocation" {
		t.Fatalf("Purpose = %q", list.Purpose)
	}

	tests := []struct {
		index uint64
		want  bool
	}{
		{0, true},
		{1, false},
		{7, false},
		{8, false},
		{15, true},
	}
	for _, tt := range tests {
		got, err := list.Revoked(tt.index)
		if err != nil {
			t.Fatalf("Revoked(%d): %v", tt.index, err)
		}
		if got != tt.want {
			t.Fatalf("Revoked(%d) = %v, want %v", tt.index, got, tt.want)
		}
	}
	if _, err := list.Revoked(16); !errors.Is(err, ErrInvalidStatusList) {
		t.Fatalf("out-of-range index error = %v, want ErrInvalidStatusList", err)
	}
}

func TestParseBitstringStatusListRejectsWrongPurpose(t *testing.T) {
	raw := makeStatusList(t, "suspension", []byte{0xff})
	if _, err := ParseBitstringStatusList(raw); !errors.Is(err, ErrInvalidStatusList) {
		t.Fatalf("wrong purpose error = %v, want ErrInvalidStatusList", err)
	}
}

func TestVerifierDoesNotFetchUntrustedHost(t *testing.T) {
	fetches := 0
	v := &Verifier{
		Fetcher: StatusListFetcherFunc(func(_ context.Context, _ string) ([]byte, error) {
			fetches++
			return nil, errors.New("must not be called")
		}),
	}

	_, err := v.CheckRevocation(context.Background(), "https://issuer.example", StatusListReference{
		URI:   "https://evil.example/status/1",
		Index: 1,
	})
	if !errors.Is(err, ErrStatusListHostMismatch) {
		t.Fatalf("CheckRevocation error = %v, want ErrStatusListHostMismatch", err)
	}
	if fetches != 0 {
		t.Fatalf("fetcher called %d times for an untrusted status list", fetches)
	}
}

func TestVerifierChecksSameHostStatusList(t *testing.T) {
	fetchedURI := ""
	raw := makeStatusList(t, "revocation", []byte{0b00000001})
	v := &Verifier{
		Fetcher: StatusListFetcherFunc(func(_ context.Context, uri string) ([]byte, error) {
			fetchedURI = uri
			return raw, nil
		}),
	}

	result, err := v.CheckCredential(context.Background(), Credential{
		Issuer: "https://issuer.example",
		StatusList: &StatusListReference{
			URI:   "https://issuer.example/status/1",
			Index: 7,
		},
	})
	if err != nil {
		t.Fatalf("CheckCredential: %v", err)
	}
	if !result.Revoked {
		t.Fatal("credential should be revoked")
	}
	if fetchedURI != "https://issuer.example/status/1" {
		t.Fatalf("fetched URI = %q", fetchedURI)
	}
}

func TestVerifierNoStatusListReference(t *testing.T) {
	v := &Verifier{}
	_, err := v.CheckCredential(context.Background(), Credential{Issuer: "https://issuer.example"})
	if !errors.Is(err, ErrNoStatusList) {
		t.Fatalf("CheckCredential error = %v, want ErrNoStatusList", err)
	}
}
