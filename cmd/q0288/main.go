// Command q0288 verifies an X.509 certificate path, including registeredID
// name constraints.
//
// Usage:
//
//	q0288 -root root.pem -intermediate intermediate.pem -leaf leaf.pem
//	q0288 -root root.pem -leaf leaf.pem -oid 1.2.3.4
//	q0288 -root root.pem -leaf leaf.pem -policy strict
package main

import (
	"crypto/x509"
	"encoding/pem"
	"flag"
	"fmt"
	"os"
	"strings"

	"example.org/netsec/q0288"
)

func main() {
	rootPath := flag.String("root", "", "PEM file containing the trust anchor")
	intermediatePath := flag.String("intermediate", "", "PEM file containing one or more intermediate CA certificates")
	leafPath := flag.String("leaf", "", "PEM file containing the leaf certificate")
	oidText := flag.String("oid", "", "require this registeredID OID on the leaf")
	dnsName := flag.String("dns", "", "require this DNS name on the leaf")
	policyText := flag.String("policy", "legacy", "name-constraint policy: legacy or strict")
	flag.Parse()

	if *leafPath == "" || *rootPath == "" {
		fmt.Fprintln(os.Stderr, "both -leaf and -root are required")
		os.Exit(2)
	}
	policy := netsec.PolicyLegacy
	switch strings.ToLower(*policyText) {
	case "legacy":
	case "strict":
		policy = netsec.PolicyStrict
	default:
		fmt.Fprintf(os.Stderr, "unknown policy %q\n", *policyText)
		os.Exit(2)
	}

	leaf := mustParse(*leafPath)
	root := mustParse(*rootPath)
	var chain []*x509.Certificate
	chain = append(chain, leaf)
	if *intermediatePath != "" {
		chain = append(chain, parseAll(*intermediatePath)...)
	}
	chain = append(chain, root)

	opts := netsec.Options{Policy: policy, DNSName: *dnsName}
	if err := netsec.VerifyChain(chain, opts); err != nil {
		fmt.Fprintln(os.Stderr, "certificate path rejected:", err)
		os.Exit(1)
	}
	if *oidText != "" {
		oid, err := netsec.ParseOID(*oidText)
		if err != nil {
			fmt.Fprintln(os.Stderr, "invalid -oid:", err)
			os.Exit(2)
		}
		if err := netsec.VerifyRegisteredID(leaf, oid, policy); err != nil {
			fmt.Fprintln(os.Stderr, "registeredID check failed:", err)
			os.Exit(1)
		}
	}
	fmt.Println("certificate path accepted")
}

func mustParse(path string) *x509.Certificate {
	certs := parseAll(path)
	if len(certs) != 1 {
		fmt.Fprintf(os.Stderr, "%s contains %d certificates; expected exactly one\n", path, len(certs))
		os.Exit(2)
	}
	return certs[0]
}

func parseAll(path string) []*x509.Certificate {
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	var certs []*x509.Certificate
	for {
		var block *pem.Block
		block, data = pem.Decode(data)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		certs = append(certs, cert)
	}
	if len(certs) == 0 {
		fmt.Fprintf(os.Stderr, "%s contains no CERTIFICATE blocks\n", path)
		os.Exit(2)
	}
	return certs
}
