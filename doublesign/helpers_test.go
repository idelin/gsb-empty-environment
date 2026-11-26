package doublesign

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
)

const testChain = "chain-test-1"

type testKey struct {
	id   string
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey
}

func genKey(t *testing.T, id string) testKey {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return testKey{id: id, priv: priv, pub: pub}
}

func genAuthority(t *testing.T) (string, ed25519.PrivateKey, ed25519.PublicKey) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return "authority", priv, pub
}

func digest(b byte) []byte {
	d := make([]byte, 32)
	d[0] = b
	return d
}

func (k testKey) member(weight uint64) Member {
	return Member{ID: k.id, PubKey: k.pub, Weight: weight}
}

// share builds and signs a consensus message.
func share(k testKey, epoch, height, round uint64, phase Phase, block, parent []byte, locked uint64) Message {
	m := Message{
		ChainID:      testChain,
		Epoch:        epoch,
		Height:       height,
		Round:        round,
		Phase:        phase,
		MemberID:     k.id,
		BlockDigest:  block,
		ParentDigest: parent,
		LockedRound:  locked,
	}
	m.Signature = Sign(k.priv, testChain, m)
	return m
}

func viewShare(k testKey, epoch, height, round, locked, qcHeight, qcRound uint64, block []byte) Message {
	m := Message{
		ChainID:     testChain,
		Epoch:       epoch,
		Height:      height,
		Round:       round,
		Phase:       PhaseViewChange,
		MemberID:    k.id,
		BlockDigest: block,
		LockedRound: locked,
		QCHeight:    qcHeight,
		QCRound:     qcRound,
	}
	m.Signature = Sign(k.priv, testChain, m)
	return m
}

func mustRegister(t *testing.T, a *Auditor, set EpochSet, anchor AuthorityAnchor, pub ed25519.PublicKey) {
	if err := a.RegisterEpoch(set, anchor, pub); err != nil {
		t.Fatal(err)
	}
}

func expectVerdict(t *testing.T, r Result, want Verdict) {
	t.Helper()
	if r.Verdict != want {
		t.Fatalf("got verdict %s (%s), want %s", r.Verdict, r.Reason, want)
	}
}

func randomSignature(t *testing.T) []byte {
	t.Helper()
	sig := make([]byte, ed25519.SignatureSize)
	if _, err := rand.Read(sig); err != nil {
		t.Fatal(err)
	}
	return sig
}
