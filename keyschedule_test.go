package treekem

import (
	"bytes"
	"testing"
)

func TestJoinerEpochTranscriptChain(t *testing.T) {
	oldInit := bytes.Repeat([]byte{0x11}, hashSize)
	commitSecret := bytes.Repeat([]byte{0x22}, hashSize)
	joiner := JoinerSecret(oldInit, commitSecret)
	if len(joiner) != hashSize {
		t.Fatalf("joiner length=%d", len(joiner))
	}
	if bytes.Equal(joiner, oldInit) || bytes.Equal(joiner, commitSecret) {
		t.Fatal("joiner_secret did not mix init and commit secrets")
	}

	groupID := []byte("key-schedule")
	transcript := NewInitialTranscript(groupID)
	context := &GroupContext{
		GroupID:                 groupID,
		Epoch:                   7,
		TreeHash:                bytes.Repeat([]byte{0x33}, hashSize),
		ConfirmedTranscriptHash: transcript.Confirmed,
	}
	secrets, err := DeriveEpochSecrets(joiner, context)
	if err != nil {
		t.Fatal(err)
	}
	if len(secrets.EpochSecret) != hashSize || len(secrets.InitSecret) != hashSize {
		t.Fatal("unexpected derived secret length")
	}
	if bytes.Equal(secrets.EpochSecret, secrets.ConfirmationKey) {
		t.Fatal("epoch and confirmation secrets must differ")
	}
	if bytes.Equal(secrets.EncryptionSecret, secrets.ExporterSecret) {
		t.Fatal("encryption and exporter secrets must differ")
	}

	nextJoiner := JoinerSecret(secrets.InitSecret, commitSecret)
	nextContext := *context
	nextContext.Epoch++
	nextContext.ConfirmedTranscriptHash = hashBytes("next", transcript.Confirmed)
	nextSecrets, err := DeriveEpochSecrets(nextJoiner, &nextContext)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(secrets.EpochSecret, nextSecrets.EpochSecret) {
		t.Fatal("epoch secret did not advance across epochs")
	}

	confirmation := confirmationTag(secrets.ConfirmationKey, context.ConfirmedTranscriptHash)
	if !verifyConfirmation(secrets.ConfirmationKey, context.ConfirmedTranscriptHash, confirmation) {
		t.Fatal("valid confirmation tag rejected")
	}
	if verifyConfirmation(secrets.ConfirmationKey, nextContext.ConfirmedTranscriptHash, confirmation) {
		t.Fatal("confirmation tag was accepted for wrong transcript")
	}

	advanced := transcript.advance([]byte("commit-bytes"), confirmation)
	if bytes.Equal(transcript.Confirmed, advanced.Confirmed) || bytes.Equal(transcript.Interim, advanced.Interim) {
		t.Fatal("transcript hashes did not advance")
	}
}
