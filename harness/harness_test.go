package harness_test

import (
	"math/rand"
	"path/filepath"
	"strings"
	"testing"

	"pkv/buggy"
	"pkv/format"
	"pkv/gen"
	"pkv/harness"
	"pkv/oracle"
)

var cleanSUT = harness.SUT{Name: "format", Encode: format.Encode, Decode: format.Decode}
var buggySUT = harness.SUT{Name: "buggy", Encode: format.Encode, Decode: buggy.Decode}

func TestCleanSUTPasses(t *testing.T) {
	failures := harness.Run(cleanSUT, 42, 200)
	for _, f := range failures {
		t.Errorf("unexpected failure: %s", f.Summary())
	}
}

// The oracle is an independent implementation; both must agree on every
// generated message and every mutant.
func TestOracleAgreement(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	for i := 0; i < 200; i++ {
		m := gen.ValidMessage(r)
		sb, _ := format.Encode(m)
		om := &oracle.Message{Records: make([]oracle.Record, len(m.Records))}
		for j, rec := range m.Records {
			om.Records[j] = oracle.Record{Key: rec.Key, Value: rec.Value}
		}
		ob, err := oracle.Encode(om)
		if err != nil || string(ob) != string(sb) {
			t.Fatalf("iter %d: oracle encoder disagrees with SUT", i)
		}
		for _, mut := range gen.Mutations() {
			mt := mut.Apply(m, sb)
			_, serr := format.Decode(mt.Data)
			_, oerr := oracle.Decode(mt.Data)
			if (serr == nil) != (oerr == nil) {
				t.Fatalf("iter %d mutation %s: accept/reject disagree (sut=%v oracle=%v)",
					i, mt.Name, serr, oerr)
			}
			if serr != nil && string(format.ClassOf(serr)) != oracle.ClassOf(oerr) {
				t.Fatalf("iter %d mutation %s: class disagree (sut=%s oracle=%s)",
					i, mt.Name, format.ClassOf(serr), oracle.ClassOf(oerr))
			}
		}
	}
}

// The known-defective decoder (ignores checksum) must be detected, and the
// failure must be reduced to a 1-minimal, replayable record.
func TestBuggyDecoderDetected(t *testing.T) {
	failures := harness.Run(buggySUT, 42, 50)
	if len(failures) == 0 {
		t.Fatal("defective decoder was not detected")
	}
	for _, f := range failures {
		if f.Mutation != "checksum" || f.Kind != "accept" {
			t.Errorf("unexpected failure kind: %s", f.Summary())
			continue
		}
		if f.ExpectClass != string(format.ClassChecksum) {
			t.Errorf("expect class = %q, want checksum", f.ExpectClass)
		}
		if f.MinimalHex == "" {
			t.Error("minimal failing bytes not recorded")
		}
		joined := strings.Join(f.ShrinkLog, "\n")
		if !strings.Contains(joined, "1-minimal confirmed") {
			t.Errorf("1-minimal check record missing:\n%s", joined)
		}
		ok, why := f.Replay(buggySUT)
		if !ok {
			t.Errorf("replay on buggy SUT did not reproduce: %s", why)
		}
		okClean, _ := f.Replay(cleanSUT)
		if okClean {
			t.Error("clean SUT should not reproduce the deviation")
		}
	}
}

func TestFailureSaveLoadReplay(t *testing.T) {
	failures := harness.Run(buggySUT, 42, 10)
	if len(failures) == 0 {
		t.Fatal("no failure to persist")
	}
	path := filepath.Join(t.TempDir(), "failure.json")
	if err := failures[0].Save(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := harness.LoadFailure(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Seed != failures[0].Seed || loaded.MinimalHex != failures[0].MinimalHex {
		t.Fatal("record did not round-trip")
	}
	if ok, why := loaded.Replay(buggySUT); !ok {
		t.Fatalf("reloaded failure does not reproduce: %s", why)
	}
}

// Over-budget encoder output must be reported as an implementation defect,
// not as a parse error.
func TestEncodeBudgetIsImplementationDefect(t *testing.T) {
	bloated := harness.SUT{
		Name: "bloated",
		Encode: func(m *format.Message) ([]byte, error) {
			b, err := format.Encode(m)
			if err != nil {
				return nil, err
			}
			return append(b, make([]byte, format.MaxMessageSize)...), nil
		},
		Decode: format.Decode,
	}
	failures := harness.Run(bloated, 42, 5)
	if len(failures) == 0 {
		t.Fatal("over-budget encoder not flagged")
	}
	for _, f := range failures {
		if f.Kind != "encode_budget" {
			t.Errorf("kind = %s, want encode_budget", f.Kind)
		}
	}
}
