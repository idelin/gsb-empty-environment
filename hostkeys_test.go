package netsec

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testHost = "example.com"

func TestHandleRejectsMissingProofWithoutWriting(t *testing.T) {
	_, _, oldKey := generateEd25519Key(t)
	_, _, newKey := generateEd25519Key(t)
	path := writeKnownHosts(t, testHost+" "+lineKey(oldKey)+"\n")
	before := readFile(t, path)

	request := validRequest(path, marshalHostKeys(newKey))
	_, err := Handle(request, nil)
	if !errors.Is(err, ErrNoProof) {
		t.Fatalf("Handle() error = %v, want ErrNoProof", err)
	}
	assertFileUnchanged(t, path, before)

	_, err = Handle(request, SingleProverFunc(func([]byte) ([]byte, error) {
		return nil, nil
	}))
	if !errors.Is(err, ErrNoProof) {
		t.Fatalf("Handle() with nil signature error = %v, want ErrNoProof", err)
	}
	assertFileUnchanged(t, path, before)
}

func TestHandleRejectsProofFromAnotherSession(t *testing.T) {
	_, _, oldKey := generateEd25519Key(t)
	_, newPrivate, newKey := generateEd25519Key(t)
	path := writeKnownHosts(t, testHost+" "+lineKey(oldKey)+"\n")
	before := readFile(t, path)

	signingSession := bytes.Repeat([]byte{0x42}, 32)
	actualSession := bytes.Repeat([]byte{0x24}, 32)
	signature, err := SignProof(newPrivate, signingSession, newKey.Blob)
	if err != nil {
		t.Fatal(err)
	}

	request := validRequest(path, marshalHostKeys(newKey))
	request.SessionID = actualSession
	_, err = Handle(request, ProverFunc(func(keys [][]byte) ([][]byte, error) {
		if len(keys) != 1 {
			t.Fatalf("prover got %d keys, want 1", len(keys))
		}
		return [][]byte{signature}, nil
	}))
	if !errors.Is(err, ErrProofVerification) {
		t.Fatalf("Handle() error = %v, want ErrProofVerification", err)
	}
	assertFileUnchanged(t, path, before)
}

func TestHandlePartialProofFailureWritesNothing(t *testing.T) {
	_, _, oldKey := generateEd25519Key(t)
	_, firstPrivate, firstKey := generateEd25519Key(t)
	_, secondPrivate, secondKey := generateEd25519Key(t)
	path := writeKnownHosts(t, testHost+" "+lineKey(oldKey)+"\n")
	before := readFile(t, path)
	sessionID := bytes.Repeat([]byte{0x11}, 32)

	firstSignature, err := SignProof(firstPrivate, sessionID, firstKey.Blob)
	if err != nil {
		t.Fatal(err)
	}
	wrongSessionSignature, err := SignProof(secondPrivate, bytes.Repeat([]byte{0x22}, 32), secondKey.Blob)
	if err != nil {
		t.Fatal(err)
	}

	request := validRequest(path, marshalHostKeys(firstKey, secondKey))
	request.SessionID = sessionID
	_, err = Handle(request, ProverFunc(func(keys [][]byte) ([][]byte, error) {
		if len(keys) != 2 {
			t.Fatalf("prover got %d keys, want 2", len(keys))
		}
		return [][]byte{firstSignature, wrongSessionSignature}, nil
	}))
	if !errors.Is(err, ErrProofVerification) {
		t.Fatalf("Handle() error = %v, want ErrProofVerification", err)
	}

	// The first key and the valid first signature must not be committed, and
	// the old key must not have been removed by the failed batch.
	assertFileUnchanged(t, path, before)
}

func TestHandleCommitsVerifiedRotationAtomically(t *testing.T) {
	_, _, oldKey := generateEd25519Key(t)
	_, newPrivate, newKey := generateEd25519Key(t)
	_, _, unrelatedKey := generateEd25519Key(t)
	path := writeKnownHosts(t,
		"# keep this comment\n"+
			testHost+" "+lineKey(oldKey)+"\n"+
			"other.example "+lineKey(unrelatedKey)+"\n",
	)

	request := validRequest(path, marshalHostKeys(newKey))
	result, err := Handle(request, proofForKeys(t, request.SessionID, newPrivate, newKey))
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if !result.Changed || len(result.Added) != 1 || len(result.Removed) != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if !bytes.Equal(result.Added[0].Blob, newKey.Blob) || !bytes.Equal(result.Removed[0].Blob, oldKey.Blob) {
		t.Fatal("result reported the wrong keys")
	}

	contents := string(readFile(t, path))
	if !strings.Contains(contents, "# keep this comment\n") {
		t.Fatal("unrelated comment was removed")
	}
	if !strings.Contains(contents, "other.example "+lineKey(unrelatedKey)) {
		t.Fatal("unrelated host entry was altered")
	}
	if strings.Contains(contents, lineKey(oldKey)) {
		t.Fatal("old host key was not removed")
	}
	if !strings.Contains(contents, testHost+" "+lineKey(newKey)) {
		t.Fatalf("new host key was not added:\n%s", contents)
	}
}

func TestHandleRemovalRequiresProofOfRetainedKey(t *testing.T) {
	_, retainedPrivate, retainedKey := generateEd25519Key(t)
	_, _, removedKey := generateEd25519Key(t)
	path := writeKnownHosts(t,
		testHost+" "+lineKey(removedKey)+"\n"+
			testHost+" "+lineKey(retainedKey)+"\n",
	)
	before := readFile(t, path)

	request := validRequest(path, marshalHostKeys(retainedKey))
	_, err := Handle(request, nil)
	if !errors.Is(err, ErrNoProof) {
		t.Fatalf("Handle() without proof error = %v, want ErrNoProof", err)
	}
	assertFileUnchanged(t, path, before)

	proverCalls := 0
	result, err := Handle(request, ProverFunc(func(keys [][]byte) ([][]byte, error) {
		proverCalls++
		if len(keys) != 1 || !bytes.Equal(keys[0], retainedKey.Blob) {
			t.Fatalf("prover got unexpected keys")
		}
		signature, err := SignProof(retainedPrivate, request.SessionID, retainedKey.Blob)
		return [][]byte{signature}, err
	}))
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if proverCalls != 1 || !result.Changed || len(result.Removed) != 1 {
		t.Fatalf("unexpected result: calls=%d result=%+v", proverCalls, result)
	}

	contents := string(readFile(t, path))
	if strings.Contains(contents, lineKey(removedKey)) {
		t.Fatal("removed key remained in known_hosts")
	}
	if !strings.Contains(contents, lineKey(retainedKey)) {
		t.Fatal("retained key was removed")
	}
}

func TestHandleRefusesUntrustedPreconditions(t *testing.T) {
	_, privateKey, advertisedKey := generateEd25519Key(t)
	path := writeKnownHosts(t, testHost+" "+lineKey(advertisedKey)+"\n")
	before := readFile(t, path)

	request := validRequest(path, marshalHostKeys(advertisedKey))
	request.HostAuthenticated = false
	called := false
	_, err := Handle(request, ProverFunc(func(keys [][]byte) ([][]byte, error) {
		called = true
		return nil, nil
	}))
	if !errors.Is(err, ErrHostNotAuthenticated) {
		t.Fatalf("Handle() error = %v, want ErrHostNotAuthenticated", err)
	}
	if called {
		t.Fatal("prover was called before host authentication")
	}
	assertFileUnchanged(t, path, before)

	otherPath := writeKnownHosts(t, "other.example "+lineKey(advertisedKey)+"\n")
	otherBefore := readFile(t, otherPath)
	request = validRequest(otherPath, marshalHostKeys(advertisedKey))
	called = false
	_, err = Handle(request, ProverFunc(func(keys [][]byte) ([][]byte, error) {
		called = true
		signature, signErr := SignProof(privateKey, request.SessionID, advertisedKey.Blob)
		return [][]byte{signature}, signErr
	}))
	if !errors.Is(err, ErrNoKnownHostsEntry) {
		t.Fatalf("Handle() error = %v, want ErrNoKnownHostsEntry", err)
	}
	if called {
		t.Fatal("prover was called without an existing known_hosts entry")
	}
	assertFileUnchanged(t, otherPath, otherBefore)
}

func TestHandleRejectsRevokedAdvertisedKey(t *testing.T) {
	_, _, trustedKey := generateEd25519Key(t)
	_, _, revokedKey := generateEd25519Key(t)
	path := writeKnownHosts(t,
		testHost+" "+lineKey(trustedKey)+"\n"+
			"@revoked "+testHost+" "+lineKey(revokedKey)+"\n",
	)
	before := readFile(t, path)
	request := validRequest(path, marshalHostKeys(trustedKey, revokedKey))

	_, err := Handle(request, ProverFunc(func(keys [][]byte) ([][]byte, error) {
		t.Fatal("prover must not be called for a revoked key")
		return nil, nil
	}))
	if !errors.Is(err, ErrRevokedKey) {
		t.Fatalf("Handle() error = %v, want ErrRevokedKey", err)
	}
	assertFileUnchanged(t, path, before)
}

func TestHandleHashedKnownHostsEntry(t *testing.T) {
	_, _, oldKey := generateEd25519Key(t)
	_, newPrivate, newKey := generateEd25519Key(t)
	hashedHost, err := hashedHostField(testHost)
	if err != nil {
		t.Fatal(err)
	}
	path := writeKnownHosts(t, hashedHost+" "+lineKey(oldKey)+"\n")

	request := validRequest(path, marshalHostKeys(newKey))
	_, err = Handle(request, proofForKeys(t, request.SessionID, newPrivate, newKey))
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	contents := string(readFile(t, path))
	if strings.Contains(contents, testHost) {
		t.Fatalf("plain host name was written to a hashed known_hosts: %s", contents)
	}
	if strings.Contains(contents, lineKey(oldKey)) || !strings.Contains(contents, lineKey(newKey)) {
		t.Fatalf("unexpected hashed known_hosts contents: %s", contents)
	}
}

func TestHandleSplitsMultiHostEntryOnRemoval(t *testing.T) {
	_, _, oldKey := generateEd25519Key(t)
	_, newPrivate, newKey := generateEd25519Key(t)
	path := writeKnownHosts(t, testHost+",alias.example "+lineKey(oldKey)+"\n")

	request := validRequest(path, marshalHostKeys(newKey))
	_, err := Handle(request, proofForKeys(t, request.SessionID, newPrivate, newKey))
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	contents := string(readFile(t, path))
	if !strings.Contains(contents, "alias.example "+lineKey(oldKey)) {
		t.Fatalf("alias entry was not preserved: %s", contents)
	}
	if strings.Contains(contents, testHost+",alias.example") {
		t.Fatalf("removed host was not split out of the shared entry: %s", contents)
	}
}

func TestConnProverUsesBatchProtocol(t *testing.T) {
	_, signer, key := generateEd25519Key(t)
	sessionID := bytes.Repeat([]byte{0x33}, 32)
	signature, err := SignProof(signer, sessionID, key.Blob)
	if err != nil {
		t.Fatal(err)
	}
	sender := &fakeRequestSender{
		ok:       true,
		response: marshalStringList([][]byte{signature}),
	}
	prover := ConnProver{Conn: sender}
	signatures, err := prover.ProveHostKeys([][]byte{key.Blob})
	if err != nil {
		t.Fatal(err)
	}
	if len(signatures) != 1 || !bytes.Equal(signatures[0], signature) {
		t.Fatal("ConnProver returned an unexpected signature")
	}
	if sender.name != HostKeysProveRequest || !sender.wantReply {
		t.Fatalf("unexpected request: name=%q wantReply=%v", sender.name, sender.wantReply)
	}
	keys, err := parseStringList(sender.payload)
	if err != nil || len(keys) != 1 || !bytes.Equal(keys[0], key.Blob) {
		t.Fatal("ConnProver sent an invalid request payload")
	}
}

func TestProofVerificationForSupportedKeyTypes(t *testing.T) {
	sessionID := bytes.Repeat([]byte{0x77}, 32)
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	ecdsaKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, edKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	testCases := []struct {
		name    string
		private any
		public  any
	}{
		{name: "ed25519", private: edKey, public: edKey.Public()},
		{name: "rsa", private: rsaKey, public: &rsaKey.PublicKey},
		{name: "ecdsa", private: ecdsaKey, public: &ecdsaKey.PublicKey},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			key, err := MarshalPublicKey(testCase.public)
			if err != nil {
				t.Fatal(err)
			}
			signature, err := SignProof(testCase.private, sessionID, key.Blob)
			if err != nil {
				t.Fatal(err)
			}
			if err := VerifyProof(key, sessionID, signature); err != nil {
				t.Fatalf("VerifyProof() error = %v", err)
			}
			if err := VerifyProof(key, bytes.Repeat([]byte{0x88}, 32), signature); !errors.Is(err, ErrProofVerification) {
				t.Fatalf("cross-session verification error = %v, want ErrProofVerification", err)
			}
		})
	}
}

func TestParseHostKeysRejectsMalformedAndDuplicateKeys(t *testing.T) {
	_, _, key := generateEd25519Key(t)
	if _, err := ParseHostKeys([]byte{0, 0, 0}); !errors.Is(err, ErrInvalidNotification) {
		t.Fatalf("truncated payload error = %v", err)
	}
	if _, err := ParseHostKeys(marshalHostKeys(key, key)); !errors.Is(err, ErrInvalidNotification) {
		t.Fatalf("duplicate payload error = %v", err)
	}
}

func validRequest(path string, payload []byte) Request {
	return Request{
		Host:              testHost,
		KnownHostsPath:    path,
		SessionID:         bytes.Repeat([]byte{0x01}, 32),
		HostAuthenticated: true,
		Payload:           payload,
	}
}

func proofForKeys(t *testing.T, sessionID []byte, privateKey ed25519.PrivateKey, keys ...HostKey) ProverFunc {
	t.Helper()
	return func(requested [][]byte) ([][]byte, error) {
		if len(requested) != len(keys) {
			return nil, fmt.Errorf("got %d requested keys, want %d", len(requested), len(keys))
		}
		signatures := make([][]byte, len(requested))
		for index, blob := range requested {
			if !bytes.Equal(blob, keys[index].Blob) {
				return nil, fmt.Errorf("unexpected key at %d", index)
			}
			signature, err := SignProof(privateKey, sessionID, blob)
			if err != nil {
				return nil, err
			}
			signatures[index] = signature
		}
		return signatures, nil
	}
}

func generateEd25519Key(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey, HostKey) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := MarshalPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	return public, private, key
}

func marshalHostKeys(keys ...HostKey) []byte {
	blobs := make([][]byte, len(keys))
	for index, key := range keys {
		blobs[index] = key.Blob
	}
	return marshalStringList(blobs)
}

func lineKey(key HostKey) string {
	return key.Type + " " + base64.StdEncoding.EncodeToString(key.Blob)
}

func writeKnownHosts(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return contents
}

func assertFileUnchanged(t *testing.T, path string, before []byte) {
	t.Helper()
	after := readFile(t, path)
	if !bytes.Equal(after, before) {
		t.Fatalf("known_hosts changed after rejected update:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

type fakeRequestSender struct {
	ok        bool
	response  []byte
	err       error
	name      string
	wantReply bool
	payload   []byte
}

func (sender *fakeRequestSender) SendRequest(name string, wantReply bool, payload []byte) (bool, []byte, error) {
	sender.name = name
	sender.wantReply = wantReply
	sender.payload = append([]byte(nil), payload...)
	return sender.ok, sender.response, sender.err
}
