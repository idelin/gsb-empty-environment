package netsec

import (
	"bytes"
	"crypto/ed25519"
	"testing"
)

type fixedResponder struct {
	response Response
}

func (f fixedResponder) Respond([]byte) (Response, error) {
	return f.response.Clone(), nil
}

func xorResponses(t *testing.T, first, second Response) []byte {
	t.Helper()
	if len(first.Payload) != RowSize || len(second.Payload) != RowSize {
		t.Fatalf("payload lengths = %d, %d", len(first.Payload), len(second.Payload))
	}
	row := make([]byte, RowSize)
	for i := range row {
		row[i] = first.Payload[i] ^ second.Payload[i]
	}
	return row
}

func TestAttackerChosenRecordWithValidSignatureIsRejected(t *testing.T) {
	t.Parallel()

	env := newTestEnv(t, 19)
	const index = 9

	q0, q1, err := GenerateQuery(&patternReader{}, env.registry.N, index)
	if err != nil {
		t.Fatal(err)
	}
	honest0, err := env.first.Respond(q0)
	if err != nil {
		t.Fatal(err)
	}
	honest1, err := env.second.Respond(q1)
	if err != nil {
		t.Fatal(err)
	}

	chosenRecord := bytes.Repeat([]byte{0x42}, RecordSize)
	craftedPayload := append([]byte(nil), honest0.Payload...)
	for i := 0; i < RecordSize; i++ {
		craftedPayload[i] ^= env.records[index][i] ^ chosenRecord[i]
	}
	crafted := env.first.signResponse(q0, craftedPayload, env.registry.SnapshotID)
	if !VerifyResponseSignature(crafted, q0, env.registry.N) {
		t.Fatal("attacker response should have a valid Ed25519 signature")
	}

	row := xorResponses(t, crafted, honest1)
	if !bytes.Equal(row[:RecordSize], chosenRecord) {
		t.Fatal("fixture did not force the attacker-selected record")
	}
	if VerifyRowTag(env.key, index, row) {
		t.Fatal("HMAC accepted the attacker-selected record")
	}

	got, err := env.client.Query(index, fixedResponder{response: crafted}, env.second)
	if got != nil || err != ErrVerification {
		t.Fatalf("Query = (%x, %v), want (nil, ErrVerification)", got, err)
	}
}

func TestSingleBitTamperWithValidSignatureIsCaughtByTag(t *testing.T) {
	t.Parallel()

	env := newTestEnv(t, 19)
	const index = 6

	q0, q1, err := GenerateQuery(&patternReader{}, env.registry.N, index)
	if err != nil {
		t.Fatal(err)
	}
	malicious := NewMaliciousServer(env.first, AttackFlipPayloadSigned)
	r0, err := malicious.Respond(q0)
	if err != nil {
		t.Fatal(err)
	}
	r1, err := env.second.Respond(q1)
	if err != nil {
		t.Fatal(err)
	}

	if !VerifyResponseSignature(r0, q0, env.registry.N) {
		t.Fatal("tampered payload should still carry a valid Ed25519 signature")
	}
	if !VerifyResponseSignature(r1, q1, env.registry.N) {
		t.Fatal("honest server signature did not verify")
	}

	row := xorResponses(t, r0, r1)
	if VerifyRowTag(env.key, index, row) {
		t.Fatal("HMAC accepted a row recovered from a one-bit tampered response")
	}

	got, err := env.client.Query(index, malicious, env.second)
	if got != nil || err != ErrVerification {
		t.Fatalf("Query = (%x, %v), want (nil, ErrVerification)", got, err)
	}
}

func TestTamperWithoutResigningIsCaughtBySignature(t *testing.T) {
	t.Parallel()

	env := newTestEnv(t, 19)
	q0, _, err := GenerateQuery(&patternReader{}, env.registry.N, 2)
	if err != nil {
		t.Fatal(err)
	}
	response, err := NewMaliciousServer(env.first, AttackFlipPayloadUnsigned).Respond(q0)
	if err != nil {
		t.Fatal(err)
	}
	if VerifyResponseSignature(response, q0, env.registry.N) {
		t.Fatal("signature verified after an unsigned payload flip")
	}
}

func TestSnapshotTamperIsRejected(t *testing.T) {
	t.Parallel()

	env := newTestEnv(t, 19)
	const index = 3
	q0, _, err := GenerateQuery(&patternReader{}, env.registry.N, index)
	if err != nil {
		t.Fatal(err)
	}

	malicious := NewMaliciousServer(env.first, AttackChangeSnapshot)
	response, err := malicious.Respond(q0)
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyResponseSignature(response, q0, env.registry.N) {
		t.Fatal("the changed snapshot ID was not validly signed")
	}
	if response.SnapshotID == env.registry.SnapshotID {
		t.Fatal("fixture did not change the snapshot ID")
	}

	got, err := env.client.Query(index, malicious, env.second)
	if got != nil || err != ErrVerification {
		t.Fatalf("Query = (%x, %v), want (nil, ErrVerification)", got, err)
	}
}

func TestMerkleRootTamperIsRejected(t *testing.T) {
	t.Parallel()

	env := newTestEnv(t, 19)
	const index = 8
	malicious := NewMaliciousServer(env.first, AttackChangeMerkleRoot)

	got, err := env.client.Query(index, malicious, env.second)
	if got != nil || err != ErrVerification {
		t.Fatalf("Query = (%x, %v), want (nil, ErrVerification)", got, err)
	}
}

func TestCorruptSignatureIsRejected(t *testing.T) {
	t.Parallel()

	env := newTestEnv(t, 19)
	malicious := NewMaliciousServer(env.first, AttackCorruptSignature)
	got, err := env.client.Query(1, malicious, env.second)
	if got != nil || err != ErrVerification {
		t.Fatalf("Query = (%x, %v), want (nil, ErrVerification)", got, err)
	}
}

func TestReplayOfAnotherQueryIsRejected(t *testing.T) {
	t.Parallel()

	env := newTestEnv(t, 17)
	queryLen := QueryLength(env.registry.N)

	oldQ0, _, err := GenerateQuery(bytes.NewReader(make([]byte, queryLen)), env.registry.N, 0)
	if err != nil {
		t.Fatal(err)
	}
	cached, err := env.first.Respond(oldQ0)
	if err != nil {
		t.Fatal(err)
	}
	replay := NewReplayServer(env.first, cached)

	newBytes := bytes.Repeat([]byte{0x5a}, queryLen)
	newQ0, _, err := GenerateQuery(bytes.NewReader(newBytes), env.registry.N, 4)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(oldQ0, newQ0) {
		t.Fatal("test setup accidentally replayed the same query vector")
	}
	if VerifyResponseSignature(cached, newQ0, env.registry.N) {
		t.Fatal("a response signed for another query digest verified")
	}

	client, err := NewClient(env.registry, env.key, [2]ed25519.PublicKey{env.pub1, env.pub2}, bytes.NewReader(newBytes))
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.Query(4, replay, env.second)
	if got != nil || err != ErrVerification {
		t.Fatalf("Query = (%x, %v), want (nil, ErrVerification)", got, err)
	}
}

func TestSameServerIdentityIsRejected(t *testing.T) {
	t.Parallel()

	env := newTestEnv(t, 12)
	_, privateKey, err := GenerateServerIdentity()
	if err != nil {
		t.Fatal(err)
	}
	first, second, err := NewSameIdentityServers(env.db, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	publicKey := privateKey.Public().(ed25519.PublicKey)

	q0, q1, err := GenerateQuery(&patternReader{}, env.registry.N, 2)
	if err != nil {
		t.Fatal(err)
	}
	r0, err := first.Respond(q0)
	if err != nil {
		t.Fatal(err)
	}
	r1, err := second.Respond(q1)
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyResponseSignature(r0, q0, env.registry.N) ||
		!VerifyResponseSignature(r1, q1, env.registry.N) {
		t.Fatal("fixture responses should have valid signatures")
	}
	if !bytes.Equal(r0.ServerID, r1.ServerID) {
		t.Fatal("fixture identities are not equal")
	}

	client, err := NewClient(env.registry, env.key, [2]ed25519.PublicKey{publicKey, publicKey}, &patternReader{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.Query(2, first, second)
	if got != nil || err != ErrVerification {
		t.Fatalf("Query = (%x, %v), want (nil, ErrVerification)", got, err)
	}
}

func TestUnpinnedServerIdentityIsRejected(t *testing.T) {
	t.Parallel()

	env := newTestEnv(t, 12)
	_, otherPrivateKey, err := GenerateServerIdentity()
	if err != nil {
		t.Fatal(err)
	}
	otherServer, err := NewServer(env.db, otherPrivateKey)
	if err != nil {
		t.Fatal(err)
	}

	got, err := env.client.Query(1, env.first, otherServer)
	if got != nil || err != ErrVerification {
		t.Fatalf("Query = (%x, %v), want (nil, ErrVerification)", got, err)
	}
}

func TestServerFromDifferentSnapshotIsRejected(t *testing.T) {
	t.Parallel()

	env := newTestEnv(t, 10)
	otherDB, err := Publish(env.records, env.key, "another-snapshot")
	if err != nil {
		t.Fatal(err)
	}
	otherServer, err := NewServer(otherDB, env.priv2)
	if err != nil {
		t.Fatal(err)
	}

	got, err := env.client.Query(5, env.first, otherServer)
	if got != nil || err != ErrVerification {
		t.Fatalf("Query = (%x, %v), want (nil, ErrVerification)", got, err)
	}
}

func TestEveryAttackReturnsTheSameError(t *testing.T) {
	t.Parallel()

	env := newTestEnv(t, 23)
	attacks := []struct {
		name   string
		attack Attack
	}{
		{"flip signed", AttackFlipPayloadSigned},
		{"flip unsigned", AttackFlipPayloadUnsigned},
		{"snapshot", AttackChangeSnapshot},
		{"Merkle root", AttackChangeMerkleRoot},
		{"signature", AttackCorruptSignature},
	}

	for _, test := range attacks {
		test := test
		t.Run(test.name, func(t *testing.T) {
			malicious := NewMaliciousServer(env.first, test.attack)
			got, err := env.client.Query(11, malicious, env.second)
			if got != nil || err != ErrVerification {
				t.Fatalf("Query = (%x, %v), want (nil, ErrVerification)", got, err)
			}
			if err.Error() != ErrVerification.Error() {
				t.Fatalf("error text leaked the cause: %q", err)
			}
		})
	}
}
