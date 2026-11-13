package netsec

import (
	"crypto/x509"
	"encoding/asn1"
	"encoding/pem"
	"errors"
	"testing"
)

func TestValidateCertificatePathUniqueExtensions(t *testing.T) {
	_, _, _, chain := q0265Chain(t)

	if err := CheckCertificatePath(chain); err != nil {
		t.Fatalf("CheckCertificatePath rejected unique extensions: %v", err)
	}
	if err := ValidateCertificatePath(chain); err != nil {
		t.Fatalf("ValidateCertificatePath rejected a valid path: %v", err)
	}

	verified, err := VerifyCertificatePath(chain, "leaf.example.com")
	if err != nil {
		t.Fatalf("VerifyCertificatePath: %v", err)
	}
	if len(verified) == 0 || len(verified[0]) == 0 {
		t.Fatal("VerifyCertificatePath returned no chain")
	}
}

func TestValidateCertificatePathRejectsDuplicateBeforeNameConstraints(t *testing.T) {
	leaf, _, root, chain := q0265Chain(t)
	_ = leaf

	wide := chain[1]
	wide.Extensions = append(wide.Extensions, q0265Extension(
		OIDExtensionNameConstraints,
		q0265NameConstraintsValue("other.example.com", false),
	), q0265Extension(
		OIDExtensionNameConstraints,
		q0265NameConstraintsValue("another.example.com", false),
	))

	if err := ValidateCertificatePath(chain); err == nil {
		t.Fatal("path with duplicate name constraints accepted")
	} else if !errors.Is(err, ErrDuplicateExtension) {
		t.Fatalf("error = %v, want duplicate extension failure", err)
	}

	if err := ValidateCertificatePath(chain, PathOptions{RootCertificates: []*x509.Certificate{root.cert}}); err == nil {
		t.Fatal("path with duplicate name constraints accepted with explicit root")
	} else if !errors.Is(err, ErrDuplicateExtension) {
		t.Fatalf("error = %v, want duplicate extension failure", err)
	}
}

func TestValidateCertificatePathRejectsDuplicateInRoot(t *testing.T) {
	leaf, intermediate, root, _ := q0265Chain(t)
	root.cert.Extensions = append(root.cert.Extensions, q0265Extension(
		asn1.ObjectIdentifier{2, 5, 29, 17},
		[]byte{0x30, 0x00},
	), q0265Extension(
		asn1.ObjectIdentifier{2, 5, 29, 17},
		[]byte{0x30, 0x00},
	))

	chain := []*x509.Certificate{leaf.cert, intermediate.cert}
	err := ValidateCertificatePath(chain, PathOptions{RootCertificates: []*x509.Certificate{root.cert}})
	if err == nil {
		t.Fatal("path with duplicate extension in root accepted")
	}
	if !errors.Is(err, ErrDuplicateExtension) {
		t.Fatalf("error = %v, want duplicate extension failure", err)
	}

	roots := x509.NewCertPool()
	roots.AddCert(root.cert)
	err = ValidateCertificatePath([]*x509.Certificate{leaf.cert, intermediate.cert}, roots)
	if err == nil {
		t.Fatal("path with duplicate extension in pooled root accepted")
	}
	if !errors.Is(err, ErrDuplicateExtension) {
		t.Fatalf("pooled root error = %v, want duplicate extension failure", err)
	}
}

func TestValidateCertificatePathOptions(t *testing.T) {
	leaf, intermediate, root, _ := q0265Chain(t)
	chain := []*x509.Certificate{leaf.cert, intermediate.cert}

	roots := x509.NewCertPool()
	roots.AddCert(root.cert)

	if err := ValidateCertificatePath(chain, PathOptions{Roots: roots, DNSName: "leaf.example.com"}); err != nil {
		t.Fatalf("PathOptions validation: %v", err)
	}
	if err := ValidateCertificatePath(chain, x509.VerifyOptions{
		Roots:   roots,
		DNSName: "leaf.example.com",
	}); err != nil {
		t.Fatalf("VerifyOptions validation: %v", err)
	}
	if err := ValidateCertificatePath(chain, "leaf.example.com", roots); err != nil {
		t.Fatalf("variadic option validation: %v", err)
	}
	if err := ValidateCertificatePath(chain, PathOptions{Roots: roots, DNSName: "other.example.com"}); err == nil {
		t.Fatal("wrong DNS name accepted")
	}

	fullChain := []*x509.Certificate{leaf.cert, intermediate.cert, root.cert}
	if err := ValidateCertificatePath(fullChain, PathOptions{
		RootCertificates: []*x509.Certificate{root.cert},
		DNSName:          "leaf.example.com",
	}); err != nil {
		t.Fatalf("full chain with explicit root: %v", err)
	}

	intermediates := x509.NewCertPool()
	intermediates.AddCert(intermediate.cert)
	if err := ValidateCertificatePath(leaf.cert, roots, intermediates, "leaf.example.com"); err != nil {
		t.Fatalf("positional root/intermediate pools: %v", err)
	}
}

func TestValidateCertificatePathDERAndPEM(t *testing.T) {
	_, _, _, chain := q0265Chain(t)

	var der []byte
	var pemData []byte
	for _, cert := range chain {
		der = append(der, cert.Raw...)
		pemData = append(pemData, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})...)
	}

	if err := ValidateCertificatePath(der); err != nil {
		t.Fatalf("DER path validation: %v", err)
	}
	if err := ValidateCertificatePath(pemData); err != nil {
		t.Fatalf("PEM path validation: %v", err)
	}
}

func TestCheckCertificatePathUniqueContinues(t *testing.T) {
	_, _, _, chain := q0265Chain(t)
	if err := CheckCertificatePath(chain); err != nil {
		t.Fatalf("unique path did not continue: %v", err)
	}
}

func TestValidateCertificatePathEnforcesUniqueNameConstraints(t *testing.T) {
	root := newQ0265Cert(t, q0265RootTemplate(), nil)
	intermediateTemplate := q0265IntermediateTemplate()
	intermediateTemplate.PermittedDNSDomains = []string{"allowed.example.com"}
	intermediate := newQ0265Cert(t, intermediateTemplate, root)

	allowed := newQ0265Cert(t, q0265LeafTemplate("allowed.example.com"), intermediate)
	blocked := newQ0265Cert(t, q0265LeafTemplate("blocked.example.com"), intermediate)

	allowedChain := []*x509.Certificate{allowed.cert, intermediate.cert, root.cert}
	if err := ValidateCertificatePath(allowedChain); err != nil {
		t.Fatalf("allowed name rejected: %v", err)
	}

	blockedChain := []*x509.Certificate{blocked.cert, intermediate.cert, root.cert}
	if err := ValidateCertificatePath(blockedChain); err == nil {
		t.Fatal("name outside a single name constraint accepted")
	}
}

func TestValidatorType(t *testing.T) {
	leaf, intermediate, root, _ := q0265Chain(t)
	chain := []*x509.Certificate{leaf.cert, intermediate.cert}
	validator := NewValidator(root.cert)
	validator.Options.DNSName = "leaf.example.com"

	if err := validator.Validate(chain); err != nil {
		t.Fatalf("Validator.Validate: %v", err)
	}
	chains, err := validator.Verify(chain)
	if err != nil {
		t.Fatalf("Validator.Verify: %v", err)
	}
	if len(chains) == 0 {
		t.Fatal("Validator.Verify returned no chains")
	}
}
