package bitlayout_test

import (
	"strings"
	"testing"

	"bitlayout"
)

func mustCompile(t *testing.T, schema string) *bitlayout.Plan {
	t.Helper()
	p, err := bitlayout.Compile([]byte(schema), nil)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	return p
}

func mustReject(t *testing.T, schema string, substr string) {
	t.Helper()
	_, err := bitlayout.Compile([]byte(schema), nil)
	if err == nil {
		t.Fatalf("expected rejection containing %q, got success", substr)
	}
	if !strings.Contains(err.Error(), substr) {
		t.Fatalf("expected rejection containing %q, got %q", substr, err.Error())
	}
}

func TestCompileRejects(t *testing.T) {
	cases := []struct {
		name, schema, want string
	}{
		{"forward length ref", `{"fields":[
			{"kind":"array","name":"a","lengthFrom":"n","element":{"kind":"int","bits":4}},
			{"kind":"int","name":"n","bits":4}]}`, "forward length reference"},
		{"self length ref", `{"fields":[
			{"kind":"array","name":"a","lengthFrom":"a","element":{"kind":"int","bits":4}}]}`, "forward length reference"},
		{"unknown length ref", `{"fields":[
			{"kind":"array","name":"a","lengthFrom":"zz","element":{"kind":"int","bits":4}}]}`, "does not name"},
		{"duplicate field name", `{"fields":[
			{"kind":"int","name":"x","bits":4},
			{"kind":"int","name":"x","bits":4}]}`, "field overlap"},
		{"undefined tag value", `{"fields":[
			{"kind":"int","name":"t","bits":2},
			{"kind":"union","name":"u","tagFrom":"t","cases":{"4":{"fields":[{"kind":"int","name":"a","bits":2}]}}}]}`, "undefined tag"},
		{"non-numeric tag", `{"fields":[
			{"kind":"int","name":"t","bits":2},
			{"kind":"union","name":"u","tagFrom":"t","cases":{"x":{"fields":[{"kind":"int","name":"a","bits":2}]}}}]}`, "undefined tag"},
		{"missing tagFrom", `{"fields":[
			{"kind":"union","name":"u","tagFrom":"nope","cases":{"0":{"fields":[{"kind":"int","name":"a","bits":2}]}}}]}`, "does not name"},
		{"unbounded array", `{"fields":[
			{"kind":"array","name":"a","element":{"kind":"int","bits":4}}]}`, "unbounded allocation"},
		{"huge length field", `{"fields":[
			{"kind":"int","name":"n","bits":32},
			{"kind":"array","name":"a","lengthFrom":"n","element":{"kind":"int","bits":4}}]}`, "unbounded allocation"},
		{"signed length field", `{"fields":[
			{"kind":"int","name":"n","bits":4,"signed":true},
			{"kind":"array","name":"a","lengthFrom":"n","element":{"kind":"int","bits":4}}]}`, "unsigned int"},
		{"checksum forward range", `{"fields":[
			{"kind":"checksum","name":"c","bits":8,"algo":"sum","from":"a","to":"a"},
			{"kind":"int","name":"a","bits":4}]}`, "earlier top-level"},
		{"checksum reversed range", `{"fields":[
			{"kind":"int","name":"a","bits":4},
			{"kind":"int","name":"b","bits":4},
			{"kind":"checksum","name":"c","bits":8,"algo":"sum","from":"b","to":"a"}]}`, "reversed"},
		{"bad kind", `{"fields":[{"kind":"float","name":"a"}]}`, "unknown kind"},
		{"bad bits", `{"fields":[{"kind":"int","name":"a","bits":0}]}`, "bits must be"},
		{"bad algo", `{"fields":[
			{"kind":"int","name":"a","bits":4},
			{"kind":"checksum","name":"c","bits":8,"algo":"md5","from":"a","to":"a"}]}`, "unknown checksum algo"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { mustReject(t, tc.schema, tc.want) })
	}
}

func TestCompileAccepts(t *testing.T) {
	p := mustCompile(t, `{
		"name": "ok", "bitOrder": "lsb", "byteOrder": "little",
		"fields": [
			{"kind":"int","name":"tag","bits":3},
			{"kind":"int","name":"level","bits":10,"signed":true},
			{"kind":"union","name":"body","tagFrom":"tag","cases":{
				"0":{"fields":[{"kind":"int","name":"code","bits":4}]},
				"1":{"fields":[{"kind":"int","name":"count","bits":4},
					{"kind":"array","name":"samples","lengthFrom":"count",
						"element":{"kind":"int","bits":12}}]}
			}},
			{"kind":"pad","align":8},
			{"kind":"checksum","name":"crc","bits":8,"algo":"crc8","from":"tag","to":"body"}
		]}`)
	if p.MinBits != 3+10+4+0+8 {
		t.Fatalf("MinBits = %d", p.MinBits)
	}
	// static bound: 3+10+4+15*12 = 197, pad contributes at most 7, +8 crc
	if p.MaxBits != 212 {
		t.Fatalf("MaxBits = %d", p.MaxBits)
	}
}
