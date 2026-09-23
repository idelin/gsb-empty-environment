// Package harness drives property checks for a PKV1 implementation under
// test (SUT): round-trip, determinism, rejection of single-point mutants,
// and agreement with the independent oracle. Every failure is reduced to a
// 1-minimal message, and the seed plus minimal failing bytes are recorded so
// the failure can be saved and replayed.
package harness

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"reflect"

	"pkv/format"
	"pkv/gen"
	"pkv/oracle"
	"pkv/shrink"
)

// SUT adapts an implementation under test to the harness.
type SUT struct {
	Name   string
	Encode func(*format.Message) ([]byte, error)
	Decode func([]byte) (*format.Message, error)
}

// Failure is a replayable record of one detected deviation.
type Failure struct {
	SUT         string   `json:"sut"`
	Seed        int64    `json:"seed"`
	Iteration   int      `json:"iteration"`
	Kind        string   `json:"kind"` // roundtrip|determinism|encode_budget|oracle_mismatch|accept|wrong_class|valid_reject|valid_mismatch
	Mutation    string   `json:"mutation,omitempty"`
	ExpectClass string   `json:"expect_class,omitempty"`
	GotClass    string   `json:"got_class,omitempty"`
	MinimalHex  string   `json:"minimal_hex"` // minimal failing decoder input
	ShrinkLog   []string `json:"shrink_log,omitempty"`
}

func (f *Failure) Summary() string {
	return fmt.Sprintf("kind=%s mutation=%s expect=%s got=%s seed=%d iter=%d minimal=%d bytes",
		f.Kind, f.Mutation, f.ExpectClass, f.GotClass, f.Seed, f.Iteration, len(f.MinimalHex)/2)
}

// Save writes the failure as JSON for later replay.
func (f *Failure) Save(path string) error {
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// LoadFailure reads a saved failure record.
func LoadFailure(path string) (*Failure, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f Failure
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	return &f, nil
}

// oracleEncode converts and encodes through the independent oracle.
func oracleEncode(m *format.Message) ([]byte, error) {
	om := &oracle.Message{Records: make([]oracle.Record, len(m.Records))}
	for i, r := range m.Records {
		om.Records[i] = oracle.Record{Key: r.Key, Value: r.Value}
	}
	return oracle.Encode(om)
}

func oracleEqual(om *oracle.Message, m *format.Message) bool {
	if om == nil || len(om.Records) != len(m.Records) {
		return false
	}
	for i, r := range m.Records {
		if om.Records[i].Key != r.Key || om.Records[i].Value != r.Value {
			return false
		}
	}
	return true
}

// checkMutant decodes one mutant with the SUT and returns the deviation
// kind ("" if the SUT behaves per spec) and the error class observed.
func checkMutant(sut SUT, mt gen.Mutant) (kind string, gotClass string) {
	dec, err := sut.Decode(mt.Data)
	if mt.Expect == format.ClassNone {
		switch {
		case err != nil:
			return "valid_reject", string(format.ClassOf(err))
		case !reflect.DeepEqual(dec, mt.Want):
			return "valid_mismatch", ""
		}
		return "", ""
	}
	if err == nil {
		return "accept", ""
	}
	if c := format.ClassOf(err); c != mt.Expect {
		return "wrong_class", string(c)
	}
	return "", ""
}

// Run executes iterations of generated messages against the SUT and returns
// one replayable Failure per detected deviation (at most one per iteration).
func Run(sut SUT, seed int64, iterations int) []Failure {
	r := rand.New(rand.NewSource(seed))
	var failures []Failure
	for i := 0; i < iterations; i++ {
		m := gen.ValidMessage(r)
		if f := checkMessage(sut, seed, i, m); f != nil {
			failures = append(failures, *f)
		}
	}
	return failures
}

func checkMessage(sut SUT, seed int64, iter int, m *format.Message) *Failure {
	base := Failure{SUT: sut.Name, Seed: seed, Iteration: iter}
	fail := func(kind, mutation, expect, got string, minimal []byte, log []string) *Failure {
		f := base
		f.Kind, f.Mutation, f.ExpectClass, f.GotClass = kind, mutation, expect, got
		f.MinimalHex = hex.EncodeToString(minimal)
		f.ShrinkLog = log
		return &f
	}

	if !format.InScope(m) {
		return fail("generator_scope", "", "", "", nil, nil)
	}
	sutBytes, err := sut.Encode(m)
	if err != nil {
		return fail("encode_error", "", "", err.Error(), nil, nil)
	}
	// Over-budget output for an in-scope message is an implementation
	// defect, never a parse error.
	if len(sutBytes) > format.MaxMessageSize {
		return fail("encode_budget", "", "", fmt.Sprintf("%d bytes", len(sutBytes)), sutBytes, nil)
	}

	// Oracle cross-check: independent encoder must agree byte for byte.
	canon, err := oracleEncode(m)
	if err != nil || !bytes.Equal(canon, sutBytes) {
		return fail("oracle_mismatch", "encode", "", "", sutBytes, nil)
	}

	// Round-trip and determinism.
	dec, derr := sut.Decode(sutBytes)
	if derr != nil || !reflect.DeepEqual(dec, m) {
		got := string(format.ClassOf(derr))
		pred := func(m2 *format.Message) bool {
			b2, e2 := sut.Encode(m2)
			if e2 != nil {
				return false
			}
			d2, de2 := sut.Decode(b2)
			return de2 != nil || !reflect.DeepEqual(d2, m2)
		}
		min, log := shrink.Shrink(m, pred)
		minBytes, _ := oracleEncode(min)
		return fail("roundtrip", "", "", got, minBytes, log)
	}
	again, _ := sut.Encode(dec)
	if !bytes.Equal(again, sutBytes) {
		return fail("determinism", "", "", "", sutBytes, nil)
	}

	// Oracle must decode the SUT's output to the same value.
	odec, oerr := oracle.Decode(sutBytes)
	if oerr != nil || !oracleEqual(odec, m) {
		return fail("oracle_mismatch", "decode", "", "", sutBytes, nil)
	}

	// Single-point mutations.
	for _, mut := range gen.Mutations() {
		mt := mut.Apply(m, canon)
		// Oracle agreement on the mutant itself (spec-level sanity).
		_, oerr := oracle.Decode(mt.Data)
		if mt.Expect == format.ClassNone && oerr != nil {
			return fail("oracle_mismatch", mt.Name, "valid", oracle.ClassOf(oerr), mt.Data, nil)
		}
		if mt.Expect != format.ClassNone && oracle.ClassOf(oerr) != string(mt.Expect) {
			return fail("oracle_mismatch", mt.Name, string(mt.Expect), oracle.ClassOf(oerr), mt.Data, nil)
		}
		kind, got := checkMutant(sut, mt)
		if kind == "" {
			continue
		}
		// Reduce while preserving the same deviation kind and error class.
		pred := func(m2 *format.Message) bool {
			c2, e2 := oracleEncode(m2)
			if e2 != nil {
				return false
			}
			k2, _ := checkMutant(sut, mut.Apply(m2, c2))
			return k2 == kind
		}
		min, log := shrink.Shrink(m, pred)
		minCanon, _ := oracleEncode(min)
		minMut := mut.Apply(min, minCanon)
		return fail(kind, mt.Name, string(mt.Expect), got, minMut.Data, log)
	}
	return nil
}

// Replay re-runs a recorded failure against a SUT and reports whether the
// same deviation still reproduces on the recorded minimal bytes.
func (f *Failure) Replay(sut SUT) (bool, string) {
	data, err := hex.DecodeString(f.MinimalHex)
	if err != nil {
		return false, "corrupt minimal bytes: " + err.Error()
	}
	_, serr := sut.Decode(data)
	_, oerr := oracle.Decode(data)
	switch f.Kind {
	case "accept":
		if serr == nil && oerr != nil && oracle.ClassOf(oerr) == f.ExpectClass {
			return true, "SUT still accepts input the oracle rejects with " + f.ExpectClass
		}
	case "wrong_class":
		if serr != nil && string(format.ClassOf(serr)) != f.ExpectClass {
			return true, "SUT still rejects with wrong class " + string(format.ClassOf(serr))
		}
	case "valid_reject":
		if serr != nil && oerr == nil {
			return true, "SUT still rejects input the oracle accepts"
		}
	case "roundtrip", "valid_mismatch":
		if oerr != nil {
			return false, "oracle no longer decodes minimal bytes"
		}
		re, eerr := sut.Encode(mustDecode(sut, data))
		if serr != nil || eerr != nil || !bytes.Equal(re, data) {
			return true, "round-trip still broken on minimal bytes"
		}
	case "encode_budget":
		if len(data) > format.MaxMessageSize {
			return true, "encoding still over budget"
		}
	}
	return false, "deviation did not reproduce"
}

func mustDecode(sut SUT, data []byte) *format.Message {
	m, _ := sut.Decode(data)
	return m
}
