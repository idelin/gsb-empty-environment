package fuzz

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"math/rand"

	"kvsfmt/format"
	"kvsfmt/oracle"
)

// DecoderFunc is any decoder under test.
type DecoderFunc func([]byte) ([]format.Entry, error)

// Failure is one reproducible check failure. Seed plus Data (the
// failing bytes) are sufficient to replay it.
type Failure struct {
	Kind   string // "budget", "roundtrip", "determinism", "rejection", "wrong-class", "mutation-drift"
	Seed   int64
	Mut    string
	Want   format.Class
	Got    format.Class
	Data   []byte
	Detail string
}

func (f Failure) String() string {
	return fmt.Sprintf("kind=%s seed=%d mut=%q want=%s got=%s data=%s %s",
		f.Kind, f.Seed, f.Mut, f.Want, f.Got, hex.EncodeToString(f.Data), f.Detail)
}

func equalEntries(a, b []format.Entry) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Key != b[i].Key || !bytes.Equal(a[i].Value, b[i].Value) {
			return false
		}
	}
	return true
}

// CheckSample runs round-trip, determinism, budget, and rejection
// checks for one generated sample against dec.
func CheckSample(seed int64, entries []format.Entry, data []byte, dec DecoderFunc) []Failure {
	var fails []Failure

	if len(data) > format.Budget {
		fails = append(fails, Failure{
			Kind: "budget", Seed: seed, Data: data,
			Detail: "encoder output exceeds budget: implementation issue",
		})
	}

	got, err := dec(data)
	if err != nil {
		fails = append(fails, Failure{
			Kind: "roundtrip", Seed: seed, Data: data,
			Detail: "decoder rejected valid sample: " + err.Error(),
		})
	} else {
		if !equalEntries(got, entries) {
			fails = append(fails, Failure{
				Kind: "roundtrip", Seed: seed, Data: data,
				Detail: "decoded entries differ from encoded entries",
			})
		}
		data2, err2 := format.Encode(got)
		if err2 != nil {
			fails = append(fails, Failure{
				Kind: "determinism", Seed: seed, Data: data,
				Detail: "re-encode of decoded entries failed: " + err2.Error(),
			})
		} else if !bytes.Equal(data2, data) {
			fails = append(fails, Failure{
				Kind: "determinism", Seed: seed, Data: data,
				Detail: "encode(decode(x)) != x",
			})
		}
	}

	for _, m := range Mutations() {
		mb := m.Apply(data)
		if mb == nil {
			continue
		}
		// A mutation that still produces a legal document must not be
		// forced into a parser error; skip it.
		if oracle.Valid(mb) {
			continue
		}
		// The mutation must break exactly the constraint it claims.
		if cls := format.Class(oracle.Classify(mb)); cls != m.Class {
			fails = append(fails, Failure{
				Kind: "mutation-drift", Seed: seed, Mut: m.Name,
				Want: m.Class, Got: cls, Data: mb,
				Detail: "mutation broke a different constraint than declared",
			})
			continue
		}
		_, derr := dec(mb)
		switch {
		case derr == nil:
			fails = append(fails, Failure{
				Kind: "rejection", Seed: seed, Mut: m.Name,
				Want: m.Class, Got: format.ClassNone, Data: mb,
				Detail: "decoder accepted invalid document",
			})
		case format.ClassOf(derr) != m.Class:
			fails = append(fails, Failure{
				Kind: "wrong-class", Seed: seed, Mut: m.Name,
				Want: m.Class, Got: format.ClassOf(derr), Data: mb,
				Detail: "decoder rejected with wrong error class",
			})
		}
	}
	return fails
}

// RunCorpus generates n samples from seed and checks each against dec.
func RunCorpus(seed int64, n int, dec DecoderFunc) []Failure {
	rng := rand.New(rand.NewSource(seed))
	var fails []Failure
	for i := 0; i < n; i++ {
		entries, data := Generate(rng)
		fails = append(fails, CheckSample(seed, entries, data, dec)...)
	}
	return fails
}
