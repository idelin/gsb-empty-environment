package nsec3_test

import (
	"fmt"
	"log"

	"prescreen.local/ps-1eac5e7124/nsec3"
)

// ExampleHashName shows the canonical salted iterated SHA-1 owner hash and
// its base32hex label form.
func ExampleHashName() {
	salt := []byte{0xaa, 0xbb, 0xcc, 0xdd}
	hash, err := nsec3.HashName(nsec3.HashSHA1, 12, salt, "a.example.")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(nsec3.Base32HexEncode(hash))
	// Output: 35mthgpgcu1qg68fab165klnsnk3dpvl
}

// ExampleVerifier_nxdomain walks the three mandatory NXDOMAIN proof
// elements.
func ExampleVerifier_nxdomain() {
	salt := []byte{0xaa, 0xbb, 0xcc, 0xdd}
	const iter = uint16(12)

	mustHash := func(name string) []byte {
		h, err := nsec3.HashName(nsec3.HashSHA1, iter, salt, name)
		if err != nil {
			log.Fatal(err)
		}
		return h
	}
	ncHash := mustHash("y.example.")
	wildHash := mustHash("*.example.")
	plusOne := func(h []byte) []byte {
		out := append([]byte(nil), h...)
		for i := len(out) - 1; i >= 0; i-- {
			out[i]++
			if out[i] != 0 {
				break
			}
		}
		return out
	}
	rec := func(ownerName string, next []byte) *nsec3.Record {
		return &nsec3.Record{
			Algorithm: nsec3.HashSHA1, Iterations: iter, Salt: salt,
			OwnerHash: mustHash(ownerName),
			NextHash:  next,
			Types:     map[uint16]bool{nsec3.TypeSOA: true, nsec3.TypeNS: true},
		}
	}
	records := []*nsec3.Record{
		rec("example.", mustHash("anchor-nc.example.")),
		rec("anchor-nc.example.", plusOne(ncHash)),
		rec("anchor-wild.example.", plusOne(wildHash)),
	}
	v, err := nsec3.NewVerifier(records)
	if err != nil {
		log.Fatal(err)
	}
	proof, err := v.VerifyNXDOMAIN("x.y.example.")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(proof.Kind, proof.ClosestEncloser, proof.NextCloser, proof.Wildcard)
	// Output: 1 example. y.example. *.example.
}
