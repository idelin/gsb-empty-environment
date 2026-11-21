package admission

import "testing"

func TestCertificateRoundTrip(t *testing.T) {
	m := conditionalAbstentionModel()
	a := mustCompile(t, m)
	res, err := a.Enumerate()
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	cert, err := a.Certificate(res)
	if err != nil {
		t.Fatalf("Certificate: %v", err)
	}
	if err := cert.Verify(m); err != nil {
		t.Fatalf("Verify on genuine certificate: %v", err)
	}
	if cert.PolicyCount != res.PolicyCount || cert.RecordsRoot == "" || cert.ModelDigest == "" {
		t.Fatal("certificate missing digest material")
	}
}

func TestCertificateDetectsTamperedBound(t *testing.T) {
	m := normalizationModel()
	a := mustCompile(t, m)
	res, err := a.Enumerate()
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	cert, err := a.Certificate(res)
	if err != nil {
		t.Fatalf("Certificate: %v", err)
	}
	cert.FailureBound = "0/1"
	if err := cert.Verify(m); err == nil {
		t.Fatal("Verify accepted a tampered failure bound")
	}
}

func TestCertificateDetectsTamperedModel(t *testing.T) {
	m := conditionalAbstentionModel()
	a := mustCompile(t, m)
	res, err := a.Enumerate()
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	cert, err := a.Certificate(res)
	if err != nil {
		t.Fatalf("Certificate: %v", err)
	}
	m.Ideal["A"] = rat("1/3")
	m.Ideal["B"] = rat("2/3")
	if err := cert.Verify(m); err == nil {
		t.Fatal("Verify accepted a certificate against an altered model")
	}
}

func TestCertificateDigestStable(t *testing.T) {
	a1 := mustCompile(t, conditionalAbstentionModel())
	a2 := mustCompile(t, conditionalAbstentionModel())
	if a1.ModelDigest() != a2.ModelDigest() {
		t.Fatal("identical models produced different digests")
	}
}
