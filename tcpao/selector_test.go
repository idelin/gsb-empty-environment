package tcpao

import (
	"errors"
	"sync"
	"testing"
	"time"
)

var testTime = time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)

func newTestKey(id KeyID, material string) Key {
	key := []byte(material)
	return Key{
		ID:              id,
		SendKey:         key,
		ReceiveKey:      key,
		Algorithm:       HMACSHA1,
		SendValidity:    Validity{NotBefore: testTime.Add(-time.Hour), NotAfter: testTime.Add(time.Hour)},
		ReceiveValidity: Validity{NotBefore: testTime.Add(-time.Hour), NotAfter: testTime.Add(time.Hour)},
	}
}

func signedSegment(t *testing.T, signer Key, keyID, next KeyID, data string) Segment {
	t.Helper()
	payload := []byte(data)
	mac, err := signer.Sign(keyID, next, payload)
	if err != nil {
		t.Fatalf("sign segment: %v", err)
	}
	return Segment{
		KeyID:      keyID,
		RNextKeyID: next,
		MAC:        mac,
		MACData:    payload,
	}
}

func TestNormalVerifiedSwitch(t *testing.T) {
	key1 := newTestKey(1, "current-key-material")
	key2 := newTestKey(2, "next-key-material")

	selector, err := NewSelector(key1.ID, key1, key2)
	if err != nil {
		t.Fatalf("new selector: %v", err)
	}

	seg := signedSegment(t, key1, key1.ID, key2.ID, "BGP UPDATE")
	result, err := selector.Receive(seg, testTime)
	if err != nil {
		t.Fatalf("receive verified switch: %v", err)
	}
	if !result.Accepted {
		t.Fatal("verified segment was not accepted")
	}
	if !result.Switched {
		t.Fatal("verified RNextKeyID did not switch the send key")
	}
	if result.SendKeyID != key2.ID {
		t.Fatalf("send key = %d, want %d", result.SendKeyID, key2.ID)
	}

	sendKey, err := selector.SendKey(testTime)
	if err != nil {
		t.Fatalf("get switched send key: %v", err)
	}
	if sendKey.ID != key2.ID {
		t.Fatalf("SendKey returned key %d, want %d", sendKey.ID, key2.ID)
	}
}

func TestForgedRNextKeyIDIsRejected(t *testing.T) {
	key1 := newTestKey(1, "current-key-material")
	key2 := newTestKey(2, "attacker-chosen-key")

	selector, err := NewSelector(key1.ID, key1, key2)
	if err != nil {
		t.Fatalf("new selector: %v", err)
	}

	// The MAC is valid for a segment whose RNextKeyID is NoKey. Flipping the
	// option changes the authenticated input and must invalidate the segment.
	seg := signedSegment(t, key1, key1.ID, NoKey, "BGP UPDATE")
	seg.RNextKeyID = key2.ID

	before := selector.SendKeyID()
	result, err := selector.Receive(seg, testTime)
	if !errors.Is(err, ErrAuthenticationFailed) {
		t.Fatalf("receive error = %v, want %v", err, ErrAuthenticationFailed)
	}
	if result.Accepted {
		t.Fatal("forged segment was accepted")
	}
	if result.Switched {
		t.Fatal("forged RNextKeyID switched the send key")
	}
	if selector.SendKeyID() != before {
		t.Fatalf("send key changed from %d to %d", before, selector.SendKeyID())
	}
}

func TestGarbageMACIsRejectedWithoutStateChange(t *testing.T) {
	key1 := newTestKey(1, "current-key-material")
	key2 := newTestKey(2, "next-key-material")

	selector, err := NewSelector(key1.ID, key1, key2)
	if err != nil {
		t.Fatalf("new selector: %v", err)
	}

	seg := Segment{
		KeyID:      key1.ID,
		RNextKeyID: key2.ID,
		MAC:        make([]byte, HMACSHA1.MACSize()),
		MACData:    []byte("BGP UPDATE"),
	}
	result, err := selector.Receive(seg, testTime)
	if !errors.Is(err, ErrAuthenticationFailed) {
		t.Fatalf("receive error = %v, want %v", err, ErrAuthenticationFailed)
	}
	if result.Accepted || result.Switched {
		t.Fatal("segment with an invalid MAC affected selector state")
	}
	if selector.SendKeyID() != key1.ID {
		t.Fatalf("send key = %d, want %d", selector.SendKeyID(), key1.ID)
	}
}

func TestVerifiedRNextKeyIDMustBeRegistered(t *testing.T) {
	key1 := newTestKey(1, "current-key-material")
	key2 := newTestKey(2, "unused-key-material")

	selector, err := NewSelector(key1.ID, key1)
	if err != nil {
		t.Fatalf("new selector: %v", err)
	}

	// The MAC proves this request, but the requested key was never registered.
	seg := signedSegment(t, key1, key1.ID, key2.ID, "BGP UPDATE")
	result, err := selector.Receive(seg, testTime)
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	if !result.Accepted {
		t.Fatal("MAC-verified segment was not accepted")
	}
	if result.Switched {
		t.Fatal("unregistered RNextKeyID changed the send key")
	}
	if !errors.Is(result.RNextError, ErrNextKeyUnknown) {
		t.Fatalf("RNextError = %v, want %v", result.RNextError, ErrNextKeyUnknown)
	}
	if selector.SendKeyID() != key1.ID {
		t.Fatalf("send key = %d, want %d", selector.SendKeyID(), key1.ID)
	}
}

func TestRNextKeyIDMustBeSendValid(t *testing.T) {
	tests := []struct {
		name      string
		validity  Validity
		at        time.Time
		wantError error
	}{
		{
			name:      "expired",
			validity:  Validity{NotBefore: testTime.Add(-time.Hour), NotAfter: testTime},
			at:        testTime,
			wantError: ErrNextKeySendExpired,
		},
		{
			name:      "not yet valid",
			validity:  Validity{NotBefore: testTime.Add(time.Second), NotAfter: testTime.Add(time.Hour)},
			at:        testTime,
			wantError: ErrNextKeyNotYetValid,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key1 := newTestKey(1, "current-key-material")
			next := newTestKey(2, "future-key-material")
			next.SendValidity = tt.validity

			selector, err := NewSelector(key1.ID, key1, next)
			if err != nil {
				t.Fatalf("new selector: %v", err)
			}

			seg := signedSegment(t, key1, key1.ID, next.ID, "BGP UPDATE")
			result, err := selector.Receive(seg, tt.at)
			if err != nil {
				t.Fatalf("receive: %v", err)
			}
			if !result.Accepted {
				t.Fatal("MAC-verified segment was not accepted")
			}
			if result.Switched {
				t.Fatal("send key switched to a key outside send validity")
			}
			if !errors.Is(result.RNextError, tt.wantError) {
				t.Fatalf("RNextError = %v, want %v", result.RNextError, tt.wantError)
			}
			if selector.SendKeyID() != key1.ID {
				t.Fatalf("send key = %d, want %d", selector.SendKeyID(), key1.ID)
			}
		})
	}
}

func TestReplayedRetiredReceiveKeyIsRejected(t *testing.T) {
	cutoff := testTime.Add(30 * time.Minute)
	key1 := newTestKey(1, "retired-key-material")
	key1.ReceiveValidity.NotAfter = cutoff
	key2 := newTestKey(2, "next-key-material")

	selector, err := NewSelector(key1.ID, key1, key2)
	if err != nil {
		t.Fatalf("new selector: %v", err)
	}

	old := signedSegment(t, key1, key1.ID, NoKey, "old BGP KEEPALIVE")
	if result, err := selector.Receive(old, cutoff.Add(-time.Minute)); err != nil || !result.Accepted {
		t.Fatalf("pre-expiry receive = (%+v, %v), want accepted", result, err)
	}

	// Replay the exact old-key segment at and after the receive cutoff.
	for _, at := range []time.Time{cutoff, cutoff.Add(time.Minute)} {
		result, err := selector.Receive(old, at)
		if !errors.Is(err, ErrReceiveKeyExpired) {
			t.Fatalf("replay at %s error = %v, want %v", at, err, ErrReceiveKeyExpired)
		}
		if result.Accepted {
			t.Fatalf("replayed retired-key segment was accepted at %s", at)
		}
	}

	// A fresh, correctly signed request through the retired key cannot trigger
	// a send-key switch either.
	request := signedSegment(t, key1, key1.ID, key2.ID, "retired key switch request")
	result, err := selector.Receive(request, cutoff)
	if !errors.Is(err, ErrReceiveKeyExpired) {
		t.Fatalf("retired key request error = %v, want %v", err, ErrReceiveKeyExpired)
	}
	if result.Switched {
		t.Fatal("retired receive key triggered a send-key switch")
	}
	if selector.SendKeyID() != key1.ID {
		t.Fatalf("send key = %d, want %d", selector.SendKeyID(), key1.ID)
	}
}

func TestReceiveKeyOutsideValidityIsRejected(t *testing.T) {
	key1 := newTestKey(1, "key-material")
	key1.ReceiveValidity.NotBefore = testTime.Add(time.Second)

	selector, err := NewSelector(key1.ID, key1)
	if err != nil {
		t.Fatalf("new selector: %v", err)
	}

	seg := signedSegment(t, key1, key1.ID, NoKey, "BGP UPDATE")
	result, err := selector.Receive(seg, testTime)
	if !errors.Is(err, ErrReceiveKeyNotYetValid) {
		t.Fatalf("receive error = %v, want %v", err, ErrReceiveKeyNotYetValid)
	}
	if result.Accepted {
		t.Fatal("segment before receive validity was accepted")
	}
}

func TestSendKeyExpiryIsCheckedWhenSending(t *testing.T) {
	cutoff := testTime.Add(time.Hour)
	key1 := newTestKey(1, "current-key-material")
	key2 := newTestKey(2, "next-key-material")
	key2.SendValidity.NotAfter = cutoff

	selector, err := NewSelector(key1.ID, key1, key2)
	if err != nil {
		t.Fatalf("new selector: %v", err)
	}

	seg := signedSegment(t, key1, key1.ID, key2.ID, "BGP UPDATE")
	if result, err := selector.Receive(seg, testTime); err != nil || !result.Switched {
		t.Fatalf("switch = (%+v, %v), want switched", result, err)
	}

	if _, err := selector.SendKey(cutoff); !errors.Is(err, ErrSendKeyExpired) {
		t.Fatalf("SendKey at cutoff error = %v, want %v", err, ErrSendKeyExpired)
	}
}

func TestUnknownKeyIsRejectedBeforeMAC(t *testing.T) {
	key1 := newTestKey(1, "known-key-material")
	selector, err := NewSelector(key1.ID, key1)
	if err != nil {
		t.Fatalf("new selector: %v", err)
	}

	seg := Segment{
		KeyID:      KeyID(9),
		RNextKeyID: key1.ID,
		MAC:        []byte("not-a-real-mac"),
		MACData:    []byte("BGP UPDATE"),
	}
	result, err := selector.Receive(seg, testTime)
	if !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("receive error = %v, want %v", err, ErrUnknownKey)
	}
	if result.Accepted {
		t.Fatal("unknown KeyID was accepted")
	}
}

func TestRegisterRejectsDuplicateAndInvalidKeys(t *testing.T) {
	key1 := newTestKey(1, "key-material")
	selector, err := NewSelector(key1.ID, key1)
	if err != nil {
		t.Fatalf("new selector: %v", err)
	}

	if err := selector.Register(key1); !errors.Is(err, ErrDuplicateKey) {
		t.Fatalf("duplicate Register error = %v, want %v", err, ErrDuplicateKey)
	}

	badAlgorithm := newTestKey(2, "key-material")
	badAlgorithm.Algorithm = Algorithm("md5")
	if err := selector.Register(badAlgorithm); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("unsupported algorithm error = %v, want %v", err, ErrInvalidKey)
	}
}

func TestConcurrentReceiveDoesNotCorruptSelection(t *testing.T) {
	key1 := newTestKey(1, "current-key-material")
	key2 := newTestKey(2, "next-key-material")
	selector, err := NewSelector(key1.ID, key1, key2)
	if err != nil {
		t.Fatalf("new selector: %v", err)
	}

	seg := signedSegment(t, key1, key1.ID, key2.ID, "BGP UPDATE")
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := selector.Receive(seg, testTime); err != nil {
				t.Errorf("receive: %v", err)
			}
		}()
	}
	wg.Wait()

	if selector.SendKeyID() != key2.ID {
		t.Fatalf("send key = %d, want %d", selector.SendKeyID(), key2.ID)
	}
}
