package netsec

import (
	"crypto/ed25519"
	"crypto/rand"
	"sort"
	"sync/atomic"
	"testing"
)

type testFixture struct {
	account    AccountID
	masterPub  ed25519.PublicKey
	masterPriv ed25519.PrivateKey
	logID      string
}

func newFixture(t *testing.T) *testFixture {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return &testFixture{
		account:    "account-bob",
		masterPub:  pub,
		masterPriv: priv,
		logID:      "transparency-log-test",
	}
}

func newTestKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func (f *testFixture) list(t *testing.T, version Version, previous Digest, devices ...Device) AccountState {
	t.Helper()
	list, err := NewAccountState(f.account, version, previous, devices...)
	if err != nil {
		t.Fatalf("NewAccountState: %v", err)
	}
	return list
}

func (f *testFixture) sign(t *testing.T, list AccountState) *SignedDeviceList {
	t.Helper()
	signed, err := SignDeviceList(f.masterPriv, list)
	if err != nil {
		t.Fatalf("SignDeviceList: %v", err)
	}
	return signed
}

func mustDigest(t *testing.T, list AccountState) Digest {
	t.Helper()
	digest, err := list.Digest()
	if err != nil {
		t.Fatalf("Digest: %v", err)
	}
	return digest
}

type fakeLog struct {
	logID  string
	leaves []Digest
}

func newFakeLog(logID string, seed ...Digest) *fakeLog {
	return &fakeLog{
		logID:  logID,
		leaves: append([]Digest(nil), seed...),
	}
}

func (l *fakeLog) clone() *fakeLog {
	return newFakeLog(l.logID, l.leaves...)
}

func (l *fakeLog) publish(t *testing.T, signed *SignedDeviceList) LogInclusionProof {
	t.Helper()
	digest, err := signed.Digest()
	if err != nil {
		t.Fatalf("list digest: %v", err)
	}
	index := len(l.leaves)
	l.leaves = append(l.leaves, digest)
	checkpoint := NewCheckpoint(l.logID, l.leaves)
	proof, err := BuildInclusionProof(l.leaves, uint64(index), checkpoint)
	if err != nil {
		t.Fatalf("BuildInclusionProof: %v", err)
	}
	return proof
}

func (l *fakeLog) checkpoint() LogCheckpoint {
	return NewCheckpoint(l.logID, l.leaves)
}

func applyCommitted(t *testing.T, client *Client, signed *SignedDeviceList, proof LogInclusionProof) UpdateResult {
	t.Helper()
	result, err := client.Apply(signed, &proof)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !result.Committed() {
		t.Fatalf("update was not committed: %+v", result)
	}
	return result
}

func removeDevice(t *testing.T, list AccountState, deviceID DeviceID, version Version) AccountState {
	t.Helper()
	devices := make([]Device, len(list.Devices))
	found := false
	for i, device := range list.Devices {
		devices[i] = device.Clone()
		if device.DeviceID == deviceID {
			removed := version
			devices[i].RemovedVersion = &removed
			found = true
		}
	}
	if !found {
		t.Fatalf("device %q not found", deviceID)
	}
	return AccountState{
		AccountID:      list.AccountID,
		Version:        version,
		Devices:        devices,
		PreviousDigest: mustDigest(t, list),
	}
}

func addDevice(t *testing.T, list AccountState, device Device, version Version) AccountState {
	t.Helper()
	devices := make([]Device, 0, len(list.Devices)+1)
	for _, existing := range list.Devices {
		devices = append(devices, existing.Clone())
	}
	devices = append(devices, device.Clone())
	sort.Slice(devices, func(i, j int) bool {
		return devices[i].DeviceID < devices[j].DeviceID
	})
	return AccountState{
		AccountID:      list.AccountID,
		Version:        version,
		Devices:        devices,
		PreviousDigest: mustDigest(t, list),
	}
}

type delayVerifier struct {
	delay atomic.Bool
	calls atomic.Int64
}

func (v *delayVerifier) VerifyProof(_ LogCheckpoint, _ LogInclusionProof, _ Digest) error {
	v.calls.Add(1)
	if v.delay.Load() {
		return ErrProofPending
	}
	return nil
}

func observationFor(t *testing.T, account AccountID, version Version, list AccountState, checkpoint LogCheckpoint) Observation {
	t.Helper()
	return Observation{
		AccountID:        account,
		Version:          version,
		ListDigest:       mustDigest(t, list),
		CheckpointDigest: checkpoint.Digest(),
	}
}

func signedObservation(t *testing.T, observer ContactID, priv ed25519.PrivateKey, obs Observation) *SignedObservation {
	t.Helper()
	receipt, err := SignObservation(observer, priv, obs)
	if err != nil {
		t.Fatalf("SignObservation: %v", err)
	}
	return receipt
}
