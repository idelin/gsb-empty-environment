package netsec

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"testing"
)

func TestVersion5BindingsUseExactHashInputs(t *testing.T) {
	publicKey := []byte{0x01, 0x23, 0x45, 0x67, 0x89}
	nonce := bytes.Repeat([]byte{0x5a}, ClientNonceSize)
	digest := bytes.Repeat([]byte{0xa5}, BindingHashSize)

	for _, tc := range []struct {
		name string
		want string
		call func(HashFunc) ([]byte, error)
	}{
		{
			name: "client-to-server",
			want: ClientToServerBindingLabel,
			call: func(hash HashFunc) ([]byte, error) {
				return ComputePubKeyAuth(5, publicKey, nonce, hash)
			},
		},
		{
			name: "server-to-client",
			want: ServerToClientBindingLabel,
			call: func(hash HashFunc) ([]byte, error) {
				return ComputeServerChecksum(5, publicKey, nonce, hash)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var gotInput []byte
			hash := func(input []byte) []byte {
				gotInput = append([]byte(nil), input...)
				return digest
			}
			got, err := tc.call(hash)
			if err != nil {
				t.Fatalf("binding failed: %v", err)
			}
			if !bytes.Equal(got, digest) {
				t.Fatalf("unexpected digest: got %x want %x", got, digest)
			}

			wantInput := append([]byte(nil), []byte(tc.want)...)
			wantInput = append(wantInput, 0)
			wantInput = append(wantInput, nonce...)
			wantInput = append(wantInput, publicKey...)
			if !bytes.Equal(gotInput, wantInput) {
				t.Fatalf("unexpected hash input: got %x want %x", gotInput, wantInput)
			}
		})
	}
}

func TestVersion5DefaultHashMatchesSHA256(t *testing.T) {
	publicKey := []byte("server certificate public key")
	nonce := bytes.Repeat([]byte{0x42}, ClientNonceSize)

	got, err := ComputePubKeyAuth(6, publicKey, nonce, nil)
	if err != nil {
		t.Fatalf("ComputePubKeyAuth failed: %v", err)
	}
	input := append([]byte(ClientToServerBindingLabel), 0)
	input = append(input, nonce...)
	input = append(input, publicKey...)
	sum := sha256.Sum256(input)
	if !bytes.Equal(got, sum[:]) {
		t.Fatalf("unexpected SHA-256 binding: got %x want %x", got, sum)
	}
}

func TestLegacyBindingsReplaceNonceWithPublicKeyOnly(t *testing.T) {
	publicKey := []byte{0xfe, 0x20, 0x30, 0x40}
	hash := func([]byte) []byte {
		t.Fatal("legacy binding must not call the hash callback")
		return nil
	}

	for version := 2; version <= 4; version++ {
		pubKeyAuth, err := ComputePubKeyAuth(version, publicKey, nil, hash)
		if err != nil {
			t.Fatalf("version %d pubKeyAuth failed: %v", version, err)
		}
		if !bytes.Equal(pubKeyAuth, publicKey) {
			t.Fatalf("version %d pubKeyAuth = %x, want public key %x", version, pubKeyAuth, publicKey)
		}

		checksum, err := ComputeServerChecksum(version, publicKey, nil, hash)
		if err != nil {
			t.Fatalf("version %d checksum failed: %v", version, err)
		}
		want := []byte{0xff, 0x20, 0x30, 0x40}
		if !bytes.Equal(checksum, want) {
			t.Fatalf("version %d checksum = %x, want %x", version, checksum, want)
		}
	}
}

func TestDefaultPolicyRejectsVersion2BeforeNonceOrCredentials(t *testing.T) {
	nonces := &countingReader{Reader: bytes.NewReader(bytes.Repeat([]byte{0x11}, ClientNonceSize))}
	sent := 0
	client := mustClient(t, Config{
		Random: nonces,
		SendCredentials: func(context.Context) error {
			sent++
			return nil
		},
	})

	_, err := client.Begin(2, []byte("server public key"))
	if !errors.Is(err, ErrVersionDowngrade) {
		t.Fatalf("Begin error = %v, want ErrVersionDowngrade", err)
	}
	if nonces.reads != 0 {
		t.Fatalf("nonce source read %d times after downgrade rejection", nonces.reads)
	}
	if sent != 0 {
		t.Fatalf("credentials sent %d times after downgrade rejection", sent)
	}
}

func TestLegacyVersionRequiresExplicitPolicy(t *testing.T) {
	publicKey := []byte{0x10, 0x20, 0x30}
	nonce := bytes.Repeat([]byte{0x33}, ClientNonceSize)
	sent := 0
	client := mustClient(t, Config{
		MinVersion: 2,
		Random:     bytes.NewReader(nonce),
		SendCredentials: func(context.Context) error {
			sent++
			return nil
		},
	})

	binding, err := client.Begin(2, publicKey)
	if err != nil {
		t.Fatalf("Begin failed: %v", err)
	}
	if binding.Version != 2 {
		t.Fatalf("negotiated version = %d, want 2", binding.Version)
	}
	if len(binding.ClientNonce) != ClientNonceSize {
		t.Fatalf("nonce length = %d, want %d", len(binding.ClientNonce), ClientNonceSize)
	}
	if !bytes.Equal(binding.PubKeyAuth, publicKey) {
		t.Fatalf("pubKeyAuth = %x, want %x", binding.PubKeyAuth, publicKey)
	}

	checksum, err := ComputeServerChecksum(2, publicKey, binding.ClientNonce, nil)
	if err != nil {
		t.Fatalf("ComputeServerChecksum failed: %v", err)
	}
	if sent != 0 {
		t.Fatalf("credentials sent before Complete: %d", sent)
	}
	if err := client.Complete(context.Background(), ServerReply{Version: 2, Checksum: checksum}); err != nil {
		t.Fatalf("Complete failed: %v", err)
	}
	if sent != 1 {
		t.Fatalf("credentials sent %d times, want 1", sent)
	}
}

func TestSuccessfulVersion5HandshakeSendsCredentials(t *testing.T) {
	publicKey := []byte("server certificate public key")
	nonce := bytes.Repeat([]byte{0x77}, ClientNonceSize)
	sent := 0
	client := mustClient(t, Config{
		MaxVersion: 6,
		Random:     bytes.NewReader(nonce),
		SendCredentials: func(context.Context) error {
			sent++
			return nil
		},
	})

	binding, err := client.Begin(99, publicKey)
	if err != nil {
		t.Fatalf("Begin failed: %v", err)
	}
	if binding.Version != 6 {
		t.Fatalf("negotiated version = %d, want local maximum 6", binding.Version)
	}
	if !bytes.Equal(binding.ClientNonce, nonce) {
		t.Fatalf("client nonce = %x, want %x", binding.ClientNonce, nonce)
	}
	if sent != 0 {
		t.Fatalf("credentials sent during Begin")
	}

	checksum, err := ComputeServerChecksum(binding.Version, publicKey, binding.ClientNonce, nil)
	if err != nil {
		t.Fatalf("ComputeServerChecksum failed: %v", err)
	}
	if err := client.Complete(context.Background(), ServerReply{Version: 99, Checksum: checksum}); err != nil {
		t.Fatalf("Complete failed: %v", err)
	}
	if sent != 1 {
		t.Fatalf("credentials sent %d times, want 1", sent)
	}
}

func TestModifiedPublicKeyIsRejected(t *testing.T) {
	publicKey := []byte("original server certificate public key")
	modifiedKey := append([]byte(nil), publicKey...)
	modifiedKey[len(modifiedKey)-1] ^= 0x01

	client := mustClient(t, Config{
		Random: bytes.NewReader(bytes.Repeat([]byte{0x44}, ClientNonceSize)),
		SendCredentials: func(context.Context) error {
			t.Fatal("credentials sent after public key substitution")
			return nil
		},
	})

	binding, err := client.Begin(5, publicKey)
	if err != nil {
		t.Fatalf("Begin failed: %v", err)
	}
	checksum, err := ComputeServerChecksum(binding.Version, modifiedKey, binding.ClientNonce, nil)
	if err != nil {
		t.Fatalf("ComputeServerChecksum failed: %v", err)
	}
	if err := client.Complete(context.Background(), ServerReply{Version: 5, Checksum: checksum}); !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("Complete error = %v, want ErrChecksumMismatch", err)
	}
}

func TestReplayOfPreviousChecksumIsRejected(t *testing.T) {
	publicKey := []byte("server certificate public key")
	firstNonce := bytes.Repeat([]byte{0x01}, ClientNonceSize)
	secondNonce := bytes.Repeat([]byte{0x02}, ClientNonceSize)
	nonceBytes := append(append([]byte(nil), firstNonce...), secondNonce...)
	sent := 0
	client := mustClient(t, Config{
		Random: bytes.NewReader(nonceBytes),
		SendCredentials: func(context.Context) error {
			sent++
			return nil
		},
	})

	first, err := client.Begin(5, publicKey)
	if err != nil {
		t.Fatalf("first Begin failed: %v", err)
	}
	firstChecksum, err := ComputeServerChecksum(5, publicKey, first.ClientNonce, nil)
	if err != nil {
		t.Fatalf("first checksum failed: %v", err)
	}
	if err := client.Complete(context.Background(), ServerReply{Version: 5, Checksum: firstChecksum}); err != nil {
		t.Fatalf("first Complete failed: %v", err)
	}

	second, err := client.Begin(5, publicKey)
	if err != nil {
		t.Fatalf("second Begin failed: %v", err)
	}
	if bytes.Equal(first.ClientNonce, second.ClientNonce) {
		t.Fatal("client nonce was reused across handshakes")
	}
	if err := client.Complete(context.Background(), ServerReply{Version: 5, Checksum: firstChecksum}); !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("replayed Complete error = %v, want ErrChecksumMismatch", err)
	}
	if sent != 1 {
		t.Fatalf("credentials sent %d times, want only the first handshake", sent)
	}
}

func TestRepeatedNonceFromSourceIsRejected(t *testing.T) {
	publicKey := []byte("server certificate public key")
	nonce := bytes.Repeat([]byte{0x09}, ClientNonceSize)
	sent := 0
	client := mustClient(t, Config{
		Random: bytes.NewReader(append(append([]byte(nil), nonce...), nonce...)),
		SendCredentials: func(context.Context) error {
			sent++
			return nil
		},
	})

	first, err := client.Begin(5, publicKey)
	if err != nil {
		t.Fatalf("first Begin failed: %v", err)
	}
	checksum, err := ComputeServerChecksum(5, publicKey, first.ClientNonce, nil)
	if err != nil {
		t.Fatalf("checksum failed: %v", err)
	}
	if err := client.Complete(context.Background(), ServerReply{Version: 5, Checksum: checksum}); err != nil {
		t.Fatalf("first Complete failed: %v", err)
	}

	if _, err := client.Begin(5, publicKey); !errors.Is(err, ErrNonceReuse) {
		t.Fatalf("second Begin error = %v, want ErrNonceReuse", err)
	}
	if sent != 1 {
		t.Fatalf("credentials sent %d times, want 1", sent)
	}
}

func TestChecksumMismatchDoesNotSendCredentials(t *testing.T) {
	publicKey := []byte("server certificate public key")
	client := mustClient(t, Config{
		Random: bytes.NewReader(bytes.Repeat([]byte{0x66}, ClientNonceSize)),
		SendCredentials: func(context.Context) error {
			t.Fatal("credentials sent after checksum mismatch")
			return nil
		},
	})

	binding, err := client.Begin(5, publicKey)
	if err != nil {
		t.Fatalf("Begin failed: %v", err)
	}
	checksum, err := ComputeServerChecksum(5, publicKey, binding.ClientNonce, nil)
	if err != nil {
		t.Fatalf("ComputeServerChecksum failed: %v", err)
	}
	checksum[0] ^= 0xff
	if err := client.Complete(context.Background(), ServerReply{Version: 5, Checksum: checksum}); !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("Complete error = %v, want ErrChecksumMismatch", err)
	}
	if err := client.Complete(context.Background(), ServerReply{Version: 5, Checksum: checksum}); !errors.Is(err, ErrNoActiveHandshake) {
		t.Fatalf("second Complete error = %v, want ErrNoActiveHandshake", err)
	}
}

func TestServerVersionChangeAbortsHandshake(t *testing.T) {
	publicKey := []byte("server certificate public key")

	t.Run("negotiated version changes", func(t *testing.T) {
		client := mustClient(t, Config{
			Random: bytes.NewReader(bytes.Repeat([]byte{0x55}, ClientNonceSize)),
			SendCredentials: func(context.Context) error {
				t.Fatal("credentials sent after version change")
				return nil
			},
		})
		if _, err := client.Begin(6, publicKey); err != nil {
			t.Fatalf("Begin failed: %v", err)
		}
		if err := client.Complete(context.Background(), ServerReply{Version: 5}); !errors.Is(err, ErrVersionChanged) {
			t.Fatalf("Complete error = %v, want ErrVersionChanged", err)
		}
	})

	t.Run("raw server version changes with same effective version", func(t *testing.T) {
		client := mustClient(t, Config{
			Random: bytes.NewReader(bytes.Repeat([]byte{0x56}, ClientNonceSize)),
			SendCredentials: func(context.Context) error {
				t.Fatal("credentials sent after version change")
				return nil
			},
		})
		if _, err := client.Begin(99, publicKey); err != nil {
			t.Fatalf("Begin failed: %v", err)
		}
		if err := client.Complete(context.Background(), ServerReply{Version: 6}); !errors.Is(err, ErrVersionChanged) {
			t.Fatalf("Complete error = %v, want ErrVersionChanged", err)
		}
	})
}

func TestBindingPrimitiveValidation(t *testing.T) {
	publicKey := []byte("server certificate public key")
	nonce := bytes.Repeat([]byte{0x01}, ClientNonceSize)

	if _, err := ComputePubKeyAuth(1, publicKey, nonce, nil); !errors.Is(err, ErrInvalidVersion) {
		t.Fatalf("version 1 error = %v, want ErrInvalidVersion", err)
	}
	if _, err := ComputePubKeyAuth(5, nil, nonce, nil); !errors.Is(err, ErrMissingPublicKey) {
		t.Fatalf("missing key error = %v, want ErrMissingPublicKey", err)
	}
	if _, err := ComputePubKeyAuth(5, publicKey, nonce[:31], nil); !errors.Is(err, ErrInvalidNonce) {
		t.Fatalf("short nonce error = %v, want ErrInvalidNonce", err)
	}
	shortHash := func([]byte) []byte { return []byte{0x01} }
	if _, err := ComputeServerChecksum(5, publicKey, nonce, shortHash); !errors.Is(err, ErrInvalidHash) {
		t.Fatalf("short hash error = %v, want ErrInvalidHash", err)
	}

	wrong := append([]byte(nil), nonce...)
	if err := VerifyServerChecksum(5, publicKey, nonce, wrong, nil); !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("VerifyServerChecksum error = %v, want ErrChecksumMismatch", err)
	}
}

func mustClient(t *testing.T, cfg Config) *Client {
	t.Helper()
	client, err := NewClient(cfg)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	return client
}

type countingReader struct {
	io.Reader
	reads int
}

func (r *countingReader) Read(p []byte) (int, error) {
	r.reads++
	return r.Reader.Read(p)
}
