package ocspcheck

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"math/big"
	"testing"
	"time"
)

var fixedNow = time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)

func newConnectionState(p *testPKI, ocspDER []byte) tls.ConnectionState {
	return tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{p.leafCert, p.caCert},
		VerifiedChains:   [][]*x509.Certificate{{p.leafCert, p.caCert}},
		OCSPResponse:     ocspDER,
	}
}

func TestOCSPCheck(t *testing.T) {
	p := newTestPKI(fixedNow)

	type testCase struct {
		name      string
		config    Config
		input     func() responseInput
		noStaple  bool
		wantErrIs error
		wantErr   bool
	}

	// Fresh, CA-signed response (byName) for the correct leaf.
	caSigned := func() responseInput {
		in := defaultResponseInput(p, p.caCert, p.caKey)
		in.embedCerts = nil
		return in
	}

	// Fresh, properly delegated response (byKey id).
	delegated := func() responseInput {
		in := defaultResponseInput(p, p.delegatedCert, p.delegatedKey)
		in.useByKey = true
		return in
	}

	tests := []testCase{
		{
			name:   "valid CA signed response",
			config: Config{},
			input:  caSigned,
		},
		{
			name:   "valid delegated response with OCSPSigning",
			config: Config{},
			input:  delegated,
		},
		{
			name:   "valid delegated byName responder id",
			config: Config{},
			input: func() responseInput {
				in := delegated()
				in.useByKey = false
				return in
			},
		},

		// --- Delegation over-privilege / unauthorized signer cases ---
		{
			name:   "delegated responder without OCSPSigning EKU rejected",
			config: Config{},
			input: func() responseInput {
				in := defaultResponseInput(p, p.noEKUCert, p.noEKUKey)
				in.useByKey = true
				return in
			},
			wantErr: true,
		},
		{
			name:   "delegated responder that is itself a CA rejected",
			config: Config{},
			input: func() responseInput {
				in := defaultResponseInput(p, p.caDelegationCert, p.caDelegationKey)
				in.useByKey = true
				return in
			},
			wantErr: true,
		},
		{
			name:   "delegated cert not embedded rejected",
			config: Config{},
			input: func() responseInput {
				in := delegated()
				in.embedCerts = nil
				return in
			},
			wantErr: true,
		},
		{
			name:   "corrupted signature rejected",
			config: Config{},
			input: func() responseInput {
				in := caSigned()
				in.corruptSig = true
				return in
			},
			wantErr: true,
		},

		// --- Time-boundary cases ---
		{
			name:   "thisUpdate in the future rejected",
			config: Config{Skew: time.Minute},
			input: func() responseInput {
				in := caSigned()
				in.thisUpdate = fixedNow.Add(5 * time.Minute)
				return in
			},
			wantErr: true,
		},
		{
			name:   "future thisUpdate within skew accepted",
			config: Config{Skew: 2 * time.Minute},
			input: func() responseInput {
				in := caSigned()
				in.thisUpdate = fixedNow.Add(90 * time.Second)
				return in
			},
		},
		{
			name:   "expired nextUpdate rejected",
			config: Config{Skew: time.Minute},
			input: func() responseInput {
				in := caSigned()
				in.nextUpdate = fixedNow.Add(-5 * time.Minute)
				return in
			},
			wantErr: true,
		},
		{
			name:   "expired nextUpdate within skew accepted",
			config: Config{Skew: 10 * time.Minute},
			input: func() responseInput {
				in := caSigned()
				in.nextUpdate = fixedNow.Add(-5 * time.Minute)
				return in
			},
		},
		{
			name:   "response without nextUpdate within MaxAge accepted",
			config: Config{MaxAge: time.Hour},
			input: func() responseInput {
				in := caSigned()
				in.omitNext = true
				in.thisUpdate = fixedNow.Add(-30 * time.Minute)
				return in
			},
		},
		{
			name:   "response without nextUpdate older than MaxAge rejected",
			config: Config{MaxAge: time.Hour},
			input: func() responseInput {
				in := caSigned()
				in.omitNext = true
				in.thisUpdate = fixedNow.Add(-2 * time.Hour)
				return in
			},
			wantErr: true,
		},
		{
			name:   "response without nextUpdate rejected when policy requires it",
			config: Config{MaxAge: -1},
			input: func() responseInput {
				in := caSigned()
				in.omitNext = true
				return in
			},
			wantErr: true,
		},

		// --- CertID mismatch cases ---
		{
			name:   "wrong serial number rejected",
			config: Config{},
			input: func() responseInput {
				in := caSigned()
				in.wrongSerial = big.NewInt(999999)
				return in
			},
			wantErr: true,
		},
		{
			name:   "serial of another valid cert rejected",
			config: Config{},
			input: func() responseInput {
				in := caSigned()
				in.wrongSerial = p.otherCert.SerialNumber
				return in
			},
			wantErr: true,
		},
		{
			name:   "wrong issuer name hash rejected",
			config: Config{},
			input: func() responseInput {
				in := caSigned()
				in.wrongNameHash = []byte{0xde, 0xad, 0xbe, 0xef}
				return in
			},
			wantErr: true,
		},
		{
			name:   "wrong issuer key hash rejected",
			config: Config{},
			input: func() responseInput {
				in := caSigned()
				in.wrongKeyHash = []byte{0xca, 0xfe, 0xba, 0xbe}
				return in
			},
			wantErr: true,
		},

		// --- Certificate status cases ---
		{
			name:      "revoked status rejected",
			config:    Config{},
			input:     func() responseInput { in := caSigned(); in.status = statusRevoked; return in },
			wantErrIs: ErrRevoked,
			wantErr:   true,
		},
		{
			name:      "unknown status fails closed by default",
			config:    Config{},
			input:     func() responseInput { in := caSigned(); in.status = statusUnknown; return in },
			wantErrIs: ErrUnknownStatus,
			wantErr:   true,
		},
		{
			name:   "unknown status allowed by policy",
			config: Config{UnknownPolicy: UnknownAllowed},
			input:  func() responseInput { in := caSigned(); in.status = statusUnknown; return in },
		},

		// --- Missing staple policy ---
		{
			name:      "missing staple fails closed by default",
			config:    Config{},
			noStaple:  true,
			wantErrIs: ErrMissingResponse,
			wantErr:   true,
		},
		{
			name:     "missing staple allowed by policy",
			config:   Config{MissingPolicy: MissingAllowed},
			noStaple: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.config.withClock(func() time.Time { return fixedNow })
			var der []byte
			if !tc.noStaple {
				der = buildOCSPResponse(tc.input())
			}
			err := cfg.checkAt(newConnectionState(p, der), fixedNow)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("expected success, got error: %v", err)
			}
			if tc.wantErrIs != nil && !errors.Is(err, tc.wantErrIs) {
				t.Fatalf("expected errors.Is(err, %v), got %v", tc.wantErrIs, err)
			}
		})
	}
}
