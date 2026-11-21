package keyusage

import "strings"

// Purpose is a bit field describing the declared cryptographic purposes of a
// key. A key may hold any combination of purposes.
type Purpose uint32

const (
	PurposeNone Purpose = 0

	PurposeSign      Purpose = 1 << iota // produce signatures / MACs
	PurposeVerify                        // verify signatures / MACs
	PurposeEncrypt                       // encrypt business data
	PurposeDecrypt                       // decrypt business data
	PurposeWrapKey                       // wrap (encrypt) other key material
	PurposeUnwrapKey                     // unwrap (decrypt) other key material
	PurposeDeriveKey                     // act as a parent in key derivation
)

var purposeNames = []struct {
	bit  Purpose
	name string
}{
	{PurposeSign, "sign"},
	{PurposeVerify, "verify"},
	{PurposeEncrypt, "encrypt"},
	{PurposeDecrypt, "decrypt"},
	{PurposeWrapKey, "wrap_key"},
	{PurposeUnwrapKey, "unwrap_key"},
	{PurposeDeriveKey, "derive_key"},
}

// contains reports whether p includes every bit of want.
func (p Purpose) contains(want Purpose) bool {
	return p&want == want
}

// String renders the enabled purposes as a pipe-separated list.
func (p Purpose) String() string {
	if p == PurposeNone {
		return "none"
	}
	var parts []string
	for _, entry := range purposeNames {
		if p&entry.bit != 0 {
			parts = append(parts, entry.name)
		}
	}
	return strings.Join(parts, "|")
}
