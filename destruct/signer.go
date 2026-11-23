package destruct

import (
	"crypto/ed25519"
	"fmt"
)

// confirmationCanonicalMessage is the byte string a replica signs. It is
// versioned and contains every identity field needed to prevent replays
// across volumes, transactions, locations and generations.
func confirmationCanonicalMessage(v VolumeID, txn TxnID, replica ReplicaID, location Location, generation uint64) []byte {
	return []byte(fmt.Sprintf(
		"erasure-confirmation/v1\nvolume=%s\ntxn=%s\nreplica=%s\nlocation=%s\ngeneration=%d\n",
		v, txn, replica, location, generation,
	))
}

// SignConfirmation produces an Ed25519 signature over the confirmation
// identity tuple. Replica-side eraser implementations use this helper.
func SignConfirmation(priv ed25519.PrivateKey, v VolumeID, txn TxnID, replica ReplicaID, location Location, generation uint64) []byte {
	msg := confirmationCanonicalMessage(v, txn, replica, location, generation)
	return ed25519.Sign(priv, msg)
}

// VerifyConfirmation checks an Ed25519 signature against the replica public key.
func VerifyConfirmation(pub ed25519.PublicKey, v VolumeID, txn TxnID, replica ReplicaID, location Location, generation uint64, sig []byte) bool {
	return ed25519.Verify(pub, confirmationCanonicalMessage(v, txn, replica, location, generation), sig)
}
