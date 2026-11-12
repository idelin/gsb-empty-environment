package netsec

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"testing"
)

type testFixture struct {
	pmk          []byte
	snonce       []byte
	aa           net.HardwareAddr
	spa          net.HardwareAddr
	plainKeyData []byte
}

type installCapture struct {
	anonces []([]byte)
	ptks    []*PTK
}

func (c *installCapture) install(anonce []byte, ptk *PTK) error {
	c.anonces = append(c.anonces, append([]byte(nil), anonce...))
	c.ptks = append(c.ptks, clonePTK(ptk))
	return nil
}

func (c *installCapture) count() int {
	return len(c.anonces)
}

type keyDataCapture struct {
	data [][]byte
}

func (c *keyDataCapture) handle(data []byte) error {
	c.data = append(c.data, append([]byte(nil), data...))
	return nil
}

func newStateFixture(t *testing.T) (*EAPOLKeyStateMachine, testFixture, *installCapture, *keyDataCapture) {
	t.Helper()
	f := testFixture{
		pmk: []byte{
			0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07,
			0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f,
			0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17,
			0x18, 0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f,
		},
		snonce: []byte{
			0xff, 0xee, 0xdd, 0xcc, 0xbb, 0xaa, 0x99, 0x88,
			0x77, 0x66, 0x55, 0x44, 0x33, 0x22, 0x11, 0x00,
			0xff, 0xee, 0xdd, 0xcc, 0xbb, 0xaa, 0x99, 0x88,
			0x77, 0x66, 0x55, 0x44, 0x33, 0x22, 0x11, 0x00,
		},
		aa:  net.HardwareAddr{0x02, 0x00, 0x00, 0x00, 0x00, 0x01},
		spa: net.HardwareAddr{0x02, 0x00, 0x00, 0x00, 0x00, 0x02},
		plainKeyData: []byte{
			0xdd, 0x0e, 0x00, 0x0f, 0xac, 0x04,
			0x01, 0x00, 0x11, 0x22, 0x33, 0x44,
			0x55, 0x66, 0x77, 0x88,
		},
	}

	installs := &installCapture{}
	keyData := &keyDataCapture{}
	sm := NewEAPOLKeyStateMachine(Config{
		PMK:               f.pmk,
		SNonce:            f.snonce,
		SupplicantAddr:    f.spa,
		AuthenticatorAddr: f.aa,
		InstallPTK:        installs.install,
		KeyDataHandler:    keyData.handle,
	})
	return sm, f, installs, keyData
}

func anonce(t *testing.T, fill byte) []byte {
	t.Helper()
	b := make([]byte, 32)
	for i := range b {
		b[i] = fill + byte(i)
	}
	return b
}

func testPTK(t *testing.T, f testFixture, anonce []byte) *PTK {
	t.Helper()
	ptk, err := DerivePTK(f.pmk, anonce, f.snonce, f.aa, f.spa)
	if err != nil {
		t.Fatalf("DerivePTK: %v", err)
	}
	return ptk
}

func testMessage3(t *testing.T, f testFixture, anonce []byte, replay uint64, ptk *PTK, plainKeyData []byte, encrypt bool) []byte {
	t.Helper()
	return testMessage3WithVersion(t, f, anonce, replay, ptk, plainKeyData, encrypt, KeyDescriptorVersionHMACSHA1AES)
}

func testMessage3WithVersion(t *testing.T, f testFixture, anonce []byte, replay uint64, ptk *PTK, plainKeyData []byte, encrypt bool, version uint16) []byte {
	t.Helper()
	keyInfo := KeyInfoKeyType | KeyInfoInstall | KeyInfoAck | KeyInfoMIC | KeyInfoSecure | version
	keyData := append([]byte(nil), plainKeyData...)
	if encrypt {
		keyInfo |= KeyInfoEncryptedKeyData
		var err error
		keyData, err = AESKeyWrap(ptk.KEK, plainKeyData)
		if err != nil {
			t.Fatalf("AESKeyWrap: %v", err)
		}
	}

	frame := &EAPOLKeyFrame{
		Version:        EAPOLVersion2,
		DescriptorType: DescriptorTypeRSN,
		KeyInfo:        keyInfo,
		KeyLength:      16,
		ReplayCounter:  replay,
		KeyData:        keyData,
	}
	copy(frame.Nonce[:], anonce)
	if err := frame.Sign(ptk.KCK); err != nil {
		t.Fatalf("Sign Message 3: %v", err)
	}
	return frame.Raw
}

func corruptMIC(t *testing.T, frame []byte) []byte {
	t.Helper()
	out := append([]byte(nil), frame...)
	if len(out) < EAPOLKeyMICOffset+16 {
		t.Fatal("frame has no MIC")
	}
	out[EAPOLKeyMICOffset] ^= 0xff
	return out
}

func TestReplayCounterAdvancesOnlyAfterMIC(t *testing.T) {
	sm, f, installs, keyData := newStateFixture(t)
	a := anonce(t, 0x01)
	ptk := testPTK(t, f, a)

	// A valid first Message 3 establishes counter 10 and installs one PTK.
	if _, err := sm.ProcessMessage3(testMessage3(t, f, a, 10, ptk, f.plainKeyData, true)); err != nil {
		t.Fatalf("first Message 3: %v", err)
	}
	if got := sm.ReplayCounter(); got != 10 {
		t.Fatalf("counter after valid Message 3 = %d, want 10", got)
	}
	if got := installs.count(); got != 1 {
		t.Fatalf("install count = %d, want 1", got)
	}
	if got := len(keyData.data); got != 1 {
		t.Fatalf("Key Data handler calls = %d, want 1", got)
	}

	// A failed MIC with a much higher counter must not advance replay state.
	b := anonce(t, 0x41)
	ptkB := testPTK(t, f, b)
	bad := corruptMIC(t, testMessage3(t, f, b, 99, ptkB, f.plainKeyData, true))
	if _, err := sm.ProcessMessage3(bad); !errors.Is(err, ErrMIC) {
		t.Fatalf("bad MIC error = %v, want ErrMIC", err)
	}
	if got := sm.ReplayCounter(); got != 10 {
		t.Fatalf("counter changed after failed MIC: got %d, want 10", got)
	}
	if got := installs.count(); got != 1 {
		t.Fatalf("PTK installed after failed MIC: count %d, want 1", got)
	}
	if got := len(keyData.data); got != 1 {
		t.Fatalf("Key Data processed after failed MIC: calls %d, want 1", got)
	}

	// Because the failed attempt did not consume counter 99, a valid frame
	// with the older counter 11 is still accepted.
	if _, err := sm.ProcessMessage3(testMessage3(t, f, b, 11, ptkB, f.plainKeyData, true)); err != nil {
		t.Fatalf("valid Message 3 after failed MIC: %v", err)
	}
	if got := sm.ReplayCounter(); got != 11 {
		t.Fatalf("counter after second valid Message 3 = %d, want 11", got)
	}
	if got := installs.count(); got != 2 {
		t.Fatalf("install count = %d, want 2", got)
	}
}

func TestOldReplayCounterRejected(t *testing.T) {
	sm, f, installs, _ := newStateFixture(t)
	a := anonce(t, 0x01)
	ptkA := testPTK(t, f, a)
	if _, err := sm.ProcessMessage3(testMessage3(t, f, a, 20, ptkA, f.plainKeyData, true)); err != nil {
		t.Fatalf("first Message 3: %v", err)
	}

	// Replaying the same ANonce with a lower counter is rejected.
	oldSame := testMessage3(t, f, a, 19, ptkA, f.plainKeyData, true)
	if _, err := sm.ProcessMessage3(oldSame); !errors.Is(err, ErrReplay) {
		t.Fatalf("old same-ANonce counter error = %v, want ErrReplay", err)
	}

	// Starting a new ANonce at a lower counter cannot roll the counter back.
	b := anonce(t, 0x41)
	ptkB := testPTK(t, f, b)
	oldNew := testMessage3(t, f, b, 19, ptkB, f.plainKeyData, true)
	if _, err := sm.ProcessMessage3(oldNew); !errors.Is(err, ErrReplay) {
		t.Fatalf("old new-ANonce counter error = %v, want ErrReplay", err)
	}

	if got := sm.ReplayCounter(); got != 20 {
		t.Fatalf("counter after replays = %d, want 20", got)
	}
	if got := installs.count(); got != 1 {
		t.Fatalf("install count after replays = %d, want 1", got)
	}
}

func TestWrongMICRejectedAndKeyDataUntouched(t *testing.T) {
	sm, f, installs, keyData := newStateFixture(t)
	a := anonce(t, 0x01)
	ptkA := testPTK(t, f, a)
	if _, err := sm.ProcessMessage3(testMessage3(t, f, a, 5, ptkA, f.plainKeyData, true)); err != nil {
		t.Fatalf("first Message 3: %v", err)
	}

	b := anonce(t, 0x41)
	ptkB := testPTK(t, f, b)
	bad := corruptMIC(t, testMessage3(t, f, b, 6, ptkB, f.plainKeyData, true))
	if _, err := sm.ProcessMessage3(bad); !errors.Is(err, ErrMIC) {
		t.Fatalf("wrong MIC error = %v, want ErrMIC", err)
	}
	if got := installs.count(); got != 1 {
		t.Fatalf("wrong MIC installed a PTK: count %d, want 1", got)
	}
	if got := len(keyData.data); got != 1 {
		t.Fatalf("wrong MIC decrypted Key Data: calls %d, want 1", got)
	}
	if got := sm.ReplayCounter(); got != 5 {
		t.Fatalf("wrong MIC changed counter: got %d, want 5", got)
	}

	// The same counter is still usable because the MIC failure did not
	// commit it.
	if _, err := sm.ProcessMessage3(testMessage3(t, f, b, 6, ptkB, f.plainKeyData, true)); err != nil {
		t.Fatalf("valid Message 3 after MIC failure: %v", err)
	}
	if got := installs.count(); got != 2 {
		t.Fatalf("install count after recovery = %d, want 2", got)
	}
}

func TestDuplicateMessage3OnlyAcknowledges(t *testing.T) {
	sm, f, installs, keyData := newStateFixture(t)
	a := anonce(t, 0x01)
	ptk := testPTK(t, f, a)
	msg3 := testMessage3(t, f, a, 5, ptk, f.plainKeyData, true)

	first, err := sm.ProcessMessage3(msg3)
	if err != nil {
		t.Fatalf("first Message 3: %v", err)
	}
	if !first.Installed || first.Duplicate {
		t.Fatalf("first result installed=%v duplicate=%v", first.Installed, first.Duplicate)
	}
	if !bytes.Equal(first.KeyData, f.plainKeyData) {
		t.Fatalf("decrypted Key Data = %x, want %x", first.KeyData, f.plainKeyData)
	}

	dup, err := sm.ProcessMessage3(msg3)
	if err != nil {
		t.Fatalf("duplicate Message 3: %v", err)
	}
	if !dup.Duplicate || dup.Installed {
		t.Fatalf("duplicate result installed=%v duplicate=%v", dup.Installed, dup.Duplicate)
	}
	if dup.KeyData != nil {
		t.Fatalf("duplicate decrypted Key Data: %x", dup.KeyData)
	}
	if got := installs.count(); got != 1 {
		t.Fatalf("duplicate reinstalled PTK: count %d, want 1", got)
	}
	if got := len(keyData.data); got != 1 {
		t.Fatalf("duplicate reprocessed Key Data: calls %d, want 1", got)
	}
	if got := sm.ReplayCounter(); got != 5 {
		t.Fatalf("duplicate changed counter: got %d, want 5", got)
	}
	if dup.Response == nil || !dup.Response.IsMessage4() {
		t.Fatal("duplicate did not produce Message 4")
	}
	if err := VerifyEAPOLKeyMIC(dup.Response.Raw, ptk.KCK); err != nil {
		t.Fatalf("Message 4 MIC: %v", err)
	}
}

func TestDuplicateWithAuthenticatedHigherCounterDoesNotReinstall(t *testing.T) {
	sm, f, installs, keyData := newStateFixture(t)
	a := anonce(t, 0x01)
	ptk := testPTK(t, f, a)
	if _, err := sm.ProcessMessage3(testMessage3(t, f, a, 5, ptk, f.plainKeyData, true)); err != nil {
		t.Fatalf("first Message 3: %v", err)
	}

	dup, err := sm.ProcessMessage3(testMessage3(t, f, a, 6, ptk, f.plainKeyData, true))
	if err != nil {
		t.Fatalf("retransmitted Message 3: %v", err)
	}
	if !dup.Duplicate || dup.Installed {
		t.Fatalf("retransmission result installed=%v duplicate=%v", dup.Installed, dup.Duplicate)
	}
	if got := installs.count(); got != 1 {
		t.Fatalf("retransmission reinstalled PTK: count %d, want 1", got)
	}
	if got := len(keyData.data); got != 1 {
		t.Fatalf("retransmission reprocessed Key Data: calls %d, want 1", got)
	}
	if got := sm.ReplayCounter(); got != 6 {
		t.Fatalf("authenticated counter increase not recorded: got %d, want 6", got)
	}
}

func TestChangedANonceInstallsNewPTKOnce(t *testing.T) {
	sm, f, installs, _ := newStateFixture(t)
	a := anonce(t, 0x01)
	b := anonce(t, 0x41)
	ptkA := testPTK(t, f, a)
	ptkB := testPTK(t, f, b)

	if _, err := sm.ProcessMessage3(testMessage3(t, f, a, 5, ptkA, f.plainKeyData, true)); err != nil {
		t.Fatalf("Message 3 for ANonce A: %v", err)
	}
	if _, err := sm.ProcessMessage3(testMessage3(t, f, b, 6, ptkB, f.plainKeyData, true)); err != nil {
		t.Fatalf("Message 3 for ANonce B: %v", err)
	}

	if got := installs.count(); got != 2 {
		t.Fatalf("install count = %d, want 2", got)
	}
	if !bytes.Equal(installs.anonces[0], a) || !bytes.Equal(installs.anonces[1], b) {
		t.Fatal("installer did not receive the changing ANonce values")
	}
	if bytes.Equal(installs.ptks[0].Key, installs.ptks[1].Key) {
		t.Fatal("different ANonces produced the same PTK")
	}
	if !bytes.Equal(sm.InstalledANonce(), b) {
		t.Fatal("state machine did not retain the newest ANonce")
	}

	// A duplicate of B must still not reinstall.
	if _, err := sm.ProcessMessage3(testMessage3(t, f, b, 6, ptkB, f.plainKeyData, true)); err != nil {
		t.Fatalf("duplicate Message 3 for ANonce B: %v", err)
	}
	if got := installs.count(); got != 2 {
		t.Fatalf("duplicate reinstalled PTK: count %d, want 2", got)
	}
}

func TestMessage4Shape(t *testing.T) {
	sm, f, _, _ := newStateFixture(t)
	a := anonce(t, 0x01)
	ptk := testPTK(t, f, a)
	result, err := sm.ProcessMessage3(testMessage3(t, f, a, 7, ptk, f.plainKeyData, true))
	if err != nil {
		t.Fatalf("Message 3: %v", err)
	}
	resp := result.Response
	if resp == nil {
		t.Fatal("no Message 4 response")
	}
	if !resp.IsMessage4() {
		t.Fatalf("response KeyInfo = %#x, not Message 4", resp.KeyInfo)
	}
	if resp.ReplayCounter != 7 {
		t.Fatalf("response replay counter = %d, want 7", resp.ReplayCounter)
	}
	if resp.KeyInfo&KeyInfoAck != 0 {
		t.Fatal("Message 4 unexpectedly has ACK set")
	}
	if len(resp.KeyData) != 0 {
		t.Fatalf("Message 4 carries Key Data: %x", resp.KeyData)
	}
	if err := VerifyEAPOLKeyMIC(resp.Raw, ptk.KCK); err != nil {
		t.Fatalf("Message 4 MIC: %v", err)
	}
}

func TestAESCMACDescriptorVersion(t *testing.T) {
	sm, f, installs, keyData := newStateFixture(t)
	a := anonce(t, 0x01)
	ptk := testPTK(t, f, a)
	msg3 := testMessage3WithVersion(t, f, a, 5, ptk, f.plainKeyData, true, KeyDescriptorVersionAES128CMAC)

	result, err := sm.ProcessMessage3(msg3)
	if err != nil {
		t.Fatalf("CMAC Message 3: %v", err)
	}
	if !result.Installed || !bytes.Equal(result.KeyData, f.plainKeyData) {
		t.Fatalf("CMAC result installed=%v keyData=%x", result.Installed, result.KeyData)
	}
	if got := installs.count(); got != 1 {
		t.Fatalf("CMAC install count = %d, want 1", got)
	}
	if got := len(keyData.data); got != 1 {
		t.Fatalf("CMAC Key Data calls = %d, want 1", got)
	}
	if err := VerifyEAPOLKeyMIC(result.Response.Raw, ptk.KCK); err != nil {
		t.Fatalf("CMAC Message 4 MIC: %v", err)
	}
}

func TestParseAndMarshalRoundTrip(t *testing.T) {
	_, f, _, _ := newStateFixture(t)
	a := anonce(t, 0x01)
	ptk := testPTK(t, f, a)
	raw := testMessage3(t, f, a, 42, ptk, f.plainKeyData, true)
	frame, err := ParseEAPOLKey(raw)
	if err != nil {
		t.Fatalf("ParseEAPOLKey: %v", err)
	}
	if frame.ReplayCounter != 42 || !bytes.Equal(frame.Nonce[:], a) {
		t.Fatal("parsed fields do not match")
	}
	encoded, err := frame.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !bytes.Equal(encoded, raw) {
		t.Fatal("parse/marshal round trip mismatch")
	}
	if binary.BigEndian.Uint16(encoded[2:4]) != uint16(len(encoded)-4) {
		t.Fatal("EAPOL length field mismatch")
	}
}

func TestErrorsWrapSentinels(t *testing.T) {
	sm, f, _, _ := newStateFixture(t)
	a := anonce(t, 0x01)
	ptk := testPTK(t, f, a)
	if _, err := sm.ProcessMessage3(testMessage3(t, f, a, 5, ptk, f.plainKeyData, true)); err != nil {
		t.Fatalf("first Message 3: %v", err)
	}

	// A non-Message 3 (Message 4 shape) is rejected distinctly.
	resp := &EAPOLKeyFrame{
		Version:        EAPOLVersion2,
		DescriptorType: DescriptorTypeRSN,
		KeyInfo:        KeyDescriptorVersionHMACSHA1AES | KeyInfoKeyType | KeyInfoMIC | KeyInfoSecure,
		ReplayCounter:  6,
	}
	if err := resp.Sign(ptk.KCK); err != nil {
		t.Fatalf("sign Message 4: %v", err)
	}
	if _, err := sm.ProcessMessage3(resp.Raw); !errors.Is(err, ErrNotMessage3) {
		t.Fatalf("Message 4 error = %v, want ErrNotMessage3", err)
	}

	// A malformed EAPOL packet is rejected without state changes.
	if _, err := sm.ProcessMessage3([]byte{0x02, 0x03}); !errors.Is(err, ErrInvalidEAPOLKeyFrame) {
		t.Fatalf("truncated frame error = %v, want ErrInvalidEAPOLKeyFrame", err)
	}
}

func TestInstallerFailureDoesNotCommit(t *testing.T) {
	f := testFixture{
		pmk:          bytes.Repeat([]byte{0x01}, 32),
		snonce:       bytes.Repeat([]byte{0x02}, 32),
		aa:           net.HardwareAddr{0x02, 0, 0, 0, 0, 1},
		spa:          net.HardwareAddr{0x02, 0, 0, 0, 0, 2},
		plainKeyData: bytes.Repeat([]byte{0x03}, 16),
	}
	sm := NewEAPOLKeyStateMachine(Config{
		PMK:               f.pmk,
		SNonce:            f.snonce,
		SupplicantAddr:    f.spa,
		AuthenticatorAddr: f.aa,
		InstallPTK: func([]byte, *PTK) error {
			return fmt.Errorf("simulated install failure")
		},
	})
	a := anonce(t, 0x01)
	ptk := testPTK(t, f, a)
	if _, err := sm.ProcessMessage3(testMessage3(t, f, a, 5, ptk, f.plainKeyData, true)); err == nil {
		t.Fatal("expected installer failure")
	}
	if sm.HasReplayCounter() || sm.InstallCount() != 0 {
		t.Fatal("failed installation committed state")
	}
}
