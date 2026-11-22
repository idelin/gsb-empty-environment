package recipereview

import "testing"

// TestVerifyWitnessRejectsTamperedBytes ensures the verifier recomputes
// everything instead of trusting witness-carried info bytes.
func TestVerifyWitnessRejectsTamperedBytes(t *testing.T) {
	alpha := mustAlphabet(t, []byte{0x00, 0x01})
	r1 := prefixRecipe(20, "p1", "v1", "x", alpha)
	r2 := prefixRecipe(21, "p2", "v1", "y", alpha)
	report, err := Review([]*Recipe{r1, r2}, NewCompatTable(), nil)
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	w := findVerdict(t, report, "distinct:20:21").Witness
	if result := VerifyWitness([]*Recipe{r1, r2}, NewCompatTable(), w); !result.Valid {
		t.Fatalf("genuine witness rejected: %s", result.Reason)
	}

	tampered := *w
	tampered.FirstInfo = []byte{0x77}
	if result := VerifyWitness([]*Recipe{r1, r2}, NewCompatTable(), &tampered); result.Valid {
		t.Fatalf("verifier trusted tampered info bytes")
	}

	if result := VerifyWitness([]*Recipe{r1, r2}, NewCompatTable(), nil); result.Valid {
		t.Fatalf("nil witness accepted")
	}

	unknown := *w
	unknown.FirstRecipeID = 999
	if result := VerifyWitness([]*Recipe{r1, r2}, NewCompatTable(), &unknown); result.Valid {
		t.Fatalf("unknown recipe witness accepted")
	}
}

// TestDecodeRejectsGarbage covers constant mismatch, prefix overrun and escape
// framing failures across the binary alphabet boundary.
func TestDecodeRejectsGarbage(t *testing.T) {
	alpha := mustAlphabet(t, []byte{0x00, 0x01})
	r := &Recipe{
		ID:      22,
		Purpose: "decode-guard",
		Version: "v1",
		Fields: map[string]*Field{
			"x": {Name: "x", Alphabet: alpha, MinLen: 0, MaxLen: 2},
		},
		Root: &SeqExpr{Parts: []Expr{
			&ConstExpr{Value: []byte{0x01}},
			&EscapeExpr{Field: "x", Escape: 0x00, Terminator: 0x01},
		}},
	}
	for _, garbage := range [][]byte{
		{0x00, 0x01},             // wrong leading constant
		{0x01, 0x02, 0x01},       // byte outside alphabet
		{0x01, 0x00, 0x02, 0x01}, // escape of a non-special byte
		{0x01, 0x00},             // dangling escape
		{0x01, 0x01, 0x01, 0x01}, // early terminator leaves trailing bytes
	} {
		if _, err := r.Decode(garbage); err == nil {
			t.Fatalf("decoder accepted garbage %x", garbage)
		}
	}

	prefix := prefixRecipe(23, "prefix-guard", "v1", "x", alpha)
	if _, err := prefix.Decode([]byte{0x07, 0x00}); err == nil {
		t.Fatalf("decoder accepted length prefix overrun")
	}
}

// TestFixedLengthRawFullEnumeration proves unique decoding for a fixed-length
// raw field over the whole binary alphabet, including repeated references.
func TestFixedLengthRawFullEnumeration(t *testing.T) {
	alpha := mustAlphabet(t, []byte{0x00, 0x01})
	r := &Recipe{
		ID:      24,
		Purpose: "fixed-raw",
		Version: "v1",
		Fields: map[string]*Field{
			"x": {Name: "x", Alphabet: alpha, MinLen: 2, MaxLen: 2},
		},
		Root: &SeqExpr{Parts: []Expr{
			&ConstExpr{Value: []byte{0x05}},
			&RawExpr{Field: "x"},
			&RawExpr{Field: "x"},
		}},
	}
	report, err := Review([]*Recipe{r}, NewCompatTable(), nil)
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if !report.Safe() {
		t.Fatalf("fixed raw recipe unsafe: %+v", report.Verdicts)
	}
	for _, v := range fieldValues(r.Fields["x"]) {
		in := Assignment{"x": v}
		info, err := r.Encode(in)
		if err != nil {
			t.Fatalf("Encode: %v", err)
		}
		decoded, err := r.Decode(info)
		if err != nil {
			t.Fatalf("Decode %x: %v", info, err)
		}
		if !assignmentsEqual(r, decoded, in) {
			t.Fatalf("round trip %v -> %v", in, decoded)
		}
	}
}

// TestInvalidAssignments checks the assignment-level validation gate.
func TestInvalidAssignments(t *testing.T) {
	alpha := mustAlphabet(t, []byte{0x00, 0x01})
	r := prefixRecipe(25, "assignment-guard", "v1", "x", alpha)
	if _, err := r.Encode(Assignment{}); err == nil {
		t.Fatalf("missing field accepted")
	}
	if _, err := r.Encode(Assignment{"x": []byte{0x00, 0x01}}); err == nil {
		t.Fatalf("over-long value accepted")
	}
	big, _ := NewAlphabet([]byte{0x02})
	other := &Recipe{
		ID:      26,
		Purpose: "other",
		Version: "v1",
		Fields:  map[string]*Field{"z": {Name: "z", Alphabet: big, MinLen: 0, MaxLen: 1}},
		Root:    &PrefixExpr{Field: "z"},
	}
	if _, err := other.Encode(Assignment{"z": []byte{0x00}}); err == nil {
		t.Fatalf("out-of-alphabet byte accepted")
	}
}
