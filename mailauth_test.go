package mailauth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"net"
	"testing"
	"time"

	"prescreen.local/ps-517b39ba66/dkim"
	"prescreen.local/ps-517b39ba66/dmarc"
	"prescreen.local/ps-517b39ba66/spf"
)

type fakeResolver struct {
	txt map[string][]string
	ips map[string][]net.IP
}

func (f *fakeResolver) LookupTXT(_ context.Context, name string) ([]string, error) {
	return f.txt[name], nil
}

func (f *fakeResolver) LookupIP(_ context.Context, _, name string) ([]net.IP, error) {
	return f.ips[name], nil
}

func (f *fakeResolver) LookupMX(context.Context, string) ([]*net.MX, error) {
	return nil, nil
}

func (f *fakeResolver) LookupAddr(context.Context, string) ([]string, error) {
	return nil, nil
}

func TestJointVerificationPass(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	message := []byte("From: Alice <alice@example.com>\r\n" +
		"To: Bob <bob@example.net>\r\n" +
		"Subject: Joint verification\r\n" +
		"\r\n" +
		"hello\r\n")
	signed, err := dkim.Sign(message, dkim.SignOptions{
		Domain:           "example.com",
		Selector:         "sel",
		PrivateKey:       key,
		Headers:          []string{"from", "to", "subject"},
		Canonicalization: "relaxed/relaxed",
		Now:              time.Unix(1700000000, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	resolver := &fakeResolver{
		txt: map[string][]string{
			"example.com":                {"v=spf1 ip4:198.51.100.0/24 -all"},
			"sel._domainkey.example.com": {"v=DKIM1; k=rsa; p=" + base64.StdEncoding.EncodeToString(publicDER)},
			"_dmarc.example.com":         {"v=DMARC1; p=reject; adkim=r; aspf=r"},
			"_dmarc.sub.example.com":     {"v=DMARC1; p=quarantine"},
			"example.net":                {"v=spf1 -all"},
			"sel._domainkey.example.net": {"v=DKIM1; p="},
			"_dmarc.example.net":         {"v=DMARC1; p=none"},
		},
		ips: map[string][]net.IP{
			"example.com": {net.ParseIP("198.51.100.10")},
		},
	}
	report, err := Verify(context.Background(), signed, Options{
		IP:       net.ParseIP("198.51.100.10"),
		MailFrom: "alice@example.com",
		Helo:     "mx.example.com",
		Resolver: resolver,
		Now:      time.Unix(1700000001, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.SPF.Result != spf.Pass {
		t.Fatalf("SPF = %s", report.SPF.Result)
	}
	if len(report.DKIM) != 1 || report.DKIM[0].Status != dkim.Pass {
		t.Fatalf("DKIM = %+v", report.DKIM)
	}
	if report.DMARC.Status != dmarc.Pass || report.DMARC.Disposition != dmarc.DispositionNone {
		t.Fatalf("DMARC = %+v", report.DMARC)
	}
	if !report.DMARC.SPFAligned || !report.DMARC.DKIMAligned {
		t.Fatalf("DMARC alignment = SPF:%v DKIM:%v, want both", report.DMARC.SPFAligned, report.DMARC.DKIMAligned)
	}
}
