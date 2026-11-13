package netsec

import (
	"encoding/pem"
	"fmt"
)

// ParsePEMCertificates extracts the DER encoding of every CERTIFICATE block
// in data.
func ParsePEMCertificates(data []byte) ([][]byte, error) {
	var chain [][]byte
	for {
		block, rest := pem.Decode(data)
		if block == nil {
			break
		}
		data = rest
		if block.Type != "CERTIFICATE" {
			continue
		}
		chain = append(chain, block.Bytes)
	}
	if len(chain) == 0 {
		return nil, fmt.Errorf("%w: no CERTIFICATE blocks found", ErrInvalidCertificate)
	}
	return chain, nil
}

// VerifyPEMChain verifies a PEM-encoded certificate path. The certificates may
// be supplied in leaf-to-root or root-to-leaf order.
func VerifyPEMChain(data []byte) error {
	chain, err := ParsePEMCertificates(data)
	if err != nil {
		return err
	}
	return VerifyDERChain(chain)
}
