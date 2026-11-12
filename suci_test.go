package netsec_test

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"example.org/netsec/q0236"
)

type fakeDecryptor struct {
	mu sync.Mutex

	calls       []string
	macErr      error
	decryptErr  error
	plaintext   []byte
	lastPayload netsec.EncryptedPayload
	lastKey     []byte
}

func (d *fakeDecryptor) VerifyMAC(scheme netsec.ProtectionScheme, key []byte, payload netsec.EncryptedPayload) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls = append(d.calls, "mac")
	d.lastPayload = payload
	d.lastKey = append([]byte(nil), key...)
	return d.macErr
}

func (d *fakeDecryptor) Decrypt(scheme netsec.ProtectionScheme, key []byte, payload netsec.EncryptedPayload) ([]byte, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls = append(d.calls, "decrypt")
	if d.decryptErr != nil {
		return nil, d.decryptErr
	}
	return append([]byte(nil), d.plaintext...), nil
}

func (d *fakeDecryptor) callSequence() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.calls...)
}

func (d *fakeDecryptor) reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls = nil
}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

const (
	currentKeyID  = 1
	previousKeyID = 2
)

func testKeyRing(t *testing.T) *netsec.KeyRing {
	t.Helper()
	ring, err := netsec.NewKeyRing(
		map[int][]byte{currentKeyID: []byte("current-private-key")},
		map[int][]byte{previousKeyID: []byte("previous-private-key")},
	)
	if err != nil {
		t.Fatalf("NewKeyRing: %v", err)
	}
	return ring
}

func schemeOutput(t *testing.T, scheme netsec.ProtectionScheme, ephemeralPublicKey, ciphertext, mac []byte) []byte {
	t.Helper()
	ephemeralKeyLength, macLength, ok := netsec.ProfileLayout(scheme)
	if !ok {
		t.Fatalf("no layout for scheme %v", scheme)
	}
	if len(ephemeralPublicKey) != ephemeralKeyLength {
		t.Fatalf("ephemeral key length = %d, want %d", len(ephemeralPublicKey), ephemeralKeyLength)
	}
	if len(mac) != macLength {
		t.Fatalf("MAC length = %d, want %d", len(mac), macLength)
	}
	if len(ciphertext) == 0 {
		t.Fatal("test ciphertext must not be empty")
	}
	return append(append(append([]byte(nil), ephemeralPublicKey...), ciphertext...), mac...)
}

func profileOutput(t *testing.T, scheme netsec.ProtectionScheme, epkSeed byte, ciphertext string) []byte {
	t.Helper()
	ephemeralKeyLength, macLength, _ := netsec.ProfileLayout(scheme)
	epk := bytes.Repeat([]byte{epkSeed}, ephemeralKeyLength)
	mac := bytes.Repeat([]byte{0xaa}, macLength)
	return schemeOutput(t, scheme, epk, []byte(ciphertext), mac)
}

func encryptedRequest(scheme netsec.ProtectionScheme, keyID int, output []byte) netsec.Request {
	return netsec.Request{
		RoutingIndicator:       "1234",
		ProtectionScheme:       scheme,
		HomeNetworkPublicKeyID: keyID,
		SchemeOutput:           output,
	}
}

func newTestAdmission(t *testing.T, decryptor *fakeDecryptor, opts ...netsec.Option) *netsec.Admission {
	t.Helper()
	admission, err := netsec.NewAdmission(testKeyRing(t), decryptor, opts...)
	if err != nil {
		t.Fatalf("NewAdmission: %v", err)
	}
	return admission
}

func TestNullSchemeRequiresEmergencyRegistration(t *testing.T) {
	decryptor := &fakeDecryptor{plaintext: []byte("001010000000001")}
	admission := newTestAdmission(t, decryptor)

	request := netsec.Request{
		RoutingIndicator:      "12",
		ProtectionScheme:      netsec.SchemeNull,
		EmergencyRegistration: false,
		SchemeOutput:          []byte("001010000000001"),
	}
	if _, err := admission.Admit(request); !errors.Is(err, netsec.ErrNullSchemeNotEmergency) {
		t.Fatalf("non-emergency null error = %v, want ErrNullSchemeNotEmergency", err)
	}
	if calls := decryptor.callSequence(); len(calls) != 0 {
		t.Fatalf("null rejection invoked crypto: %v", calls)
	}

	request.EmergencyRegistration = true
	result, err := admission.Admit(request)
	if err != nil {
		t.Fatalf("emergency null admission: %v", err)
	}
	if got, want := string(result.SUPI), "001010000000001"; got != want {
		t.Fatalf("SUPI = %q, want %q", got, want)
	}
	if result.KeyGeneration != netsec.KeyGenerationNone {
		t.Fatalf("null key generation = %v, want none", result.KeyGeneration)
	}
	if calls := decryptor.callSequence(); len(calls) != 0 {
		t.Fatalf("null emergency invoked crypto: %v", calls)
	}
}

func TestProtectionSchemeAdmission(t *testing.T) {
	for _, tc := range []struct {
		name         string
		scheme       netsec.ProtectionScheme
		ephemeralLen int
	}{
		{"profile A", netsec.SchemeProfileA, netsec.ProfileAEphemeralPublicKeyLength},
		{"profile B", netsec.SchemeProfileB, netsec.ProfileBEphemeralPublicKeyLength},
	} {
		t.Run(tc.name, func(t *testing.T) {
			decryptor := &fakeDecryptor{plaintext: []byte("001010000000001")}
			admission := newTestAdmission(t, decryptor)
			request := encryptedRequest(tc.scheme, currentKeyID, profileOutput(t, tc.scheme, 0x11, "ciphertext"))

			result, err := admission.Admit(request)
			if err != nil {
				t.Fatalf("Admit: %v", err)
			}
			if result.Scheme != tc.scheme {
				t.Fatalf("scheme = %v, want %v", result.Scheme, tc.scheme)
			}
			if len(decryptor.lastPayload.EphemeralPublicKey) != tc.ephemeralLen {
				t.Fatalf("ephemeral key length = %d, want %d", len(decryptor.lastPayload.EphemeralPublicKey), tc.ephemeralLen)
			}
			if got := decryptor.callSequence(); len(got) != 2 || got[0] != "mac" || got[1] != "decrypt" {
				t.Fatalf("crypto calls = %v, want [mac decrypt]", got)
			}
		})
	}
}

func TestRejectsUnknownProtectionScheme(t *testing.T) {
	decryptor := &fakeDecryptor{plaintext: []byte("unused")}
	admission := newTestAdmission(t, decryptor)
	_, err := admission.Admit(netsec.Request{
		RoutingIndicator: "1",
		ProtectionScheme: 3,
		SchemeOutput:     []byte("anything"),
	})
	if !errors.Is(err, netsec.ErrUnsupportedProtectionScheme) {
		t.Fatalf("error = %v, want ErrUnsupportedProtectionScheme", err)
	}
	if calls := decryptor.callSequence(); len(calls) != 0 {
		t.Fatalf("unsupported scheme invoked crypto: %v", calls)
	}
}

func TestRoutingIndicatorValidation(t *testing.T) {
	for _, valid := range []string{"0", "00", "123", "1234", "0000"} {
		t.Run(valid, func(t *testing.T) {
			decryptor := &fakeDecryptor{plaintext: []byte("001010000000001")}
			admission := newTestAdmission(t, decryptor)
			request := encryptedRequest(netsec.SchemeProfileA, currentKeyID, profileOutput(t, netsec.SchemeProfileA, 0x23, "ciphertext"))
			request.RoutingIndicator = valid
			if _, err := admission.Admit(request); err != nil {
				t.Fatalf("routing indicator %q rejected: %v", valid, err)
			}
		})
	}

	for _, invalid := range []string{"", "1a", "-1", "12345", " 1", "9９"} {
		t.Run(invalid, func(t *testing.T) {
			decryptor := &fakeDecryptor{plaintext: []byte("001010000000001")}
			admission := newTestAdmission(t, decryptor)
			request := encryptedRequest(netsec.SchemeProfileA, currentKeyID, profileOutput(t, netsec.SchemeProfileA, 0x24, "ciphertext"))
			request.RoutingIndicator = invalid
			_, err := admission.Admit(request)
			if !errors.Is(err, netsec.ErrInvalidRoutingIndicator) {
				t.Fatalf("routing indicator %q error = %v, want ErrInvalidRoutingIndicator", invalid, err)
			}
			if calls := decryptor.callSequence(); len(calls) != 0 {
				t.Fatalf("invalid routing indicator invoked crypto: %v", calls)
			}
		})
	}
}

func TestUnknownAndPreviousGenerationKeys(t *testing.T) {
	decryptor := &fakeDecryptor{plaintext: []byte("001010000000001")}
	admission := newTestAdmission(t, decryptor)

	unknown := encryptedRequest(netsec.SchemeProfileA, 9, profileOutput(t, netsec.SchemeProfileA, 0x31, "ciphertext"))
	if _, err := admission.Admit(unknown); !errors.Is(err, netsec.ErrUnknownHomeNetworkKey) {
		t.Fatalf("unknown key error = %v, want ErrUnknownHomeNetworkKey", err)
	}
	if calls := decryptor.callSequence(); len(calls) != 0 {
		t.Fatalf("unknown key invoked crypto: %v", calls)
	}

	old := encryptedRequest(netsec.SchemeProfileA, previousKeyID, profileOutput(t, netsec.SchemeProfileA, 0x32, "ciphertext"))
	result, err := admission.Admit(old)
	if err != nil {
		t.Fatalf("previous-generation key rejected: %v", err)
	}
	if result.KeyGeneration != netsec.KeyGenerationPrevious {
		t.Fatalf("key generation = %v, want previous", result.KeyGeneration)
	}
	if got, want := string(decryptor.lastKey), "previous-private-key"; got != want {
		t.Fatalf("decryptor key = %q, want %q", got, want)
	}
}

func TestMACIsVerifiedBeforeDecrypt(t *testing.T) {
	decryptor := &fakeDecryptor{
		plaintext: []byte("should-not-be-used"),
		macErr:    errors.New("bad mac"),
	}
	admission := newTestAdmission(t, decryptor)
	request := encryptedRequest(netsec.SchemeProfileA, currentKeyID, profileOutput(t, netsec.SchemeProfileA, 0x41, "ciphertext"))

	if _, err := admission.Admit(request); !errors.Is(err, netsec.ErrMACVerificationFailed) {
		t.Fatalf("bad MAC error = %v, want ErrMACVerificationFailed", err)
	}
	if calls := decryptor.callSequence(); len(calls) != 1 || calls[0] != "mac" {
		t.Fatalf("bad MAC crypto calls = %v, want [mac]", calls)
	}
}

func TestFailedMACDoesNotPoisonReplayCache(t *testing.T) {
	decryptor := &fakeDecryptor{
		plaintext: []byte("001010000000001"),
		macErr:    errors.New("bad mac"),
	}
	admission := newTestAdmission(t, decryptor)
	request := encryptedRequest(netsec.SchemeProfileA, currentKeyID, profileOutput(t, netsec.SchemeProfileA, 0x42, "ciphertext"))

	if _, err := admission.Admit(request); !errors.Is(err, netsec.ErrMACVerificationFailed) {
		t.Fatalf("first error = %v, want ErrMACVerificationFailed", err)
	}

	decryptor.mu.Lock()
	decryptor.macErr = nil
	decryptor.mu.Unlock()
	decryptor.reset()

	if _, err := admission.Admit(request); err != nil {
		t.Fatalf("valid retry after failed MAC rejected: %v", err)
	}
}

func TestMalformedSchemeOutput(t *testing.T) {
	for _, tc := range []struct {
		name   string
		scheme netsec.ProtectionScheme
		output []byte
	}{
		{"profile A empty", netsec.SchemeProfileA, nil},
		{"profile A missing ciphertext", netsec.SchemeProfileA, bytes.Repeat([]byte{1}, netsec.ProfileAEphemeralPublicKeyLength+netsec.ProfileAMACLength)},
		{"profile B missing ciphertext", netsec.SchemeProfileB, bytes.Repeat([]byte{2}, netsec.ProfileBEphemeralPublicKeyLength+netsec.ProfileBMACLength)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			decryptor := &fakeDecryptor{plaintext: []byte("unused")}
			admission := newTestAdmission(t, decryptor)
			_, err := admission.Admit(encryptedRequest(tc.scheme, currentKeyID, tc.output))
			if !errors.Is(err, netsec.ErrMalformedSchemeOutput) {
				t.Fatalf("error = %v, want ErrMalformedSchemeOutput", err)
			}
			if calls := decryptor.callSequence(); len(calls) != 0 {
				t.Fatalf("malformed output invoked crypto: %v", calls)
			}
		})
	}
}

func TestDecryptFailureIsReportedAndNotCommitted(t *testing.T) {
	decryptor := &fakeDecryptor{
		plaintext:  []byte("unused"),
		decryptErr: errors.New("decrypt failed"),
	}
	admission := newTestAdmission(t, decryptor)
	request := encryptedRequest(netsec.SchemeProfileA, currentKeyID, profileOutput(t, netsec.SchemeProfileA, 0x51, "ciphertext"))

	if _, err := admission.Admit(request); !errors.Is(err, netsec.ErrDecryptionFailed) {
		t.Fatalf("decrypt error = %v, want ErrDecryptionFailed", err)
	}
	if calls := decryptor.callSequence(); len(calls) != 2 || calls[0] != "mac" || calls[1] != "decrypt" {
		t.Fatalf("crypto calls = %v, want [mac decrypt]", calls)
	}

	decryptor.mu.Lock()
	decryptor.decryptErr = nil
	decryptor.mu.Unlock()
	if _, err := admission.Admit(request); err != nil {
		t.Fatalf("retry after decrypt failure rejected: %v", err)
	}
}

func TestReplayRejectedWithinWindowAndAllowedAfterExpiry(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	decryptor := &fakeDecryptor{plaintext: []byte("001010000000001")}
	admission := newTestAdmission(t, decryptor, netsec.WithClock(clock.Now), netsec.WithReplayWindow(30*time.Second))
	request := encryptedRequest(netsec.SchemeProfileA, currentKeyID, profileOutput(t, netsec.SchemeProfileA, 0x61, "ciphertext"))

	if _, err := admission.Admit(request); err != nil {
		t.Fatalf("first admission: %v", err)
	}
	replay := request
	replay.SchemeOutput = profileOutput(t, netsec.SchemeProfileA, 0x61, "other-ciphertext")
	if _, err := admission.Admit(replay); !errors.Is(err, netsec.ErrReplayDetected) {
		t.Fatalf("replay error = %v, want ErrReplayDetected", err)
	}

	clock.Advance(30 * time.Second)
	if _, err := admission.Admit(replay); err != nil {
		t.Fatalf("expired replay window rejected: %v", err)
	}
}

func TestConcurrentReplayHasSingleWinner(t *testing.T) {
	decryptor := &fakeDecryptor{plaintext: []byte("001010000000001")}
	admission := newTestAdmission(t, decryptor)
	request := encryptedRequest(netsec.SchemeProfileA, currentKeyID, profileOutput(t, netsec.SchemeProfileA, 0x71, "ciphertext"))

	var successes atomic.Int32
	var replays atomic.Int32
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := admission.Admit(request)
			switch {
			case err == nil:
				successes.Add(1)
			case errors.Is(err, netsec.ErrReplayDetected):
				replays.Add(1)
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()

	if got := successes.Load(); got != 1 {
		t.Fatalf("successes = %d, want 1", got)
	}
	if got := replays.Load(); got != 15 {
		t.Fatalf("replays = %d, want 15", got)
	}
}

func TestKeyRingRejectsInvalidEntriesAndCopiesKeys(t *testing.T) {
	if _, err := netsec.NewKeyRing(map[int][]byte{0: []byte("key")}, nil); !errors.Is(err, netsec.ErrInvalidKeyID) {
		t.Fatalf("id 0 error = %v, want ErrInvalidKeyID", err)
	}
	if _, err := netsec.NewKeyRing(map[int][]byte{256: []byte("key")}, nil); !errors.Is(err, netsec.ErrInvalidKeyID) {
		t.Fatalf("id 256 error = %v, want ErrInvalidKeyID", err)
	}
	if _, err := netsec.NewKeyRing(map[int][]byte{1: nil}, nil); !errors.Is(err, netsec.ErrEmptyKeyMaterial) {
		t.Fatalf("empty key error = %v, want ErrEmptyKeyMaterial", err)
	}

	source := []byte("secret")
	ring, err := netsec.NewKeyRing(map[int][]byte{1: source}, nil)
	if err != nil {
		t.Fatalf("NewKeyRing: %v", err)
	}
	source[0] = 'X'
	got, generation, ok := ring.Lookup(1)
	if !ok || generation != netsec.KeyGenerationCurrent {
		t.Fatalf("Lookup = %q, %v, %v", got, generation, ok)
	}
	if string(got) != "secret" {
		t.Fatalf("stored key changed with caller slice: %q", got)
	}
}

func TestAdmissionConstructorValidation(t *testing.T) {
	decryptor := &fakeDecryptor{}
	if _, err := netsec.NewAdmission(nil, decryptor); !errors.Is(err, netsec.ErrNilKeyRing) {
		t.Fatalf("nil key ring error = %v, want ErrNilKeyRing", err)
	}
	if _, err := netsec.NewAdmission(testKeyRing(t), nil); !errors.Is(err, netsec.ErrNilDecryptor) {
		t.Fatalf("nil decryptor error = %v, want ErrNilDecryptor", err)
	}
	if _, err := netsec.NewAdmission(testKeyRing(t), decryptor, netsec.WithReplayWindow(-time.Second)); !errors.Is(err, netsec.ErrInvalidReplayWindow) {
		t.Fatalf("negative window error = %v, want ErrInvalidReplayWindow", err)
	}
	if _, err := netsec.NewAdmission(testKeyRing(t), decryptor, netsec.WithClock(nil)); !errors.Is(err, netsec.ErrNilClock) {
		t.Fatalf("nil clock error = %v, want ErrNilClock", err)
	}
}

func TestDecryptorFuncs(t *testing.T) {
	var sequence []string
	decryptor := netsec.DecryptorFuncs{
		VerifyMACFunc: func(scheme netsec.ProtectionScheme, key []byte, payload netsec.EncryptedPayload) error {
			sequence = append(sequence, "mac")
			return nil
		},
		DecryptFunc: func(scheme netsec.ProtectionScheme, key []byte, payload netsec.EncryptedPayload) ([]byte, error) {
			sequence = append(sequence, "decrypt")
			return []byte("001010000000001"), nil
		},
	}
	admission, err := netsec.NewAdmission(testKeyRing(t), decryptor)
	if err != nil {
		t.Fatalf("NewAdmission: %v", err)
	}
	request := encryptedRequest(netsec.SchemeProfileB, currentKeyID, profileOutput(t, netsec.SchemeProfileB, 0x81, "ciphertext"))
	if _, err := admission.Admit(request); err != nil {
		t.Fatalf("Admit: %v", err)
	}
	if got, want := fmt.Sprint(sequence), "[mac decrypt]"; got != want {
		t.Fatalf("sequence = %s, want %s", got, want)
	}
}
