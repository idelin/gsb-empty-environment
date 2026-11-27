package writeproof

import "testing"

func TestMaskedDomainCoversSelectedAddresses(t *testing.T) {
	domain, err := decomposeMaskedDomain(181, 240, 94, 78, 8, nil)
	if err != nil {
		t.Fatal(err)
	}
	var found206, found207 bool
	for x := uint64(181); x < 240; x++ {
		if x&94 != 78 {
			continue
		}
		inside := false
		for _, cube := range domain.Cubes {
			if x&^cube.FreeMask == cube.Base&^cube.FreeMask {
				inside = true
			}
		}
		if !inside {
			t.Fatalf("address %d not covered by %+v", x, domain.Cubes)
		}
		found206 = found206 || x == 206
		found207 = found207 || x == 207
	}
	if !found206 || !found207 {
		t.Fatalf("206=%v 207=%v cubes=%+v", found206, found207, domain.Cubes)
	}
}
