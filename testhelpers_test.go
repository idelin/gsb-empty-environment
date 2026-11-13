package netsec

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"math/big"
	"sync/atomic"
	"testing"
	"time"
)

var q0265Serial atomic.Int64

type q0265Cert struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	der  []byte
}

func newQ0265Cert(t *testing.T, template *x509.Certificate, parent *q0265Cert) *q0265Cert {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	template.SerialNumber = big.NewInt(q0265Serial.Add(1))
	if template.NotBefore.IsZero() {
		template.NotBefore = time.Now().Add(-time.Hour)
	}
	if template.NotAfter.IsZero() {
		template.NotAfter = time.Now().Add(time.Hour)
	}

	parentCert := template
	signerKey := key
	if parent != nil {
		parentCert = parent.cert
		signerKey = parent.key
	}

	der, err := x509.CreateCertificate(rand.Reader, template, parentCert, &key.PublicKey, signerKey)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse generated certificate: %v", err)
	}
	return &q0265Cert{cert: parsed, key: key, der: der}
}

func q0265RootTemplate() *x509.Certificate {
	return &x509.Certificate{
		Subject:               pkix.Name{CommonName: "test root"},
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
}

func q0265IntermediateTemplate() *x509.Certificate {
	return &x509.Certificate{
		Subject:               pkix.Name{CommonName: "test intermediate"},
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
}

func q0265LeafTemplate(dnsName string) *x509.Certificate {
	return &x509.Certificate{
		Subject:     pkix.Name{CommonName: dnsName},
		DNSNames:    []string{dnsName},
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
}

func q0265Chain(t *testing.T) (leaf, intermediate, root *q0265Cert, chain []*x509.Certificate) {
	t.Helper()

	root = newQ0265Cert(t, q0265RootTemplate(), nil)
	intermediate = newQ0265Cert(t, q0265IntermediateTemplate(), root)
	leaf = newQ0265Cert(t, q0265LeafTemplate("leaf.example.com"), intermediate)
	chain = []*x509.Certificate{leaf.cert, intermediate.cert, root.cert}
	return leaf, intermediate, root, chain
}

func q0265NameConstraintsValue(domain string, excluded bool) []byte {
	type generalSubtree struct {
		Base asn1.RawValue
	}
	type nameConstraints struct {
		Permitted []generalSubtree `asn1:"optional,tag:0"`
		Excluded  []generalSubtree `asn1:"optional,tag:1"`
	}

	subtree := generalSubtree{
		Base: asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 2, Bytes: []byte(domain)},
	}
	var constraints nameConstraints
	if excluded {
		constraints.Excluded = []generalSubtree{subtree}
	} else {
		constraints.Permitted = []generalSubtree{subtree}
	}
	der, err := asn1.Marshal(constraints)
	if err != nil {
		panic(err)
	}
	return der
}

func q0265Extension(oid asn1.ObjectIdentifier, value []byte) pkix.Extension {
	return pkix.Extension{Id: oid, Value: value}
}
