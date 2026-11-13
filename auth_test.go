package netsec

import (
	"bytes"
	"errors"
	"sync"
	"testing"
)

func TestIdentityChallengeBindsAddressNonceAndConnection(t *testing.T) {
	device := newTestDevice("device-a", 1)
	publisher := &identityCapture{}
	resolver, err := NewResolver(Options{
		AES:             testAES,
		VerifySignature: testSignatureVerifier,
		PublishIdentity: publisher.publish,
	})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if _, err := resolver.AddDevice(device.record()); err != nil {
		t.Fatalf("AddDevice: %v", err)
	}
	prand := [3]byte{0x40, 0x78, 0x89}
	address := addressFor(t, testAES, device.irk, prand)
	resolution, err := resolver.Resolve(address)
	if err != nil || !resolution.IsUnique() {
		t.Fatalf("Resolve = %+v, %v", resolution, err)
	}

	nonce := bytes.Repeat([]byte{0xa5}, 32)
	const connectionID = uint64(7)
	validSignature := signChallenge(t, device.privateKey, address, connectionID, nonce)

	wrongConnectionSignature := signChallenge(t, device.privateKey, address, connectionID+1, nonce)
	if _, err := resolver.Complete(resolution.Ticket, Challenge{
		Nonce:        nonce,
		ConnectionID: connectionID,
	}, IdentityProof{Signature: wrongConnectionSignature}); !errors.Is(err, ErrIdentityProof) {
		t.Fatalf("wrong connection error = %v, want ErrIdentityProof", err)
	}

	otherNonce := bytes.Repeat([]byte{0xb6}, 32)
	wrongNonceSignature := signChallenge(t, device.privateKey, address, connectionID, otherNonce)
	if _, err := resolver.Complete(resolution.Ticket, Challenge{
		Nonce:        nonce,
		ConnectionID: connectionID,
	}, IdentityProof{Signature: wrongNonceSignature}); !errors.Is(err, ErrIdentityProof) {
		t.Fatalf("wrong nonce error = %v, want ErrIdentityProof", err)
	}

	otherAddress := addressFor(t, testAES, device.irk, [3]byte{0x40, 0x79, 0x8a})
	wrongAddressSignature := signChallenge(t, device.privateKey, otherAddress, connectionID, nonce)
	if _, err := resolver.Complete(resolution.Ticket, Challenge{
		Nonce:        nonce,
		ConnectionID: connectionID,
	}, IdentityProof{Signature: wrongAddressSignature}); !errors.Is(err, ErrIdentityProof) {
		t.Fatalf("wrong address error = %v, want ErrIdentityProof", err)
	}

	corrupted := append([]byte(nil), validSignature...)
	corrupted[0] ^= 0xff
	if _, err := resolver.Complete(resolution.Ticket, Challenge{
		Nonce:        nonce,
		ConnectionID: connectionID,
	}, IdentityProof{Signature: corrupted}); !errors.Is(err, ErrIdentityProof) {
		t.Fatalf("corrupted signature error = %v, want ErrIdentityProof", err)
	}

	if publisher.count() != 0 {
		t.Fatalf("published identities after failed proofs = %d, want 0", publisher.count())
	}
	identity, err := resolver.Complete(resolution.Ticket, Challenge{
		Nonce:        nonce,
		ConnectionID: connectionID,
	}, IdentityProof{Signature: validSignature})
	if err != nil {
		t.Fatalf("Complete valid proof: %v", err)
	}
	if identity.DeviceID != device.id || identity.ConnectionID != connectionID || !identity.HasFullPrivilege() {
		t.Fatalf("identity = %+v", identity)
	}
	if publisher.count() != 1 {
		t.Fatalf("published identities = %d, want 1", publisher.count())
	}
}

func TestMatchingAddressIsNotAuthentication(t *testing.T) {
	device := newTestDevice("device-a", 1)
	attacker := newTestDevice("attacker", 1)
	publisher := &identityCapture{}
	resolver, err := NewResolver(Options{
		AES:             testAES,
		VerifySignature: testSignatureVerifier,
		PublishIdentity: publisher.publish,
	})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if _, err := resolver.AddDevice(device.record()); err != nil {
		t.Fatalf("AddDevice: %v", err)
	}
	address := addressFor(t, testAES, device.irk, [3]byte{0x40, 0x9a, 0xab})
	resolution, err := resolver.Resolve(address)
	if err != nil || !resolution.IsUnique() {
		t.Fatalf("Resolve = %+v, %v", resolution, err)
	}

	nonce := bytes.Repeat([]byte{0xc7}, 32)
	attackerSignature := signChallenge(t, attacker.privateKey, address, 11, nonce)
	if _, err := resolver.Complete(resolution.Ticket, Challenge{
		Nonce:        nonce,
		ConnectionID: 11,
	}, IdentityProof{Signature: attackerSignature}); !errors.Is(err, ErrIdentityProof) {
		t.Fatalf("attacker proof error = %v, want ErrIdentityProof", err)
	}
	if publisher.count() != 0 {
		t.Fatalf("identity was published after address-only match: %d", publisher.count())
	}
}

func TestTwoConcurrentConnections(t *testing.T) {
	device := newTestDevice("device-a", 2)
	publisher := &identityCapture{}
	resolver, err := NewResolver(Options{
		AES:             testAES,
		VerifySignature: testSignatureVerifier,
		PublishIdentity: publisher.publish,
	})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if _, err := resolver.AddDevice(device.record()); err != nil {
		t.Fatalf("AddDevice: %v", err)
	}
	address := addressFor(t, testAES, device.irk, [3]byte{0x40, 0xbc, 0xcd})
	resolution, err := resolver.Resolve(address)
	if err != nil || !resolution.IsUnique() {
		t.Fatalf("Resolve = %+v, %v", resolution, err)
	}

	challenges := []Challenge{
		{Nonce: bytes.Repeat([]byte{0x01}, 32), ConnectionID: 101},
		{Nonce: bytes.Repeat([]byte{0x02}, 32), ConnectionID: 202},
	}
	signatures := [][]byte{
		signChallenge(t, device.privateKey, address, challenges[0].ConnectionID, challenges[0].Nonce),
		signChallenge(t, device.privateKey, address, challenges[1].ConnectionID, challenges[1].Nonce),
	}

	type result struct {
		index    int
		identity *Identity
		err      error
	}
	results := make(chan result, len(challenges))
	var wait sync.WaitGroup
	for index := range challenges {
		index := index
		wait.Add(1)
		go func() {
			defer wait.Done()
			identity, err := resolver.Complete(resolution.Ticket, challenges[index], IdentityProof{
				Signature: signatures[index],
			})
			results <- result{index: index, identity: identity, err: err}
		}()
	}
	wait.Wait()
	close(results)

	for completed := range results {
		if completed.err != nil {
			t.Fatalf("connection %d: %v", completed.index, completed.err)
		}
		if completed.identity == nil {
			t.Fatalf("connection %d: nil identity", completed.index)
		}
		wantConnectionID := challenges[completed.index].ConnectionID
		if completed.identity.ConnectionID != wantConnectionID {
			t.Fatalf(
				"connection %d identity connection = %d, want %d",
				completed.index,
				completed.identity.ConnectionID,
				wantConnectionID,
			)
		}
		if completed.identity.DeviceID != device.id || !completed.identity.HasFullPrivilege() {
			t.Fatalf("connection %d identity = %+v", completed.index, completed.identity)
		}
	}

	identities := publisher.snapshot()
	if len(identities) != 2 {
		t.Fatalf("published identities = %d, want 2", len(identities))
	}
	seen := map[uint64]bool{}
	for _, identity := range identities {
		seen[identity.ConnectionID] = true
	}
	if !seen[101] || !seen[202] {
		t.Fatalf("published connection IDs = %v, want 101 and 202", seen)
	}
}

func TestCompleteFailsClosedWithoutVerifierOrLongTermKey(t *testing.T) {
	device := newTestDevice("device-a", 1)

	withoutVerifier, err := NewResolver(Options{AES: testAES})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if _, err := withoutVerifier.AddDevice(device.record()); err != nil {
		t.Fatalf("AddDevice: %v", err)
	}
	address := addressFor(t, testAES, device.irk, [3]byte{0x40, 0xde, 0xef})
	resolution, err := withoutVerifier.Resolve(address)
	if err != nil || !resolution.IsUnique() {
		t.Fatalf("Resolve = %+v, %v", resolution, err)
	}
	nonce := bytes.Repeat([]byte{0x44}, 32)
	if _, err := withoutVerifier.Complete(resolution.Ticket, Challenge{
		Nonce:        nonce,
		ConnectionID: 1,
	}, IdentityProof{Signature: []byte{1}}); !errors.Is(err, ErrSignatureVerifier) {
		t.Fatalf("missing verifier error = %v, want ErrSignatureVerifier", err)
	}

	withoutKey, err := NewResolver(Options{
		AES:             testAES,
		VerifySignature: testSignatureVerifier,
	})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	record := device.record()
	record.IdentityPublicKey = nil
	if _, err := withoutKey.AddDevice(record); err != nil {
		t.Fatalf("AddDevice: %v", err)
	}
	resolution, err = withoutKey.Resolve(address)
	if err != nil || !resolution.IsUnique() {
		t.Fatalf("Resolve = %+v, %v", resolution, err)
	}
	if _, err := withoutKey.Complete(resolution.Ticket, Challenge{
		Nonce:        nonce,
		ConnectionID: 1,
	}, IdentityProof{Signature: []byte{1}}); !errors.Is(err, ErrIdentityPublicKey) {
		t.Fatalf("missing public key error = %v, want ErrIdentityPublicKey", err)
	}
}
