package jsonpathstream

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"testing"
)

// TestFuzzParity generates random nested documents and random supported
// queries, then asserts the streaming engine (at several chunk sizes) returns
// exactly the same ordered matches as the whole-document evaluator.
func TestFuzzParity(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	keys := []string{"id", "name", "price", "active", "tag"}
	strVals := []string{"alpha", "beta", "gamma", "x", "y"}
	randomScalar := func() any {
		switch rng.Intn(5) {
		case 0:
			return float64(rng.Intn(20))
		case 1:
			return strVals[rng.Intn(len(strVals))]
		case 2:
			return rng.Intn(2) == 0
		case 3:
			return nil
		default:
			return float64(rng.Intn(100)) / 2.0
		}
	}
	var randomValue func(depth int) any
	randomValue = func(depth int) any {
		if depth <= 0 || rng.Intn(3) == 0 {
			return randomScalar()
		}
		if rng.Intn(2) == 0 {
			n := 1 + rng.Intn(4)
			arr := make([]any, n)
			for i := range arr {
				arr[i] = randomValue(depth - 1)
			}
			return arr
		}
		obj := map[string]any{}
		n := 1 + rng.Intn(4)
		for i := 0; i < n; i++ {
			obj[keys[rng.Intn(len(keys))]] = randomValue(depth - 1)
		}
		return obj
	}

	queryTemplates := []string{
		"$",
		"$.%s",
		"$..%s",
		"$.*",
		"$..*",
		"$[%d]",
		"$[-%d]",
		"$[%d:%d]",
		"$[:%d]",
		"$[%d::2]",
		"$[*]",
		"$..[%d]",
		"$[?(@.%s == %v)]",
		"$[?(@.%s < %d)]",
		"$[?(@.%s != %v)]",
		"$..[?(@.%s == %v)]",
	}
	chunks := []int{1, 4, 33}
	for iter := 0; iter < 200; iter++ {
		doc, err := json.Marshal(randomValue(4))
		if err != nil {
			t.Fatal(err)
		}
		q := queryTemplates[rng.Intn(len(queryTemplates))]
		q = fmt.Sprintf(q,
			keys[rng.Intn(len(keys))],
			rng.Intn(3), 1+rng.Intn(3), rng.Intn(3), rng.Intn(3),
			rng.Intn(3), keys[rng.Intn(len(keys))], rng.Intn(10),
			keys[rng.Intn(len(keys))], keys[rng.Intn(len(keys))], strVals[rng.Intn(len(strVals))],
			keys[rng.Intn(len(keys))], strVals[rng.Intn(len(strVals))])
		p, perr := Parse(q)
		if perr != nil {
			continue // skip queries the grammar rejects
		}
		want := referenceEval(t, p, doc)
		for _, chunk := range chunks {
			got := streamEval(t, p, doc, chunk)
			if len(got) != len(want) {
				t.Fatalf("iter %d %q (chunk %d): got %d %v want %d %v\ndoc=%s", iter, q, chunk, len(got), got, len(want), want, doc)
			}
			for i := range want {
				if got[i].Path != want[i].Path || !jsonEqual(got[i].Value, want[i].Value) {
					t.Fatalf("iter %d %q (chunk %d): mismatch at %d got %#v want %#v\ndoc=%s", iter, q, chunk, i, got[i], want[i], doc)
				}
			}
		}
	}
}

func jsonEqual(a, b any) bool {
	aj, _ := json.Marshal(a)
	bj, _ := json.Marshal(b)
	return string(aj) == string(bj)
}
