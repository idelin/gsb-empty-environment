package netsec

import (
	"errors"
	"testing"
)

var oneCert = [][]byte{{0x30, 0x82, 0x01, 0x00}}

var errPathRejected = errors.New("path rejected")

func TestValidateCertificateList(t *testing.T) {
	rejectPath := func([][]byte) error { return errPathRejected }
	acceptPath := func([][]byte) error { return nil }

	tests := []struct {
		name    string
		certs   [][]byte
		policy  ListPolicy
		wantErr error
	}{
		{
			name:   "empty list, certificate required",
			certs:  nil,
			policy: ListPolicy{CertificateRequested: true, RequireCertificate: true},
			// 要求客户端证书时对端发送空列表：握手失败。
			wantErr: ErrCertificateRequired,
		},
		{
			name:    "empty list, not required, request sent",
			certs:   [][]byte{},
			policy:  ListPolicy{CertificateRequested: true},
			wantErr: ErrEmptyCertificateList,
		},
		{
			name:   "empty list, not required, no request sent",
			certs:  nil,
			policy: ListPolicy{},
			// 未发出证书请求时允许空列表，握手继续。
			wantErr: nil,
		},
		{
			name:    "one certificate, acceptable path",
			certs:   oneCert,
			policy:  ListPolicy{CertificateRequested: true, RequireCertificate: true, VerifyPath: acceptPath},
			wantErr: nil,
		},
		{
			name:    "one certificate, unacceptable path",
			certs:   oneCert,
			policy:  ListPolicy{CertificateRequested: true, RequireCertificate: true, VerifyPath: rejectPath},
			wantErr: errPathRejected,
		},
		{
			name:    "one certificate, no verifier",
			certs:   oneCert,
			policy:  ListPolicy{CertificateRequested: true},
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCertificateList(tt.certs, tt.policy)
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("ValidateCertificateList() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("ValidateCertificateList() = nil, want %v", tt.wantErr)
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ValidateCertificateList() = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestClientCertificateList(t *testing.T) {
	t.Run("no request, no message", func(t *testing.T) {
		if got := ClientCertificateList(false, oneCert); got != nil {
			t.Fatalf("ClientCertificateList(false, certs) = %v, want nil", got)
		}
	})

	t.Run("requested, no certificate, send empty list", func(t *testing.T) {
		// 要求客户端证书但无证书可用：仍须回复空列表的 Certificate 消息。
		got := ClientCertificateList(true, nil)
		if got == nil {
			t.Fatal("ClientCertificateList(true, nil) = nil, want non-nil empty list")
		}
		if len(got) != 0 {
			t.Fatalf("ClientCertificateList(true, nil) has %d entries, want 0", len(got))
		}
	})

	t.Run("requested, certificate available", func(t *testing.T) {
		got := ClientCertificateList(true, oneCert)
		if len(got) != 1 {
			t.Fatalf("ClientCertificateList(true, oneCert) has %d entries, want 1", len(got))
		}
	})
}
