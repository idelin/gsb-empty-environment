package ipp_test

import (
	"fmt"
	"testing"

	ipp "prescreen.local/ps-bad42ea1f6"
)

func TestSmallPrimeTranscriptRehashesZero(t *testing.T) {
	ad, err := ipp.NewSmallGroupWithOrder(3)
	if err != nil {
		t.Fatal(err)
	}
	gens := ipp.SmallGenerators(ad, 2)
	transcript, err := ipp.NewTranscript(ad, []byte("domain"), []byte("context"), gens, ad.Identity())
	if err != nil {
		t.Fatal(err)
	}
	x, err := transcript.Challenge(ad.Identity(), ad.Identity())
	if err != nil {
		t.Fatal(err)
	}
	if x.Sign() <= 0 || x.Cmp(ad.Order()) >= 0 {
		t.Fatalf("challenge outside [1,q): %v", x)
	}

	for i := 0; i < 64; i++ {
		tr, err := ipp.NewTranscript(ad, []byte("domain"), []byte(fmt.Sprintf("context-%d", i)), gens, ad.Identity())
		if err != nil {
			t.Fatal(err)
		}
		challenge, err := tr.Challenge(ad.Identity(), ad.Identity())
		if err != nil {
			t.Fatal(err)
		}
		if challenge.Sign() <= 0 || challenge.Cmp(ad.Order()) >= 0 {
			t.Fatalf("challenge outside [1,q): %v", challenge)
		}
	}
}
