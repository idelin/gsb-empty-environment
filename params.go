package netsec

import (
	"crypto/dsa"
	"crypto/x509/pkix"
	"encoding/asn1"
	"fmt"
	"math/big"
)

// DSAParameters is the set of DSA domain parameters (p, q, g).
type DSAParameters = dsa.Parameters

var (
	oidPublicKeyDSA = asn1.ObjectIdentifier{1, 2, 840, 10040, 4, 1}

	oidDSAWithSHA1   = asn1.ObjectIdentifier{1, 2, 840, 10040, 4, 3}
	oidDSAWithSHA224 = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 3, 1}
	oidDSAWithSHA256 = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 3, 2}
	oidDSAWithSHA384 = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 3, 3}
	oidDSAWithSHA512 = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 3, 4}
)

type dsaParameterSet struct {
	P, Q, G *big.Int
}

// ParametersEqual reports whether a and b contain the same DSA domain
// parameters. Two nil sets compare equal; nil and non-nil sets do not.
func ParametersEqual(a, b *dsa.Parameters) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.P != nil && b.P != nil && a.Q != nil && b.Q != nil && a.G != nil && b.G != nil &&
		a.P.Cmp(b.P) == 0 && a.Q.Cmp(b.Q) == 0 && a.G.Cmp(b.G) == 0
}

// ParseDSAParameters parses a DER-encoded Dss-Parms value.
func ParseDSAParameters(der []byte) (*dsa.Parameters, error) {
	var params dsaParameterSet
	rest, err := asn1.Unmarshal(der, &params)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidDSAParameters, err)
	}
	if len(rest) != 0 {
		return nil, fmt.Errorf("%w: trailing data", ErrInvalidDSAParameters)
	}
	if params.P == nil || params.Q == nil || params.G == nil ||
		params.P.Sign() <= 0 || params.Q.Sign() <= 0 || params.G.Sign() <= 0 {
		return nil, fmt.Errorf("%w: missing or non-positive component", ErrInvalidDSAParameters)
	}
	return &dsa.Parameters{P: params.P, Q: params.Q, G: params.G}, nil
}

func dsaParametersFromAlgorithmIdentifier(ai pkix.AlgorithmIdentifier) (*dsa.Parameters, error) {
	if len(ai.Parameters.FullBytes) == 0 {
		return nil, nil
	}
	return ParseDSAParameters(ai.Parameters.FullBytes)
}

func isDSASignatureAlgorithm(oid asn1.ObjectIdentifier) bool {
	return oid.Equal(oidDSAWithSHA1) ||
		oid.Equal(oidDSAWithSHA224) ||
		oid.Equal(oidDSAWithSHA256) ||
		oid.Equal(oidDSAWithSHA384) ||
		oid.Equal(oidDSAWithSHA512)
}
