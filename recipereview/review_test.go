package recipereview

import (
	"bytes"
	"testing"
)

func mustAlphabet(t *testing.T, b []byte) *Alphabet {
	t.Helper()
	a, err := NewAlphabet(b)
	if err != nil {
		t.Fatalf("NewAlphabet: %v", err)
	}
	return a
}

func findVerdict(t *testing.T, rep *Report, name string) *Verdict {
	t.Helper()
	if v := verdictNamed(rep, name); v != nil {
		return v
	}
	t.Fatalf("verdict %q not found", name)
	return nil
}

func verdictNamed(rep *Report, name string) *Verdict {
	for _, v := range rep.Verdicts {
		if v.Property == name {
			return v
		}
	}
	return nil
}

func prefixRecipe(id int, purpose, version, field string, alpha *Alphabet) *Recipe {
	return &Recipe{
		ID:      id,
		Purpose: purpose,
		Version: version,
		Fields: map[string]*Field{
			field: {Name: field, Alphabet: alpha, MinLen: 0, MaxLen: 1},
		},
		Root: &PrefixExpr{Field: field},
	}
}

// TestRepeatedFieldsAndIndependentEncoders exercises a field referenced twice
// and cross-checks the recursive encoder, opcode encoder and decoder over the
// whole enumerated domain.
func TestRepeatedFieldsAndIndependentEncoders(t *testing.T) {
	alpha := mustAlphabet(t, []byte{0x00, 0x01})
	r := &Recipe{
		ID:      1,
		Purpose: "aes-wrap",
		Version: "v1",
		Fields: map[string]*Field{
			"x": {Name: "x", Alphabet: alpha, MinLen: 0, MaxLen: 2},
		},
		Root: &SeqExpr{Parts: []Expr{
			&ConstExpr{Value: []byte{0xaa}},
			&PrefixExpr{Field: "x"},
			&EscapeExpr{Field: "x", Escape: 0x00, Terminator: 0x01},
		}},
	}
	if err := ValidateRecipe(r); err != nil {
		t.Fatalf("ValidateRecipe: %v", err)
	}
	prog, err := r.CompileProgram()
	if err != nil {
		t.Fatalf("CompileProgram: %v", err)
	}
	for _, in := range fieldValues(r.Fields["x"]) {
		assignment := Assignment{"x": append([]byte(nil), in...)}
		info, err := r.Encode(assignment)
		if err != nil {
			t.Fatalf("Encode %v: %v", in, err)
		}
		alt, err := prog.Encode(assignment)
		if err != nil {
			t.Fatalf("program Encode %v: %v", in, err)
		}
		if !bytes.Equal(info, alt) {
			t.Fatalf("encoders disagree on %v: %x vs %x", in, info, alt)
		}
		decoded, err := r.Decode(info)
		if err != nil {
			t.Fatalf("Decode %x: %v", info, err)
		}
		if !bytes.Equal(decoded["x"], in) {
			t.Fatalf("round trip mismatch: got %v want %v", decoded["x"], in)
		}
	}

	report, err := Review([]*Recipe{r}, NewCompatTable(), nil)
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if !report.Safe() {
		t.Fatalf("expected safe report, got %+v", report.Verdicts)
	}
}

// TestZeroLengthBranchCollision builds two branch arms that collapse onto the
// same bytes when the branch field is empty versus non-empty.
func TestZeroLengthBranchCollision(t *testing.T) {
	alpha := mustAlphabet(t, []byte{0x00, 0x01})
	r := &Recipe{
		ID:      2,
		Purpose: "branching",
		Version: "v1",
		Fields: map[string]*Field{
			"x": {Name: "x", Alphabet: alpha, MinLen: 0, MaxLen: 1},
			"y": {Name: "y", Alphabet: alpha, MinLen: 0, MaxLen: 1},
		},
		Root: &BranchExpr{
			Field:      "x",
			IfEmpty:    &PrefixExpr{Field: "y"},
			IfNonEmpty: &PrefixExpr{Field: "x"},
		},
	}
	// x empty, y=0x00 -> [0x01,0x00]; x=0x00, y empty -> [0x01,0x00].
	// Explicitly assert the intended collision shape first.
	infoEmpty, err := r.Encode(Assignment{"x": {}, "y": {0x00}})
	if err != nil {
		t.Fatalf("encode empty arm: %v", err)
	}
	infoNonEmpty, err := r.Encode(Assignment{"x": {0x00}, "y": {}})
	if err != nil {
		t.Fatalf("encode non-empty arm: %v", err)
	}
	if !bytes.Equal(infoEmpty, infoNonEmpty) {
		t.Fatalf("test setup not a collision: %x vs %x", infoEmpty, infoNonEmpty)
	}

	report, err := Review([]*Recipe{r}, NewCompatTable(), &Options{Budget: NewBudget(50_000)})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	v := findVerdict(t, report, "injective:2(branching/v1)")
	if v.Kind != VerdictConflict || v.Witness == nil {
		t.Fatalf("expected conflict, got kind=%d reason=%q", v.Kind, v.Reason)
	}
	w := v.Witness
	if w.Pair.totalLen != 2 {
		t.Fatalf("expected shortest witness total length 2, got %d", w.Pair.totalLen)
	}
	if len(w.First["x"]) != 0 || !bytes.Equal(w.First["y"], []byte{0x00}) {
		t.Fatalf("unexpected empty-arm assignment: %+v", w.First)
	}
	if !bytes.Equal(w.Second["x"], []byte{0x00}) || len(w.Second["y"]) != 0 {
		t.Fatalf("unexpected non-empty-arm assignment: %+v", w.Second)
	}
	if !bytes.Equal(w.FirstInfo, w.SecondInfo) {
		t.Fatalf("witness info strings differ")
	}
	if result := VerifyWitness([]*Recipe{r}, NewCompatTable(), w); !result.Valid {
		t.Fatalf("witness failed independent verification: %s", result.Reason)
	}
	if _, err := r.Decode(w.FirstInfo); err == nil {
		t.Fatalf("ambiguous encoding was accepted by Decode")
	}
}
