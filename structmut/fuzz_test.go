package structmut

import (
	"encoding/binary"
	"math/rand"
	"testing"
)

// fuzzSchema is a compact format with alignment, a back-reference and a
// zero-width-capable payload, used as the seed model for fuzzing.
func fuzzSchema(style byte) *Schema {
	sch := schemaForTests()
	switch style % 3 {
	case 1:
		// Extra relative reference so the same target is referenced twice.
		sch.Sections[2].Body = []byte{0, 0}
		sch.Sections[2].Fields = append(sch.Sections[2].Fields,
			Field{ID: "rel2", Offset: 1, Kind: KindRelOffset, Width: 1, Target: "payload"})
	case 2:
		// A zero-length section referenced by a length and an abs field.
		sch.Sections = append(sch.Sections, &Section{
			ID:    "empty",
			Body:  nil,
			Align: 4,
			After: []SectionID{"back"},
		})
		sch.Sections[0].Body = append(sch.Sections[0].Body, 0, 0)
		sch.Sections[0].Fields = append(sch.Sections[0].Fields,
			Field{ID: "empty_len", Offset: 7, Kind: KindLength, Width: 1, Target: "empty"},
			Field{ID: "empty_abs", Offset: 8, Kind: KindAbsOffset, Width: 4, Target: "empty"},
		)
	}
	return sch
}

func FuzzApply(f *testing.F) {
	f.Add([]byte{0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef, 0x00})
	f.Add([]byte{0x10, 0x32, 0x54, 0x76, 0x98, 0xba, 0xdc, 0xfe, 0x01})
	f.Add(make([]byte, 9))

	f.Fuzz(func(t *testing.T, in []byte) {
		if len(in) < 8 {
			return
		}
		seed := int64(binary.LittleEndian.Uint64(in))
		sch := fuzzSchema(in[len(in)-1])
		before := snapshot(sch)

		res, err := sch.Random(rand.New(rand.NewSource(seed)), 6)
		if err != nil {
			// A rejected candidate must leave the input untouched.
			if !equalSnapshot(snapshot(sch), before) {
				t.Fatal("rejected candidate mutated original schema")
			}
			return
		}
		report, err := Read(res.Blob, res.Schema)
		if err != nil {
			t.Fatalf("independent read-back failed: %v", err)
		}
		// Every reference must resolve inside the blob.
		for _, ref := range report.References {
			target := findInfo(report, ref.Target)
			switch ref.Kind {
			case KindLength:
				if int(ref.Value) != target.Length {
					t.Fatalf("length mismatch for %s", ref.Target)
				}
			case KindAbsOffset:
				if int(ref.Value) != target.Start {
					t.Fatalf("abs mismatch for %s", ref.Target)
				}
			case KindRelOffset:
				if ref.Position+int(ref.Value) != target.Start {
					t.Fatalf("rel mismatch for %s", ref.Target)
				}
			}
		}
		if !equalSnapshot(snapshot(sch), before) {
			t.Fatal("successful candidate mutated original schema")
		}
	})
}
