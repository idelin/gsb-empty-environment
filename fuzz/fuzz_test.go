package fuzz

import (
	"bytes"
	"math/rand"
	"testing"

	"kvsfmt/format"
	"kvsfmt/oracle"
)

func TestGeneratorDeterministic(t *testing.T) {
	_, a := Generate(rand.New(rand.NewSource(42)))
	_, b := Generate(rand.New(rand.NewSource(42)))
	if !bytes.Equal(a, b) {
		t.Fatal("same seed produced different bytes")
	}
}

func TestCorpusReferenceDecoder(t *testing.T) {
	fails := RunCorpus(1, 300, format.Decode)
	if len(fails) != 0 {
		t.Fatalf("%d failures, first: %s", len(fails), fails[0])
	}
}

// The oracle is implemented independently of the encoder/decoder under
// test; both must agree on validity and error class across valid
// samples, mutations, and random garbage.
func TestOracleAgreement(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	check := func(data []byte) {
		_, err := format.Decode(data)
		got := format.ClassNone
		if err != nil {
			got = format.ClassOf(err)
		}
		want := format.Class(oracle.Classify(data))
		if got != want {
			t.Fatalf("disagree on %x: decoder=%q oracle=%q", data, got, want)
		}
		if err == nil {
			pairs, perr := oracle.Parse(data)
			if perr != nil {
				t.Fatalf("oracle rejected decoder-valid %x: %v", data, perr)
			}
			entries, _ := format.Decode(data)
			if len(entries) != len(pairs) {
				t.Fatalf("entry count mismatch on %x", data)
			}
			for i := range entries {
				if entries[i].Key != string(pairs[i].Key) || !bytes.Equal(entries[i].Value, pairs[i].Value) {
					t.Fatalf("entry %d mismatch on %x", i, data)
				}
			}
		}
	}
	for i := 0; i < 100; i++ {
		_, data := Generate(rng)
		check(data)
		for _, m := range Mutations() {
			if mb := m.Apply(data); mb != nil {
				check(mb)
			}
		}
		garbage := make([]byte, rng.Intn(64))
		rng.Read(garbage)
		check(garbage)
	}
}

func TestShrinkArtificial(t *testing.T) {
	// Predicate: input contains 0xAA. Minimal reproducer is [0xAA].
	pred := func(b []byte) bool {
		return bytes.IndexByte(b, 0xAA) >= 0
	}
	res := Shrink([]byte{1, 2, 0xAA, 3, 4}, pred)
	if !bytes.Equal(res.Minimal, []byte{0xAA}) {
		t.Fatalf("minimal = %x, want [aa]", res.Minimal)
	}
	if !pred(res.Minimal) {
		t.Fatal("minimal does not reproduce")
	}
	// 1-minimality: the only deletable byte must not reproduce.
	if pred([]byte{}) {
		t.Fatal("empty input reproduces; predicate broken")
	}
	foundRecord := false
	for _, line := range res.Log {
		if bytes.Contains([]byte(line), []byte("1-min delete@0")) {
			foundRecord = true
		}
	}
	if !foundRecord {
		t.Fatal("shrink log lacks 1-minimality record")
	}
}
