package integrity

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

const (
	testPackage = "com.example.app"
	testDigest  = "AA11BB22CC33DD44EE55FF66AA11BB22CC33DD44EE55FF66AA11BB22CC33DD44"
)

var testNow = time.UnixMilli(1_700_000_000_000)

func newTestVerifier() *Verifier {
	return NewVerifier(testPackage, []string{testDigest},
		WithClock(func() time.Time { return testNow }))
}

func verdictJSON(t *testing.T, body []byte, ts time.Time, mutate func(*Verdict)) []byte {
	t.Helper()
	var v Verdict
	v.RequestDetails.RequestPackageName = testPackage
	v.RequestDetails.RequestHash = RequestHash(body)
	v.RequestDetails.TimestampMillis = ts.UnixMilli()
	v.AppIntegrity.AppRecognitionVerdict = "PLAY_RECOGNIZED"
	v.AppIntegrity.PackageName = testPackage
	v.AppIntegrity.CertificateSha256Digest = []string{"UNRELATED", testDigest}
	v.DeviceIntegrity.DeviceRecognitionVerdict = []string{"MEETS_DEVICE_INTEGRITY"}
	if mutate != nil {
		mutate(&v)
	}
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestVerifyValidVerdict(t *testing.T) {
	v := newTestVerifier()
	body := []byte(`{"op":"transfer","amount":100}`)
	if err := v.Verify(body, verdictJSON(t, body, testNow, nil)); err != nil {
		t.Fatalf("valid verdict rejected: %v", err)
	}
}

func TestForwardedTokenRejected(t *testing.T) {
	v := newTestVerifier()
	// Token minted for an unrelated, harmless request.
	otherBody := []byte(`{"op":"getBalance"}`)
	token := verdictJSON(t, otherBody, testNow, nil)
	// Attacker forwards it to the sensitive endpoint.
	sensitiveBody := []byte(`{"op":"transfer","amount":100}`)
	if err := v.Verify(sensitiveBody, token); !errors.Is(err, ErrRequestHashMismatch) {
		t.Fatalf("forwarded token: got %v, want ErrRequestHashMismatch", err)
	}
}

func TestReplayRejected(t *testing.T) {
	v := newTestVerifier()
	body := []byte(`{"op":"transfer","amount":100}`)
	token := verdictJSON(t, body, testNow, nil)
	if err := v.Verify(body, token); err != nil {
		t.Fatalf("first use rejected: %v", err)
	}
	if err := v.Verify(body, token); !errors.Is(err, ErrReplay) {
		t.Fatalf("replay: got %v, want ErrReplay", err)
	}
}

func TestSameHashDifferentTimestampAccepted(t *testing.T) {
	v := newTestVerifier()
	body := []byte(`{"op":"transfer","amount":100}`)
	if err := v.Verify(body, verdictJSON(t, body, testNow, nil)); err != nil {
		t.Fatalf("first use rejected: %v", err)
	}
	// Same requestHash but a new timestamp is a distinct combination.
	if err := v.Verify(body, verdictJSON(t, body, testNow.Add(time.Second), nil)); err != nil {
		t.Fatalf("distinct (hash, timestamp) combo rejected: %v", err)
	}
}

func TestTimestampBoundary(t *testing.T) {
	cases := []struct {
		name    string
		offset  time.Duration
		wantErr error
	}{
		{"exactly 60s past", -60 * time.Second, nil},
		{"exactly 60s future", 60 * time.Second, nil},
		{"61s past", -61 * time.Second, ErrTimestampSkew},
		{"61s future", 61 * time.Second, ErrTimestampSkew},
		{"60s plus 1ms", -(60*time.Second + time.Millisecond), ErrTimestampSkew},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := newTestVerifier()
			body := []byte(`{"op":"transfer"}`)
			token := verdictJSON(t, body, testNow.Add(tc.offset), nil)
			if err := v.Verify(body, token); !errors.Is(err, tc.wantErr) {
				t.Fatalf("got %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestUnregisteredCertDigestRejected(t *testing.T) {
	v := newTestVerifier()
	body := []byte(`{"op":"transfer"}`)
	token := verdictJSON(t, body, testNow, func(vd *Verdict) {
		vd.AppIntegrity.CertificateSha256Digest = []string{"DEADBEEF"}
	})
	if err := v.Verify(body, token); !errors.Is(err, ErrCertDigest) {
		t.Fatalf("wrong digest: got %v, want ErrCertDigest", err)
	}
}

func TestPackageMismatchRejected(t *testing.T) {
	body := []byte(`{"op":"transfer"}`)
	cases := []struct {
		name   string
		mutate func(*Verdict)
	}{
		{"requestPackageName", func(vd *Verdict) { vd.RequestDetails.RequestPackageName = "com.evil.app" }},
		{"appIntegrity packageName", func(vd *Verdict) { vd.AppIntegrity.PackageName = "com.evil.app" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := newTestVerifier()
			if err := v.Verify(body, verdictJSON(t, body, testNow, tc.mutate)); !errors.Is(err, ErrPackageMismatch) {
				t.Fatalf("got %v, want ErrPackageMismatch", err)
			}
		})
	}
}

func TestAppNotRecognizedRejected(t *testing.T) {
	v := newTestVerifier()
	body := []byte(`{"op":"transfer"}`)
	token := verdictJSON(t, body, testNow, func(vd *Verdict) {
		vd.AppIntegrity.AppRecognitionVerdict = "UNRECOGNIZED_VERSION"
	})
	if err := v.Verify(body, token); !errors.Is(err, ErrAppNotRecognized) {
		t.Fatalf("got %v, want ErrAppNotRecognized", err)
	}
}

func TestDeviceIntegrityMissingRejected(t *testing.T) {
	v := newTestVerifier()
	body := []byte(`{"op":"transfer"}`)
	token := verdictJSON(t, body, testNow, func(vd *Verdict) {
		vd.DeviceIntegrity.DeviceRecognitionVerdict = []string{"MEETS_BASIC_INTEGRITY"}
	})
	if err := v.Verify(body, token); !errors.Is(err, ErrDeviceIntegrity) {
		t.Fatalf("got %v, want ErrDeviceIntegrity", err)
	}
}

func TestMalformedVerdictRejected(t *testing.T) {
	v := newTestVerifier()
	if err := v.Verify([]byte(`{}`), []byte(`not json`)); err == nil {
		t.Fatal("malformed verdict accepted")
	}
}
