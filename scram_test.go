package netsec

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"testing"
)

func TestParseGS2Header(t *testing.T) {
	tests := []struct {
		name       string
		message    string
		flag       string
		binding    string
		authzID    string
		headerOnly string
	}{
		{
			name:       "no binding",
			message:    "n,,n=user,r=nonce",
			flag:       "n",
			headerOnly: "n,,",
		},
		{
			name:       "supported but no server binding",
			message:    "y,,n=user,r=nonce",
			flag:       "y",
			headerOnly: "y,,",
		},
		{
			name:       "required server endpoint",
			message:    "p=tls-server-end-point,,n=user,r=nonce",
			flag:       "p",
			binding:    ChannelBindingTLSServerEndPoint,
			headerOnly: "p=tls-server-end-point,,",
		},
		{
			name:       "authzid",
			message:    "n,a=admin@example.org,n=user,r=nonce",
			flag:       "n",
			authzID:    "admin@example.org",
			headerOnly: "n,a=admin@example.org,",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			header, err := ParseGS2Header(test.message)
			if err != nil {
				t.Fatalf("ParseGS2Header() error = %v", err)
			}
			if header.Flag != test.flag {
				t.Fatalf("Flag = %q, want %q", header.Flag, test.flag)
			}
			if header.ChannelBindingType != test.binding {
				t.Fatalf("ChannelBindingType = %q, want %q", header.ChannelBindingType, test.binding)
			}
			if header.AuthzID != test.authzID {
				t.Fatalf("AuthzID = %q, want %q", header.AuthzID, test.authzID)
			}
			if header.Raw != test.headerOnly {
				t.Fatalf("Raw = %q, want %q", header.Raw, test.headerOnly)
			}
		})
	}
}

func TestEvaluateFirstMessageRejectsPlusDowngrade(t *testing.T) {
	_, err := EvaluateFirstMessage(
		MechanismSCRAMSHA256,
		"y,,n=user,r=nonce",
		[]string{MechanismSCRAMSHA256, MechanismSCRAMSHA256PLUS},
	)
	if !errors.Is(err, ErrChannelBindingDowngrade) {
		t.Fatalf("error = %v, want ErrChannelBindingDowngrade", err)
	}
}

func TestEvaluateFirstMessageMechanismGS2Mismatch(t *testing.T) {
	tests := []struct {
		name      string
		mechanism string
		gs2       string
	}{
		{
			name:      "PLUS with n",
			mechanism: MechanismSCRAMSHA256PLUS,
			gs2:       "n,,n=user,r=nonce",
		},
		{
			name:      "PLUS with y",
			mechanism: MechanismSCRAMSHA256PLUS,
			gs2:       "y,,n=user,r=nonce",
		},
		{
			name:      "non-PLUS with p",
			mechanism: MechanismSCRAMSHA256,
			gs2:       "p=tls-exporter,,n=user,r=nonce",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := EvaluateFirstMessage(test.mechanism, test.gs2, []string{MechanismSCRAMSHA256, MechanismSCRAMSHA256PLUS})
			if !errors.Is(err, ErrMechanismMismatch) {
				t.Fatalf("error = %v, want ErrMechanismMismatch", err)
			}
		})
	}
}

func TestEvaluateFirstMessageRejectsUnknownBindingType(t *testing.T) {
	_, err := EvaluateFirstMessage(
		MechanismSCRAMSHA256PLUS,
		"p=tls-unique,,n=user,r=nonce",
		[]string{MechanismSCRAMSHA256, MechanismSCRAMSHA256PLUS},
	)
	if !errors.Is(err, ErrUnsupportedChannelBindingType) {
		t.Fatalf("error = %v, want ErrUnsupportedChannelBindingType", err)
	}
}

func TestEvaluateFirstMessageAllowsYWithoutAdvertisedPLUS(t *testing.T) {
	binding, err := EvaluateFirstMessage(
		MechanismSCRAMSHA256,
		"y,,n=user,r=nonce",
		[]string{MechanismSCRAMSHA256},
	)
	if err != nil {
		t.Fatalf("EvaluateFirstMessage() error = %v", err)
	}
	if binding.ChannelBindingType != "" {
		t.Fatalf("ChannelBindingType = %q, want empty", binding.ChannelBindingType)
	}
}

func TestVerifyFinalMessage(t *testing.T) {
	header := "p=tls-exporter,,"
	data := []byte("exporter-output")
	want := base64.StdEncoding.EncodeToString(append([]byte(header), data...))
	unpadded := base64.RawStdEncoding.EncodeToString(append([]byte(header), data...))

	if err := VerifyFinalMessage("c="+want+",r=nonce,p=proof", header, data); err != nil {
		t.Fatalf("VerifyFinalMessage() error = %v", err)
	}
	if err := VerifyFinalMessage("c="+unpadded+",r=nonce,p=proof", header, data); err != nil {
		t.Fatalf("VerifyFinalMessage(unpadded) error = %v", err)
	}
	if err := VerifyFinalMessage("c="+want+",r=nonce,p=proof", header, []byte("wrong-output")); !errors.Is(err, ErrChannelBindingMismatch) {
		t.Fatalf("error = %v, want ErrChannelBindingMismatch", err)
	}
}

func TestVerifyFinalMessageWithAuthzID(t *testing.T) {
	header := "n,a=admin@example.org,"
	want := base64.StdEncoding.EncodeToString([]byte(header))
	if err := VerifyFinalMessage("c="+want+",r=nonce,p=proof", header, nil); err != nil {
		t.Fatalf("VerifyFinalMessage() error = %v", err)
	}
}

func TestVerifyFinalMessageRejectsMalformedC(t *testing.T) {
	tests := []string{
		"r=nonce,p=proof",
		"c=not-base64!,r=nonce,p=proof",
		"c=biws,c=biws,r=nonce,p=proof",
	}
	for _, message := range tests {
		if err := VerifyFinalMessage(message, "n,,", nil); !errors.Is(err, ErrMalformedFinalMessage) {
			t.Fatalf("VerifyFinalMessage(%q) error = %v, want ErrMalformedFinalMessage", message, err)
		}
	}
}

func TestTLSServerEndPointBindingUpgradesSHA1(t *testing.T) {
	cert := &x509.Certificate{
		Raw:                []byte("sha1-signed certificate DER"),
		SignatureAlgorithm: x509.SHA1WithRSA,
	}
	if got := CertificateHashAlgorithm(cert); got.String() != "SHA-256" {
		t.Fatalf("CertificateHashAlgorithm() = %v, want SHA-256", got)
	}

	data, err := TLSServerEndPointBindingData(cert)
	if err != nil {
		t.Fatalf("TLSServerEndPointBindingData() error = %v", err)
	}
	want := sha256.Sum256(cert.Raw)
	if string(data) != string(want[:]) {
		t.Fatalf("hash = %x, want %x", data, want)
	}
}

func TestTLSServerEndPointBindingUpgradesMD5(t *testing.T) {
	cert := &x509.Certificate{
		Raw:                []byte("md5-signed certificate DER"),
		SignatureAlgorithm: x509.MD5WithRSA,
	}
	if got := CertificateHashAlgorithm(cert); got.String() != "SHA-256" {
		t.Fatalf("CertificateHashAlgorithm() = %v, want SHA-256", got)
	}
	data, err := CertificateHash(cert)
	if err != nil {
		t.Fatalf("CertificateHash() error = %v", err)
	}
	want := sha256.Sum256(cert.Raw)
	if string(data) != string(want[:]) {
		t.Fatalf("hash = %x, want %x", data, want)
	}
}

func TestTLSServerEndPointSHA384UsesSHA384(t *testing.T) {
	cert := &x509.Certificate{
		Raw:                []byte("sha384-signed certificate DER"),
		SignatureAlgorithm: x509.SHA384WithRSA,
	}
	if got := CertificateHashAlgorithm(cert); got.String() != "SHA-384" {
		t.Fatalf("CertificateHashAlgorithm() = %v, want SHA-384", got)
	}
}
