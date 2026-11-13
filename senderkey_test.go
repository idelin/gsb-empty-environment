package netsec

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"math"
	"sync"
	"testing"
)

type testEnv struct {
	groupID []byte

	adminPub  ed25519.PublicKey
	adminPriv ed25519.PrivateKey

	devices []DeviceID

	states map[DeviceID]*GroupState
	stores map[DeviceID]*MemoryKeyStore
}

func newTestEnv(t *testing.T, memberCount int) *testEnv {
	t.Helper()

	adminPub, adminPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate admin key: %v", err)
	}

	env := &testEnv{
		groupID:   []byte("group-42"),
		adminPub:  adminPub,
		adminPriv: adminPriv,
		states:    make(map[DeviceID]*GroupState),
		stores:    make(map[DeviceID]*MemoryKeyStore),
	}

	for i := 0; i < memberCount; i++ {
		pub, _, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatalf("generate device key: %v", err)
		}
		env.devices = append(env.devices, DeviceFromPublic(pub))
	}

	members := NewMemberSet(env.devices...)
	for _, device := range env.devices {
		store := NewMemoryKeyStore()
		env.stores[device] = store
		env.states[device] = NewGroupState(env.groupID, adminPub, members, store)
	}
	return env
}

func makeDistribution(t *testing.T, env *testEnv, sender DeviceID, epoch uint64, digest Digest) (*Distribution, []byte, Handle) {
	t.Helper()

	var chainID ChainID
	if _, err := rand.Read(chainID[:]); err != nil {
		t.Fatalf("random chain id: %v", err)
	}
	initialKey := make([]byte, 32)
	if _, err := rand.Read(initialKey); err != nil {
		t.Fatalf("random chain key: %v", err)
	}
	ciphertext := make([]byte, 48)
	if _, err := rand.Read(ciphertext); err != nil {
		t.Fatalf("random ciphertext: %v", err)
	}

	dist := &Distribution{
		GroupID:            append([]byte(nil), env.groupID...),
		Epoch:              epoch,
		MembersDigest:      digest,
		SenderDevice:       sender,
		ChainID:            chainID,
		ChainKeyCiphertext: ciphertext,
		ChainKeyDigest:     ChainKeyCipherDigest(ciphertext),
		StartIndex:         0,
	}
	SignDistribution(dist, env.adminPriv)

	handle := Handle("chain-" + string(chainID[:]))
	return dist, initialKey, handle
}

func installForAll(t *testing.T, env *testEnv, sender DeviceID) (*Distribution, []byte) {
	t.Helper()

	var dist *Distribution
	var initialKey []byte
	var handle Handle
	first := env.states[sender]
	dist, initialKey, handle = makeDistribution(t, env, sender, first.Epoch(), first.MembersDigest())

	for _, device := range env.devices {
		if err := env.states[device].ApplyDistribution(dist, initialKey, handle); err != nil {
			t.Fatalf("install distribution on %x: %v", device[:4], err)
		}
	}
	return dist, initialKey
}

func sealMsg(t *testing.T, env *testEnv, sender DeviceID, plaintext []byte) (Header, []byte, []byte) {
	t.Helper()

	header, _, nonce, ciphertext, err := env.states[sender].Seal(sender, plaintext)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	return header, nonce, ciphertext
}

type recordedCall struct {
	sender    DeviceID
	header    Header
	plaintext []byte
}

func openMsg(t *testing.T, env *testEnv, receiver DeviceID, header Header, nonce, ciphertext []byte) recordedCall {
	t.Helper()

	var call recordedCall
	callback := func(sender DeviceID, h Header, plaintext []byte) error {
		call = recordedCall{
			sender:    sender,
			header:    h,
			plaintext: append([]byte(nil), plaintext...),
		}
		return nil
	}
	if err := env.states[receiver].Open(header, nonce, ciphertext, callback); err != nil {
		t.Fatalf("open: %v", err)
	}
	return call
}

func wantErr(t *testing.T, got error, want error, context string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s: expected %v, got %v", context, want, got)
	}
}

func TestBasicRoundTripAndRatchet(t *testing.T) {
	env := newTestEnv(t, 3)
	installForAll(t, env, env.devices[0])

	receiver := env.devices[1]
	for i := uint64(0); i < 5; i++ {
		plaintext := []byte{'h', 'e', 'l', 'l', 'o', byte(i)}
		header, nonce, ciphertext := sealMsg(t, env, env.devices[0], plaintext)
		if header.Index != i {
			t.Fatalf("header index = %d, want %d", header.Index, i)
		}
		call := openMsg(t, env, receiver, header, nonce, ciphertext)
		if !bytes.Equal(call.plaintext, plaintext) {
			t.Fatalf("plaintext mismatch at index %d", i)
		}
		if call.sender != env.devices[0] {
			t.Fatal("callback sender not bound to header sender")
		}
	}

	_, _, nextIndex, ok := env.states[receiver].ChainStatus(env.devices[0])
	if !ok || nextIndex != 5 {
		t.Fatalf("receiver nextIndex = %d (ok=%v), want 5", nextIndex, ok)
	}
	_, _, senderNext, _ := env.states[env.devices[0]].ChainStatus(env.devices[0])
	if senderNext != 5 {
		t.Fatalf("sender nextIndex = %d, want 5", senderNext)
	}
}

func TestOutOfOrderDeliveryFillsSkippedCache(t *testing.T) {
	env := newTestEnv(t, 2)
	installForAll(t, env, env.devices[0])

	headers := make([]Header, 5)
	nonces := make([][]byte, 5)
	ciphertexts := make([][]byte, 5)
	for i := range headers {
		headers[i], nonces[i], ciphertexts[i] = sealMsg(t, env, env.devices[0], []byte{byte(i)})
	}

	receiver := env.devices[1]
	state := env.states[receiver]

	// Deliver index 4 first; indices 0..3 become cached skipped keys.
	openMsg(t, env, receiver, headers[4], nonces[4], ciphertexts[4])
	if len(state.skipped) != 4 {
		t.Fatalf("skipped cache size = %d, want 4", len(state.skipped))
	}

	for _, idx := range []int{2, 0, 3, 1} {
		openMsg(t, env, receiver, headers[idx], nonces[idx], ciphertexts[idx])
	}
	if len(state.skipped) != 0 {
		t.Fatalf("skipped cache not drained: %d entries remain", len(state.skipped))
	}
}

func TestDuplicateInOrderMessageRejected(t *testing.T) {
	env := newTestEnv(t, 2)
	installForAll(t, env, env.devices[0])

	header, nonce, ciphertext := sealMsg(t, env, env.devices[0], []byte("once"))
	receiver := env.devices[1]

	openMsg(t, env, receiver, header, nonce, ciphertext)

	err := env.states[receiver].Open(header, nonce, ciphertext, func(DeviceID, Header, []byte) error {
		t.Fatal("callback must not run for a replayed message")
		return nil
	})
	wantErr(t, err, ErrDuplicate, "in-order replay")
}

func TestDuplicateSkippedMessageRejected(t *testing.T) {
	env := newTestEnv(t, 2)
	installForAll(t, env, env.devices[0])

	headers := make([]Header, 3)
	nonces := make([][]byte, 3)
	ciphertexts := make([][]byte, 3)
	for i := range headers {
		headers[i], nonces[i], ciphertexts[i] = sealMsg(t, env, env.devices[0], []byte{byte(i)})
	}

	receiver := env.devices[1]
	// Index 2 first caches 0 and 1.
	openMsg(t, env, receiver, headers[2], nonces[2], ciphertexts[2])
	// Index 0 consumes one cached key.
	openMsg(t, env, receiver, headers[0], nonces[0], ciphertexts[0])

	err := env.states[receiver].Open(headers[0], nonces[0], ciphertexts[0], func(DeviceID, Header, []byte) error {
		t.Fatal("callback must not run for a replayed skipped message")
		return nil
	})
	wantErr(t, err, ErrDuplicate, "cached-key replay")
}

func TestRemovedDeviceOldChainRejected(t *testing.T) {
	env := newTestEnv(t, 3)
	installForAll(t, env, env.devices[0])
	installForAll(t, env, env.devices[1])

	removed := env.devices[1]
	oldHeader, oldNonce, oldCiphertext := sealMsg(t, env, removed, []byte("before removal"))

	survivors := NewMemberSet(env.devices[0], env.devices[2])
	for _, device := range env.devices {
		if device == removed {
			continue
		}
		if _, _, err := env.states[device].AdvanceEpoch(survivors); err != nil {
			t.Fatalf("advance epoch: %v", err)
		}
	}

	receiver := env.devices[0]
	err := env.states[receiver].Open(oldHeader, oldNonce, oldCiphertext, func(DeviceID, Header, []byte) error {
		t.Fatal("removed device must not reach the success callback on an old chain")
		return nil
	})
	wantErr(t, err, ErrOldEpoch, "old-epoch message after removal")

	oldHandle := Handle("chain-" + string(oldHeader.ChainID[:]))
	if _, err := env.stores[receiver].Get(oldHandle); err != ErrKeyNotFound {
		t.Fatalf("old chain key not deleted, got err %v", err)
	}

	digest := env.states[receiver].MembersDigest()
	dist, initialKey, handle := makeDistribution(t, env, removed, env.states[receiver].Epoch(), digest)
	wantErr(t, env.states[receiver].ApplyDistribution(dist, initialKey, handle), ErrNotMember, "distribution for removed device")
}

func TestAdminForkedMemberDigestRejected(t *testing.T) {
	env := newTestEnv(t, 3)
	installForAll(t, env, env.devices[0])

	receiver := env.states[env.devices[1]]
	sender := env.devices[0]

	forkedDigest := NewMemberSet(env.devices[0], env.devices[2]).Digest()
	dist, initialKey, handle := makeDistribution(t, env, sender, receiver.Epoch(), receiver.MembersDigest())
	dist.MembersDigest = forkedDigest
	SignDistribution(dist, env.adminPriv)

	wantErr(t, receiver.ApplyDistribution(dist, initialKey, handle), ErrDigestMismatch, "forked member digest")
}

func TestInvalidAdministratorSignatureRejected(t *testing.T) {
	env := newTestEnv(t, 2)
	installForAll(t, env, env.devices[0])

	receiver := env.states[env.devices[1]]
	dist, initialKey, handle := makeDistribution(t, env, env.devices[0], receiver.Epoch(), receiver.MembersDigest())
	dist.Signature[0] ^= 0xFF

	wantErr(t, receiver.ApplyDistribution(dist, initialKey, handle), ErrSignature, "tampered signature")
}

func TestChainIDReuseRejectedAcrossEpochs(t *testing.T) {
	env := newTestEnv(t, 2)
	original, initialKey := installForAll(t, env, env.devices[0])

	receiver := env.states[env.devices[1]]

	sameEpoch := *original
	sameEpoch.ChainKeyCiphertext = append([]byte(nil), original.ChainKeyCiphertext...)
	sameEpoch.ChainKeyDigest = ChainKeyCipherDigest(sameEpoch.ChainKeyCiphertext)
	SignDistribution(&sameEpoch, env.adminPriv)
	wantErr(t, receiver.ApplyDistribution(&sameEpoch, initialKey, Handle("dup-handle-1")), ErrChainReused, "same-epoch chain reuse")

	members := receiver.Members()
	if _, _, err := receiver.AdvanceEpoch(members); err != nil {
		t.Fatalf("advance: %v", err)
	}
	crossEpoch := &Distribution{
		GroupID:            append([]byte(nil), env.groupID...),
		Epoch:              receiver.Epoch(),
		MembersDigest:      receiver.MembersDigest(),
		SenderDevice:       env.devices[0],
		ChainID:            original.ChainID,
		ChainKeyCiphertext: append([]byte(nil), original.ChainKeyCiphertext...),
		ChainKeyDigest:     original.ChainKeyDigest,
		StartIndex:         0,
	}
	SignDistribution(crossEpoch, env.adminPriv)
	wantErr(t, receiver.ApplyDistribution(crossEpoch, initialKey, Handle("dup-handle-2")), ErrChainReused, "cross-epoch chain reuse")
}

func TestSkippedKeyLimitEnforced(t *testing.T) {
	env := newTestEnv(t, 2)
	installForAll(t, env, env.devices[0])

	sender := env.devices[0]
	receiver := env.states[env.devices[1]]

	// Produce MaxSkippedKeys+2 messages without opening any, then jump
	// straight to the last one: filling the gap would exceed the cache.
	var header Header
	var nonce, ciphertext []byte
	for i := 0; i < MaxSkippedKeys+2; i++ {
		header, nonce, ciphertext = sealMsg(t, env, sender, []byte{byte(i)})
	}

	err := receiver.Open(header, nonce, ciphertext, func(DeviceID, Header, []byte) error {
		t.Fatal("callback must not run when the skipped cache would overflow")
		return nil
	})
	wantErr(t, err, ErrSkippedLimit, "skipped cache overflow")

	if len(receiver.skipped) != 0 {
		t.Fatalf("failed open polluted skipped cache: %d entries", len(receiver.skipped))
	}

	// Exactly MaxSkippedKeys gap entries is allowed: open the message whose
	// gap is MaxSkippedKeys, i.e. index MaxSkippedKeys with nextIndex 0.
	env2 := newTestEnv(t, 2)
	installForAll(t, env2, env2.devices[0])
	var atLimit Header
	var atNonce, atCipher []byte
	for i := 0; i <= MaxSkippedKeys; i++ {
		atLimit, atNonce, atCipher = sealMsg(t, env2, env2.devices[0], []byte{byte(i)})
	}
	openMsg(t, env2, env2.devices[1], atLimit, atNonce, atCipher)
	if len(env2.states[env2.devices[1]].skipped) != MaxSkippedKeys {
		t.Fatalf("expected exactly %d cached keys", MaxSkippedKeys)
	}
}

func TestHugeIndexOverflowRejected(t *testing.T) {
	env := newTestEnv(t, 2)
	dist, initialKey, handle := makeDistribution(t, env, env.devices[0], 0, env.states[env.devices[0]].MembersDigest())
	dist.StartIndex = math.MaxUint64
	SignDistribution(dist, env.adminPriv)

	receiver := env.states[env.devices[1]]
	wantErr(t, receiver.ApplyDistribution(dist, initialKey, handle), ErrIndexOverflow, "distribution starting at MaxUint64")

	// A message claiming MaxUint64 against a healthy chain is rejected too.
	env2 := newTestEnv(t, 2)
	installForAll(t, env2, env2.devices[0])
	header, nonce, ciphertext := sealMsg(t, env2, env2.devices[0], []byte("x"))
	header.Index = math.MaxUint64
	err := env2.states[env2.devices[1]].Open(header, nonce, ciphertext, func(DeviceID, Header, []byte) error {
		t.Fatal("callback must not run for an overflow index")
		return nil
	})
	wantErr(t, err, ErrIndexOverflow, "message index MaxUint64")

	// Sealing a chain whose next index reaches MaxUint64 must error.
	env3 := newTestEnv(t, 1)
	overflowDist, overflowKey, overflowHandle := makeDistribution(t, env3, env3.devices[0], 0, env3.states[env3.devices[0]].MembersDigest())
	overflowDist.StartIndex = math.MaxUint64 - 1
	SignDistribution(overflowDist, env3.adminPriv)
	if err := env3.states[env3.devices[0]].ApplyDistribution(overflowDist, overflowKey, overflowHandle); err != nil {
		t.Fatalf("install near-overflow chain: %v", err)
	}
	if _, _, _, _, err := env3.states[env3.devices[0]].Seal(env3.devices[0], []byte("ok")); err != nil {
		t.Fatalf("seal at MaxUint64-1: %v", err)
	}
	var overflowErr error
	_, _, _, _, overflowErr = env3.states[env3.devices[0]].Seal(env3.devices[0], []byte("overflow"))
	wantErr(t, overflowErr, ErrIndexOverflow, "seal at MaxUint64")
}

func TestAuthenticationFailureDoesNotAdvance(t *testing.T) {
	env := newTestEnv(t, 2)
	installForAll(t, env, env.devices[0])

	receiver := env.states[env.devices[1]]
	header, nonce, ciphertext := sealMsg(t, env, env.devices[0], []byte("authentic"))

	_, _, beforeIndex, _ := receiver.ChainStatus(env.devices[0])

	tampered := append([]byte(nil), ciphertext...)
	tampered[len(tampered)-1] ^= 0x01
	err := receiver.Open(header, nonce, tampered, func(DeviceID, Header, []byte) error {
		t.Fatal("callback must not run when authentication fails")
		return nil
	})
	wantErr(t, err, ErrAuth, "tampered ciphertext")

	_, _, afterIndex, _ := receiver.ChainStatus(env.devices[0])
	if afterIndex != beforeIndex {
		t.Fatalf("chain advanced after auth failure: %d -> %d", beforeIndex, afterIndex)
	}
	if len(receiver.skipped) != 0 {
		t.Fatalf("auth failure left skipped keys: %d", len(receiver.skipped))
	}

	// The unmodified message still opens, proving no state was consumed.
	call := openMsg(t, env, env.devices[1], header, nonce, ciphertext)
	if string(call.plaintext) != "authentic" {
		t.Fatalf("original message no longer readable after failed attempt")
	}
}

func TestHeaderAndDigestTamperingRejected(t *testing.T) {
	env := newTestEnv(t, 2)
	installForAll(t, env, env.devices[0])

	receiver := env.states[env.devices[1]]
	header, nonce, ciphertext := sealMsg(t, env, env.devices[0], []byte("bound"))

	tamperedHeader := header
	tamperedHeader.Index = header.Index + 5
	wantErr(t, receiver.Open(tamperedHeader, nonce, ciphertext, func(DeviceID, Header, []byte) error {
		t.Fatal("callback must not run for a tampered header")
		return nil
	}), ErrAuth, "tampered header index")

	// Re-sealing with a different member digest must also fail AAD checks.
	otherDigest := NewMemberSet(env.devices[1]).Digest()
	header2, msgKey, nonce2, realCipher := func() (Header, []byte, []byte, []byte) {
		h, mk, n, c, err := env.states[env.devices[0]].Seal(env.devices[0], []byte("bound2"))
		if err != nil {
			t.Fatalf("seal: %v", err)
		}
		return h, mk, n, c
	}()
	badAAD := EncodeAAD(header2, otherDigest)
	badNonce, badCipher, err := Seal(msgKey, []byte("bound2"), badAAD)
	if err != nil {
		t.Fatalf("seal with bad aad: %v", err)
	}
	wantErr(t, receiver.Open(header2, badNonce, badCipher, func(DeviceID, Header, []byte) error {
		t.Fatal("callback must not run when member digest is rebound")
		return nil
	}), ErrAuth, "rebound member digest")

	// nonce2/c from the real seal still opens with the correct AAD.
	openMsg(t, env, env.devices[1], header2, nonce2, realCipher)
}

func TestEpochAdvanceRequiresRedistributionToSend(t *testing.T) {
	env := newTestEnv(t, 2)
	installForAll(t, env, env.devices[0])

	receiver := env.states[env.devices[1]]
	members := receiver.Members()

	oldHeader, oldNonce, oldCipher := sealMsg(t, env, env.devices[0], []byte("epoch0"))

	newEpoch, newDigest, err := receiver.AdvanceEpoch(members)
	if err != nil {
		t.Fatalf("advance: %v", err)
	}
	if newEpoch != 1 {
		t.Fatalf("new epoch = %d, want 1", newEpoch)
	}
	if newDigest != receiver.MembersDigest() {
		t.Fatal("returned digest does not match stored digest")
	}

	// Old-epoch data messages are rejected even with unchanged membership.
	wantErr(t, receiver.Open(oldHeader, oldNonce, oldCipher, func(DeviceID, Header, []byte) error {
		t.Fatal("old epoch message must never reach callback")
		return nil
	}), ErrOldEpoch, "old epoch after unchanged-member advance")

	// A surviving member cannot seal on the receiver until redistributed.
	_, _, _, ok := receiver.ChainStatus(env.devices[0])
	if ok {
		t.Fatal("old chain survived epoch advance")
	}

	// Old-epoch distribution must be refused.
	oldDist, oldKey, oldHandle := makeDistribution(t, env, env.devices[0], 0, newDigest)
	wantErr(t, receiver.ApplyDistribution(oldDist, oldKey, oldHandle), ErrEpoch, "stale distribution")

	// Fresh, current-epoch distribution restores the ability to send.
	newDist, newKey, newHandle := makeDistribution(t, env, env.devices[0], newEpoch, newDigest)
	if err := receiver.ApplyDistribution(newDist, newKey, newHandle); err != nil {
		t.Fatalf("install new-epoch distribution: %v", err)
	}

	// Install on the sender side too so it can seal, then round-trip.
	senderState := env.states[env.devices[0]]
	if _, _, err := senderState.AdvanceEpoch(members); err != nil {
		t.Fatalf("advance sender: %v", err)
	}
	if err := senderState.ApplyDistribution(newDist, newKey, newHandle); err != nil {
		t.Fatalf("install on sender: %v", err)
	}

	header, nonce, ciphertext := sealMsg(t, env, env.devices[0], []byte("epoch1"))
	if header.Epoch != 1 {
		t.Fatalf("new message epoch = %d, want 1", header.Epoch)
	}
	call := openMsg(t, env, env.devices[1], header, nonce, ciphertext)
	if string(call.plaintext) != "epoch1" {
		t.Fatalf("new epoch round trip failed: %q", call.plaintext)
	}
}

func TestDistributionRulesEnforced(t *testing.T) {
	env := newTestEnv(t, 3)
	installForAll(t, env, env.devices[0])

	receiver := env.states[env.devices[1]]
	digest := receiver.MembersDigest()

	// Non-member sender.
	notMember := func() DeviceID {
		pub, _, _ := ed25519.GenerateKey(rand.Reader)
		return DeviceFromPublic(pub)
	}()
	dist, key, handle := makeDistribution(t, env, notMember, receiver.Epoch(), digest)
	wantErr(t, receiver.ApplyDistribution(dist, key, handle), ErrNotMember, "non-member distribution")

	// Foreign group id.
	foreign, fkey, fhandle := makeDistribution(t, env, env.devices[0], receiver.Epoch(), digest)
	foreign.GroupID = []byte("other-group")
	SignDistribution(foreign, env.adminPriv)
	wantErr(t, receiver.ApplyDistribution(foreign, fkey, fhandle), ErrGroupID, "foreign group distribution")

	// Ciphertext digest inconsistency.
	badDigestDist, bkey, bhandle := makeDistribution(t, env, env.devices[0], receiver.Epoch(), digest)
	badDigestDist.ChainKeyCiphertext = append(badDigestDist.ChainKeyCiphertext, 0x01)
	SignDistribution(badDigestDist, env.adminPriv)
	wantErr(t, receiver.ApplyDistribution(badDigestDist, bkey, bhandle), ErrDigestMismatch, "ciphertext digest mismatch")

	// Malformed initial chain key length.
	okDist, _, okHandle := makeDistribution(t, env, env.devices[0], receiver.Epoch(), digest)
	wantErr(t, receiver.ApplyDistribution(okDist, make([]byte, 16), okHandle), ErrInvalidChainKey, "short chain key")

	// Opening a message from a member without any installed chain.
	env2 := newTestEnv(t, 2)
	header := Header{
		GroupID:      env2.groupID,
		Epoch:        0,
		SenderDevice: env2.devices[0],
		ChainID:      ChainID{1, 2, 3},
		Index:        0,
	}
	wantErr(t, env2.states[env2.devices[1]].Open(header, make([]byte, 12), make([]byte, 16), func(DeviceID, Header, []byte) error {
		t.Fatal("callback must not run without an installed chain")
		return nil
	}), ErrChainNotFound, "open without chain")
}

func TestSuccessfulCallbackSenderIsSignedMember(t *testing.T) {
	env := newTestEnv(t, 3)
	installForAll(t, env, env.devices[0])

	// Confirm every success callback sees a sender recorded in the signed
	// member set of that message's epoch.
	for i := 0; i < 3; i++ {
		header, nonce, ciphertext := sealMsg(t, env, env.devices[0], []byte{byte(i)})
		err := env.states[env.devices[2]].Open(header, nonce, ciphertext, func(sender DeviceID, h Header, _ []byte) error {
			info, ok := env.states[env.devices[2]].history[h.Epoch]
			if !ok || !info.members.Contains(sender) {
				t.Fatalf("callback sender %x not in signed member set of epoch %d", sender[:4], h.Epoch)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
	}

	// After removing device 0 and redistributing to device 2, device 0 can
	// never trigger a callback again on either epoch.
	remaining := NewMemberSet(env.devices[2])
	state2 := env.states[env.devices[2]]
	if _, _, err := state2.AdvanceEpoch(remaining); err != nil {
		t.Fatalf("advance: %v", err)
	}
	header, nonce, ciphertext := sealMsg(t, env, env.devices[0], []byte("ghost"))
	err := state2.Open(header, nonce, ciphertext, func(DeviceID, Header, []byte) error {
		t.Fatal("removed sender reached callback")
		return nil
	})
	wantErr(t, err, ErrOldEpoch, "removed sender after epoch change")
}

func TestConcurrentEpochUpdatesAndMessaging(t *testing.T) {
	env := newTestEnv(t, 3)
	installForAll(t, env, env.devices[0])

	receiver := env.states[env.devices[1]]
	sender := env.states[env.devices[0]]

	const goroutines = 16
	var wg sync.WaitGroup

	// Concurrent epoch advances on the receiver.
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, _ = receiver.AdvanceEpoch(receiver.Members())
		}()
	}

	// Concurrent seal/open traffic against independently advancing state.
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _, _, _, err := sender.Seal(env.devices[0], []byte{byte(i)})
			_ = err
			_ = sender.Epoch()
			_ = receiver.Members()
		}(i)
	}

	wg.Wait()

	if receiver.Epoch() != uint64(goroutines) {
		t.Fatalf("epoch = %d, want exactly %d strictly-incremented epochs", receiver.Epoch(), goroutines)
	}
	if len(receiver.chains) != 0 {
		t.Fatal("chains were not revoked during concurrent advances")
	}

	// History retained every epoch and epochs remain contiguous.
	for epoch := uint64(0); epoch <= goroutines; epoch++ {
		if _, ok := receiver.history[epoch]; !ok {
			t.Fatalf("missing member-set history for epoch %d", epoch)
		}
	}

	// Bring the sender to the receiver's final epoch, then redistribute. The
	// state machine must remain fully usable after the concurrent storm.
	for sender.Epoch() < receiver.Epoch() {
		if _, _, err := sender.AdvanceEpoch(sender.Members()); err != nil {
			t.Fatalf("catch sender up: %v", err)
		}
	}
	dist, key, handle := makeDistribution(t, env, env.devices[0], receiver.Epoch(), receiver.MembersDigest())
	if err := receiver.ApplyDistribution(dist, key, handle); err != nil {
		t.Fatalf("post-storm receiver distribution: %v", err)
	}
	if err := sender.ApplyDistribution(dist, key, handle); err != nil {
		t.Fatalf("post-storm sender distribution: %v", err)
	}

	header, nonce, ciphertext := sealMsg(t, env, env.devices[0], []byte("after storm"))
	call := openMsg(t, env, env.devices[1], header, nonce, ciphertext)
	if string(call.plaintext) != "after storm" {
		t.Fatalf("post-storm round trip failed: %q", call.plaintext)
	}
}

func TestMultipleSendersIndependentChains(t *testing.T) {
	env := newTestEnv(t, 3)
	installForAll(t, env, env.devices[0])
	installForAll(t, env, env.devices[1])

	receiver := env.devices[2]

	h0, n0, c0 := sealMsg(t, env, env.devices[0], []byte("from-0"))
	h1, n1, c1 := sealMsg(t, env, env.devices[1], []byte("from-1"))

	// Interleaved, out-of-order processing must keep chains independent.
	call1 := openMsg(t, env, receiver, h1, n1, c1)
	if call1.sender != env.devices[1] || string(call1.plaintext) != "from-1" {
		t.Fatal("sender 1 chain corrupted")
	}
	call0 := openMsg(t, env, receiver, h0, n0, c0)
	if call0.sender != env.devices[0] || string(call0.plaintext) != "from-0" {
		t.Fatal("sender 0 chain corrupted")
	}

	chain0, _, idx0, ok0 := env.states[receiver].ChainStatus(env.devices[0])
	chain1, _, idx1, ok1 := env.states[receiver].ChainStatus(env.devices[1])
	if !ok0 || !ok1 || chain0 == chain1 || idx0 != 1 || idx1 != 1 {
		t.Fatalf("independent chain state violated: %d/%d ok=%v/%v", idx0, idx1, ok0, ok1)
	}
}

func TestForeignGroupAndFutureEpochMessagesRejected(t *testing.T) {
	env := newTestEnv(t, 2)
	installForAll(t, env, env.devices[0])

	receiver := env.states[env.devices[1]]
	header, nonce, ciphertext := sealMsg(t, env, env.devices[0], []byte("ok"))

	foreign := header
	foreign.GroupID = []byte("foreign-group")
	wantErr(t, receiver.Open(foreign, nonce, ciphertext, func(DeviceID, Header, []byte) error {
		t.Fatal("callback must not run for a foreign group message")
		return nil
	}), ErrGroupID, "foreign group data message")

	future := header
	future.Epoch = header.Epoch + 1
	wantErr(t, receiver.Open(future, nonce, ciphertext, func(DeviceID, Header, []byte) error {
		t.Fatal("callback must not run for a future epoch message")
		return nil
	}), ErrOldEpoch, "future epoch data message")
}

func TestWrongChainIDMessageRejected(t *testing.T) {
	env := newTestEnv(t, 2)
	installForAll(t, env, env.devices[0])

	receiver := env.states[env.devices[1]]
	header, nonce, ciphertext := sealMsg(t, env, env.devices[0], []byte("ok"))

	wrong := header
	wrong.ChainID = ChainID{0xDE, 0xAD, 0xBE, 0xEF}
	wantErr(t, receiver.Open(wrong, nonce, ciphertext, func(DeviceID, Header, []byte) error {
		t.Fatal("callback must not run for a mismatched chain id")
		return nil
	}), ErrChainMismatch, "wrong chain id")
}

func TestMemoryKeyStoreCopiesAndValidates(t *testing.T) {
	store := NewMemoryKeyStore()
	key := make([]byte, 32)
	key[0] = 0xAA

	if err := store.Put("h", key); err != nil {
		t.Fatalf("put: %v", err)
	}

	// Mutating the caller's buffer must not change stored material.
	key[0] = 0xBB
	got, err := store.Get("h")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got[0] != 0xAA {
		t.Fatal("stored key aliased caller memory")
	}

	// Mutating the returned buffer must not change stored material either.
	got[0] = 0xCC
	again, _ := store.Get("h")
	if again[0] != 0xAA {
		t.Fatal("stored key aliased returned memory")
	}

	if err := store.Put("bad", make([]byte, 16)); err != ErrInvalidChainKey {
		t.Fatalf("short key: got %v, want ErrInvalidChainKey", err)
	}
	if _, err := store.Get("missing"); err != ErrKeyNotFound {
		t.Fatalf("missing handle: got %v, want ErrKeyNotFound", err)
	}
	if err := store.Delete("missing"); err != nil {
		t.Fatalf("deleting a missing handle should succeed, got %v", err)
	}
	if err := store.Delete("h"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := store.Get("h"); err != ErrKeyNotFound {
		t.Fatalf("key survived deletion, got err %v", err)
	}
}

func TestMemberSetDeterministicDigestAndEquality(t *testing.T) {
	env := newTestEnv(t, 3)

	a := NewMemberSet(env.devices[0], env.devices[1], env.devices[2])
	b := NewMemberSet(env.devices[2], env.devices[0], env.devices[1])
	if a.Digest() != b.Digest() {
		t.Fatal("digest depends on insertion order")
	}
	if !a.Equal(b) {
		t.Fatal("equal sets reported unequal")
	}

	c := NewMemberSet(env.devices[0], env.devices[1])
	if a.Digest() == c.Digest() {
		t.Fatal("different member sets produced the same digest")
	}
	if a.Equal(c) {
		t.Fatal("unequal sets reported equal")
	}

	clone := a.Clone()
	delete(clone, env.devices[0])
	if a.Contains(env.devices[0]) != true {
		t.Fatal("clone mutation leaked into the original set")
	}
}

func TestStateAccessorsAndMalformedSignature(t *testing.T) {
	env := newTestEnv(t, 2)
	state := env.states[env.devices[0]]

	if string(state.GroupID()) != "group-42" {
		t.Fatalf("group id = %q", state.GroupID())
	}
	if state.Epoch() != 0 {
		t.Fatalf("initial epoch = %d, want 0", state.Epoch())
	}
	if len(state.Members()) != 2 {
		t.Fatalf("member count = %d, want 2", len(state.Members()))
	}
	if state.MembersDigest() != NewMemberSet(env.devices...).Digest() {
		t.Fatal("exposed member digest inconsistent")
	}

	dist, _, _ := makeDistribution(t, env, env.devices[0], state.Epoch(), state.MembersDigest())
	dist.Signature = dist.Signature[:4]
	if VerifyDistribution(dist, env.adminPub) {
		t.Fatal("truncated signature verified")
	}

	if ErrKeyNotFound.Error() == "" {
		t.Fatal("key-not-found error has empty message")
	}
}
