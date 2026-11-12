package netsec

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
	"time"
)

func testKey(id byte) Key {
	return Key{ID: id, Secret: bytes.Repeat([]byte{byte(id + 1)}, 32)}
}

func testBinding() Binding {
	return Binding{
		Principal: "alice",
		Descriptor: Descriptor{
			Type: DescriptorPath,
			Path: []string{"orders", "2026-11"},
		},
		Endpoint:  "grpc+tls://flight-a.example:50051",
		Partition: 7,
	}
}

func mustKeyring(t *testing.T, current Key, previous ...Key) *Keyring {
	t.Helper()
	ring, err := NewKeyring(current, previous...)
	if err != nil {
		t.Fatal(err)
	}
	return ring
}

func mustIssue(t *testing.T, ring *Keyring, binding Binding, expiresAt time.Time) []byte {
	t.Helper()
	ticket, err := ring.Issue(binding, expiresAt)
	if err != nil {
		t.Fatal(err)
	}
	return ticket
}

func TestTicketRoundTrip(t *testing.T) {
	ring := mustKeyring(t, testKey(7))
	now := time.Date(2026, 11, 12, 9, 30, 0, 0, time.UTC)
	expiresAt := now.Add(2 * time.Minute)
	binding := testBinding()

	ticket := mustIssue(t, ring, binding, expiresAt)
	claims, err := ring.VerifyAt(ticket, binding, now)
	if err != nil {
		t.Fatalf("VerifyAt() error = %v", err)
	}
	if !claims.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("ExpiresAt = %v, want %v", claims.ExpiresAt, expiresAt)
	}
	if claims.KeyID != 7 {
		t.Fatalf("KeyID = %d, want 7", claims.KeyID)
	}
	if claims.Binding().Principal != binding.Principal ||
		claims.Binding().Endpoint != binding.Endpoint ||
		claims.Binding().Partition != binding.Partition {
		t.Fatalf("claims binding = %#v, want %#v", claims.Binding(), binding)
	}
}

func TestCommandDescriptorRoundTrip(t *testing.T) {
	ring := mustKeyring(t, testKey(3))
	now := time.Now()
	binding := Binding{
		Principal: "alice",
		Descriptor: Descriptor{
			Type:    DescriptorCommand,
			Command: []byte(`{"dataset":"orders","snapshot":42}`),
		},
		Endpoint:  "grpc+tls://flight-a.example:50051",
		Partition: 2,
	}
	ticket := mustIssue(t, ring, binding, now.Add(time.Minute))

	claims, err := ring.VerifyAt(ticket, binding, now)
	if err != nil {
		t.Fatalf("VerifyAt() error = %v", err)
	}
	if !descriptorEqual(claims.Descriptor, binding.Descriptor) {
		t.Fatalf("Descriptor = %#v, want %#v", claims.Descriptor, binding.Descriptor)
	}
}

func TestEndpointSwapRejected(t *testing.T) {
	ring := mustKeyring(t, testKey(1))
	now := time.Now()
	binding := testBinding()
	ticket := mustIssue(t, ring, binding, now.Add(time.Minute))

	if _, err := ring.VerifyRequestAt(ticket, binding.Principal, binding.Endpoint, now); err != nil {
		t.Fatalf("VerifyRequestAt() correct endpoint error = %v", err)
	}

	otherEndpoint := binding
	otherEndpoint.Endpoint = "grpc+tls://flight-b.example:50051"
	_, err := ring.VerifyRequestAt(ticket, binding.Principal, otherEndpoint.Endpoint, now)
	if !errors.Is(err, ErrEndpointMismatch) {
		t.Fatalf("VerifyRequestAt() error = %v, want ErrEndpointMismatch", err)
	}
	_, err = ring.VerifyAt(ticket, otherEndpoint, now)
	if !errors.Is(err, ErrEndpointMismatch) {
		t.Fatalf("VerifyAt() error = %v, want ErrEndpointMismatch", err)
	}
}

func TestCrossUserRejected(t *testing.T) {
	ring := mustKeyring(t, testKey(1))
	now := time.Now()
	binding := testBinding()
	ticket := mustIssue(t, ring, binding, now.Add(time.Minute))

	_, err := ring.VerifyRequestAt(ticket, "bob", binding.Endpoint, now)
	if !errors.Is(err, ErrPrincipalMismatch) {
		t.Fatalf("VerifyRequestAt() error = %v, want ErrPrincipalMismatch", err)
	}

	otherUser := binding
	otherUser.Principal = "bob"
	_, err = ring.VerifyAt(ticket, otherUser, now)
	if !errors.Is(err, ErrPrincipalMismatch) {
		t.Fatalf("VerifyAt() error = %v, want ErrPrincipalMismatch", err)
	}
}

func TestDoGetRequestReturnsAuthenticatedClaims(t *testing.T) {
	ring := mustKeyring(t, testKey(1))
	now := time.Now()
	binding := testBinding()
	ticket := mustIssue(t, ring, binding, now.Add(time.Minute))

	claims, err := ring.VerifyRequestAt(ticket, binding.Principal, binding.Endpoint, now)
	if err != nil {
		t.Fatalf("VerifyRequestAt() error = %v", err)
	}
	if !descriptorEqual(claims.Descriptor, binding.Descriptor) {
		t.Fatalf("Descriptor = %#v, want %#v", claims.Descriptor, binding.Descriptor)
	}
	if claims.Partition != binding.Partition {
		t.Fatalf("Partition = %d, want %d", claims.Partition, binding.Partition)
	}
}

func TestPartitionChangeRejected(t *testing.T) {
	ring := mustKeyring(t, testKey(1))
	now := time.Now()
	binding := testBinding()
	ticket := mustIssue(t, ring, binding, now.Add(time.Minute))

	otherPartition := binding
	otherPartition.Partition = binding.Partition + 1
	_, err := ring.VerifyAt(ticket, otherPartition, now)
	if !errors.Is(err, ErrPartitionMismatch) {
		t.Fatalf("VerifyAt() error = %v, want ErrPartitionMismatch", err)
	}
}

func TestDescriptorChangeRejected(t *testing.T) {
	ring := mustKeyring(t, testKey(1))
	now := time.Now()
	binding := testBinding()
	ticket := mustIssue(t, ring, binding, now.Add(time.Minute))

	tests := []struct {
		name       string
		descriptor Descriptor
	}{
		{
			name: "different path segment",
			descriptor: Descriptor{
				Type: DescriptorPath,
				Path: []string{"orders", "2026-12"},
			},
		},
		{
			name: "different path boundaries",
			descriptor: Descriptor{
				Type: DescriptorPath,
				Path: []string{"orders2026-11"},
			},
		},
		{
			name: "command instead of path",
			descriptor: Descriptor{
				Type:    DescriptorCommand,
				Command: []byte(`{"dataset":"orders"}`),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			other := binding
			other.Descriptor = tt.descriptor
			_, err := ring.VerifyAt(ticket, other, now)
			if !errors.Is(err, ErrDescriptorMismatch) {
				t.Fatalf("VerifyAt() error = %v, want ErrDescriptorMismatch", err)
			}
		})
	}
}

func TestEveryAuthenticatedByteIsMACProtected(t *testing.T) {
	ring := mustKeyring(t, testKey(1))
	now := time.Now()
	binding := testBinding()
	ticket := mustIssue(t, ring, binding, now.Add(time.Minute))

	payloadStart := ticketHeaderSize
	payloadEnd := len(ticket) - ticketMACSize
	for offset := payloadStart; offset < payloadEnd; offset++ {
		t.Run(fmt.Sprintf("payload_byte_%d", offset), func(t *testing.T) {
			tampered := bytes.Clone(ticket)
			tampered[offset] ^= 0x01
			_, err := ring.VerifyAt(tampered, binding, now)
			if !errors.Is(err, ErrBadMAC) {
				t.Fatalf("VerifyAt() error = %v, want ErrBadMAC", err)
			}
		})
	}
}

func TestMACByteChangeRejected(t *testing.T) {
	ring := mustKeyring(t, testKey(1))
	now := time.Now()
	binding := testBinding()
	ticket := mustIssue(t, ring, binding, now.Add(time.Minute))

	tampered := bytes.Clone(ticket)
	tampered[len(tampered)-1] ^= 0x80
	_, err := ring.VerifyAt(tampered, binding, now)
	if !errors.Is(err, ErrBadMAC) {
		t.Fatalf("VerifyAt() error = %v, want ErrBadMAC", err)
	}
}

func TestExpiredTicketRejected(t *testing.T) {
	ring := mustKeyring(t, testKey(1))
	binding := testBinding()
	expiresAt := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	ticket := mustIssue(t, ring, binding, expiresAt)

	if _, err := ring.VerifyAt(ticket, binding, expiresAt.Add(-time.Nanosecond)); err != nil {
		t.Fatalf("VerifyAt() before expiry error = %v", err)
	}
	for _, now := range []time.Time{expiresAt, expiresAt.Add(time.Second)} {
		_, err := ring.VerifyAt(ticket, binding, now)
		if !errors.Is(err, ErrExpired) {
			t.Fatalf("VerifyAt() at %v error = %v, want ErrExpired", now, err)
		}
	}
}

func TestTruncatedTicketRejected(t *testing.T) {
	ring := mustKeyring(t, testKey(1))
	now := time.Now()
	ticket := mustIssue(t, ring, testBinding(), now.Add(time.Minute))

	cuts := []int{0, 1, ticketHeaderSize - 1, ticketHeaderSize, len(ticket) - ticketMACSize, len(ticket) - 1}
	for _, size := range cuts {
		t.Run(fmt.Sprintf("length_%d", size), func(t *testing.T) {
			_, err := ring.VerifyAt(ticket[:size], testBinding(), now)
			if !errors.Is(err, ErrTruncated) {
				t.Fatalf("VerifyAt() error = %v, want ErrTruncated", err)
			}
		})
	}
}

func TestUnexpectedTrailingBytesRejected(t *testing.T) {
	ring := mustKeyring(t, testKey(1))
	now := time.Now()
	ticket := mustIssue(t, ring, testBinding(), now.Add(time.Minute))

	tampered := append(bytes.Clone(ticket), 0x00)
	_, err := ring.VerifyAt(tampered, testBinding(), now)
	if !errors.Is(err, ErrMalformed) {
		t.Fatalf("VerifyAt() error = %v, want ErrMalformed", err)
	}
}

func TestPayloadLengthChangesRejected(t *testing.T) {
	ring := mustKeyring(t, testKey(1))
	now := time.Now()
	ticket := mustIssue(t, ring, testBinding(), now.Add(time.Minute))

	tests := []struct {
		name   string
		length uint32
		want   error
	}{
		{name: "too long", length: uint32(len(ticket) - ticketHeaderSize - ticketMACSize + 1), want: ErrTruncated},
		{name: "too short", length: uint32(len(ticket) - ticketHeaderSize - ticketMACSize - 1), want: ErrMalformed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tampered := bytes.Clone(ticket)
			binaryBigEndianPutUint32(tampered[ticketHeaderSize-4:ticketHeaderSize], tt.length)
			_, err := ring.VerifyAt(tampered, testBinding(), now)
			if !errors.Is(err, tt.want) {
				t.Fatalf("VerifyAt() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestUnknownVersionRejected(t *testing.T) {
	ring := mustKeyring(t, testKey(1))
	now := time.Now()
	ticket := mustIssue(t, ring, testBinding(), now.Add(time.Minute))

	for _, version := range []byte{0, 2, 255} {
		t.Run(fmt.Sprintf("version_%d", version), func(t *testing.T) {
			tampered := bytes.Clone(ticket)
			tampered[0] = version
			_, err := ring.VerifyAt(tampered, testBinding(), now)
			if !errors.Is(err, ErrUnsupportedTicket) {
				t.Fatalf("VerifyAt() error = %v, want ErrUnsupportedTicket", err)
			}
		})
	}
}

func TestUnknownKeyRejected(t *testing.T) {
	ring := mustKeyring(t, testKey(1))
	now := time.Now()
	ticket := mustIssue(t, ring, testBinding(), now.Add(time.Minute))

	tampered := bytes.Clone(ticket)
	tampered[1] = 99
	_, err := ring.VerifyAt(tampered, testBinding(), now)
	if !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("VerifyAt() error = %v, want ErrUnknownKey", err)
	}
}

func TestKnownKeySubstitutionRejected(t *testing.T) {
	current := testKey(2)
	old := testKey(1)
	ring := mustKeyring(t, current, old)
	now := time.Now()
	ticket := mustIssue(t, ring, testBinding(), now.Add(time.Minute))

	tampered := bytes.Clone(ticket)
	tampered[1] = old.ID
	_, err := ring.VerifyAt(tampered, testBinding(), now)
	if !errors.Is(err, ErrBadMAC) {
		t.Fatalf("VerifyAt() error = %v, want ErrBadMAC", err)
	}
}

func TestKeyRotationWindow(t *testing.T) {
	now := time.Now()
	binding := testBinding()
	old := testKey(1)
	current := testKey(2)
	oldTicket := mustIssue(t, mustKeyring(t, old), binding, now.Add(time.Minute))

	rotating := mustKeyring(t, current, old)
	if _, err := rotating.VerifyAt(oldTicket, binding, now); err != nil {
		t.Fatalf("old ticket during rotation error = %v", err)
	}
	currentTicket := mustIssue(t, rotating, binding, now.Add(time.Minute))
	if _, err := rotating.VerifyAt(currentTicket, binding, now); err != nil {
		t.Fatalf("current ticket during rotation error = %v", err)
	}

	afterRotation := mustKeyring(t, current)
	if _, err := afterRotation.VerifyAt(currentTicket, binding, now); err != nil {
		t.Fatalf("current ticket after rotation error = %v", err)
	}
	if _, err := afterRotation.VerifyAt(oldTicket, binding, now); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("old ticket after rotation error = %v, want ErrUnknownKey", err)
	}
}

func TestInvalidBindingsRejected(t *testing.T) {
	ring := mustKeyring(t, testKey(1))
	expiresAt := time.Now().Add(time.Minute)

	tests := []struct {
		name    string
		binding Binding
	}{
		{name: "empty principal", binding: Binding{Endpoint: "grpc://flight:50051"}},
		{name: "empty endpoint", binding: Binding{Principal: "alice"}},
		{
			name: "unknown descriptor type",
			binding: Binding{
				Principal:  "alice",
				Descriptor: Descriptor{Type: 3},
				Endpoint:   "grpc://flight:50051",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ring.Issue(tt.binding, expiresAt)
			if !errors.Is(err, ErrInvalidBinding) {
				t.Fatalf("Issue() error = %v, want ErrInvalidBinding", err)
			}
		})
	}
}

func TestVerifyRequestRejectsEmptyContext(t *testing.T) {
	ring := mustKeyring(t, testKey(1))
	now := time.Now()
	binding := testBinding()
	ticket := mustIssue(t, ring, binding, now.Add(time.Minute))

	for _, tt := range []struct {
		name      string
		principal string
		endpoint  string
	}{
		{name: "empty principal", endpoint: binding.Endpoint},
		{name: "empty endpoint", principal: binding.Principal},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ring.VerifyRequestAt(ticket, tt.principal, tt.endpoint, now)
			if !errors.Is(err, ErrInvalidBinding) {
				t.Fatalf("VerifyRequestAt() error = %v, want ErrInvalidBinding", err)
			}
		})
	}
}

func TestInvalidKeyringConfigurationRejected(t *testing.T) {
	if _, err := NewKeyring(Key{ID: 1, Secret: []byte("short")}); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("NewKeyring() error = %v, want ErrInvalidKey", err)
	}
	if _, err := NewKeyring(testKey(1), testKey(1)); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("NewKeyring() duplicate error = %v, want ErrInvalidKey", err)
	}
}

func TestIssueCopiesKeyMaterial(t *testing.T) {
	key := testKey(1)
	ring := mustKeyring(t, key)
	key.Secret[0] ^= 0xff

	now := time.Now()
	_, err := ring.VerifyAt(
		mustIssue(t, ring, testBinding(), now.Add(time.Minute)),
		testBinding(),
		now,
	)
	if err != nil {
		t.Fatalf("VerifyAt() after caller mutation error = %v", err)
	}
}

func binaryBigEndianPutUint32(dst []byte, value uint32) {
	dst[0] = byte(value >> 24)
	dst[1] = byte(value >> 16)
	dst[2] = byte(value >> 8)
	dst[3] = byte(value)
}
