package netsec

import "testing"

func fuzzSchemas() []Type {
	return []Type{
		Uint32Type,
		BoolType,
		MustListType(Uint16Type, 8),
		MustListType(MustListType(Uint8Type, 4), 4),
		MustBitlistType(16),
		MustBitvectorType(10),
		MustContainerType([]Field{
			{Name: "a", Type: Uint32Type},
			{Name: "b", Type: MustListType(Uint64Type, 4)},
		}),
		MustUnionType([]Type{Uint8Type, MustListType(Uint8Type, 4)}),
	}
}

func FuzzDecodeNoPanic(f *testing.F) {
	schemas := fuzzSchemas()
	f.Add([]byte{1, 2, 3, 4})
	f.Add([]byte{8, 0, 0, 0})
	f.Add([]byte{0x00})
	f.Fuzz(func(t *testing.T, data []byte) {
		for _, schema := range schemas {
			v, err := Decode(schema, data)
			if err == nil && v == nil {
				t.Fatalf("Decode returned nil value with nil error")
			}
			if err == nil && len(v.raw) == 0 && schema.FixedSize() != 0 {
				t.Fatalf("fixed-size value decoded with empty raw")
			}
		}
	})
}
