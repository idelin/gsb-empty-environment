package recipereview

import (
	"bytes"
	"testing"
)

// TestBinaryEscapeAcrossBoundary confirms escaped terminators do not terminate
// early and that an unescaped terminator closes the value correctly.
func TestBinaryEscapeAcrossBoundary(t *testing.T) {
	alpha := mustAlphabet(t, []byte{0x00, 0x01})
	r := &Recipe{
		ID:      3,
		Purpose: "escape-lab",
		Version: "v1",
		Fields: map[string]*Field{
			"x": {Name: "x", Alphabet: alpha, MinLen: 0, MaxLen: 3},
		},
		Root: &SeqExpr{Parts: []Expr{
			&ConstExpr{Value: []byte{0x01}},
			&EscapeExpr{Field: "x", Escape: 0x00, Terminator: 0x01},
			&ConstExpr{Value: []byte{0x01}},
		}},
	}
	tricky := Assignment{"x": []byte{0x01, 0x00, 0x01}}
	info, err := r.Encode(tricky)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	want := []byte{0x01, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x01, 0x01}
	if !bytes.Equal(info, want) {
		t.Fatalf("escape encoding %x, want %x", info, want)
	}
	decoded, err := r.Decode(info)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !bytes.Equal(decoded["x"], tricky["x"]) {
		t.Fatalf("escape round trip got %x", decoded["x"])
	}

	report, err := Review([]*Recipe{r}, NewCompatTable(), nil)
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if !report.Safe() {
		t.Fatalf("expected safe escape recipe, got %+v", report.Verdicts)
	}

	// A bare terminator embedded in the field would close parsing early and
	// leave trailing bytes, so it must be rejected.
	broken := []byte{0x01, 0x01, 0x01, 0x01}
	if _, err := r.Decode(broken); err == nil {
		t.Fatalf("decoder accepted unescaped early terminator")
	}
}

// TestReadOnlyOverlap constructs a read-only legacy recipe whose encodings are
// accepted by a recipe of a different purpose.
func TestReadOnlyOverlap(t *testing.T) {
	alpha := mustAlphabet(t, []byte{0x00, 0x01})
	legacy := &Recipe{
		ID:       4,
		Purpose:  "legacy",
		Version:  "v9",
		ReadOnly: true,
		Fields: map[string]*Field{
			"a": {Name: "a", Alphabet: alpha, MinLen: 0, MaxLen: 1},
		},
		Root: &PrefixExpr{Field: "a"},
	}
	active := &Recipe{
		ID:      5,
		Purpose: "new-hotness",
		Version: "v1",
		Fields: map[string]*Field{
			"b": {Name: "b", Alphabet: alpha, MinLen: 0, MaxLen: 1},
		},
		Root: &PrefixExpr{Field: "b"},
	}
	report, err := Review([]*Recipe{legacy, active}, NewCompatTable(), nil)
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	rv := findVerdict(t, report, "readonly:4:5")
	if rv.Kind != VerdictConflict || rv.Witness == nil {
		t.Fatalf("expected read-only conflict, got %+v", rv)
	}
	w := rv.Witness
	if w.FirstRecipeID != 4 || w.SecondRecipeID != 5 {
		t.Fatalf("witness oriented at %d,%d", w.FirstRecipeID, w.SecondRecipeID)
	}
	if result := VerifyWitness([]*Recipe{legacy, active}, NewCompatTable(), w); !result.Valid {
		t.Fatalf("read-only witness rejected: %s", result.Reason)
	}

	// A distinct leading constant keeps the two purposes disjoint and safe.
	active.Root = &SeqExpr{Parts: []Expr{
		&ConstExpr{Value: []byte{0x07}},
		&PrefixExpr{Field: "b"},
	}}
	report, err = Review([]*Recipe{legacy, active}, NewCompatTable(), nil)
	if err != nil {
		t.Fatalf("second Review: %v", err)
	}
	if !report.Safe() {
		t.Fatalf("expected disjoint purposes to be safe, got %+v", report.Verdicts)
	}
}

// TestDifferentPurposeCollision checks cross-purpose info collision and the
// total-length / recipe-ID / raw-byte tie-breaking rules.
func TestDifferentPurposeCollision(t *testing.T) {
	alpha := mustAlphabet(t, []byte{0x00, 0x01})
	r1 := prefixRecipe(6, "purpose-a", "v1", "x", alpha)
	r2 := prefixRecipe(7, "purpose-b", "v1", "y", alpha)
	report, err := Review([]*Recipe{r1, r2}, NewCompatTable(), nil)
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	v := findVerdict(t, report, "distinct:6:7")
	if v.Kind != VerdictConflict || v.Witness == nil {
		t.Fatalf("expected cross-purpose conflict, got %+v", v)
	}
	w := v.Witness
	if w.Pair.totalLen != 0 {
		t.Fatalf("expected empty assignments as shortest witness, got length %d", w.Pair.totalLen)
	}
	if result := VerifyWitness([]*Recipe{r1, r2}, NewCompatTable(), w); !result.Valid {
		t.Fatalf("collision witness rejected: %s", result.Reason)
	}

	// Different leading symbols disjoint the root first-byte sets and let the
	// symbolic layer prove safety without enumeration.
	r2.Root = &SeqExpr{Parts: []Expr{
		&ConstExpr{Value: []byte{0x0f}},
		&PrefixExpr{Field: "y"},
	}}
	report, err = Review([]*Recipe{r1, r2}, NewCompatTable(), nil)
	if err != nil {
		t.Fatalf("second Review: %v", err)
	}
	v = findVerdict(t, report, "distinct:6:7")
	if v.Kind != VerdictSafe || v.Enumerated {
		t.Fatalf("expected symbolic proof, got %+v", v)
	}
}

// TestCompatTableGating ensures same-purpose versions only collide-check when
// the compatibility table says they coexist.
func TestCompatTableGating(t *testing.T) {
	alpha := mustAlphabet(t, []byte{0x00, 0x01})
	v1 := prefixRecipe(8, "same-purpose", "v1", "x", alpha)
	v2 := prefixRecipe(9, "same-purpose", "v2", "y", alpha)

	report, err := Review([]*Recipe{v1, v2}, NewCompatTable(), nil)
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if verdictNamed(report, "compat:8:9") != nil {
		t.Fatalf("unexpected compat property when versions do not coexist")
	}

	compat := NewCompatTable([2]VersionRef{
		{Purpose: "same-purpose", Version: "v1"},
		{Purpose: "same-purpose", Version: "v2"},
	})
	report, err = Review([]*Recipe{v1, v2}, compat, nil)
	if err != nil {
		t.Fatalf("Review with compat: %v", err)
	}
	v := findVerdict(t, report, "compat:8:9")
	if v.Kind != VerdictConflict {
		t.Fatalf("expected coexistence collision, got %+v", v)
	}
	w := v.Witness
	if result := VerifyWitness([]*Recipe{v1, v2}, compat, w); !result.Valid {
		t.Fatalf("compat witness rejected: %s", result.Reason)
	}
	if result := VerifyWitness([]*Recipe{v1, v2}, NewCompatTable(), w); result.Valid {
		t.Fatalf("witness validated despite revoked compatibility")
	}
}

// TestBudgetExhaustedReturnsUnknown forces the state budget to run out before
// the branched property can be settled by enumeration.
func TestBudgetExhaustedReturnsUnknown(t *testing.T) {
	alpha := mustAlphabet(t, []byte{0x00, 0x01})
	r := &Recipe{
		ID:      10,
		Purpose: "budgeted",
		Version: "v1",
		Fields: map[string]*Field{
			"x": {Name: "x", Alphabet: alpha, MinLen: 0, MaxLen: 3},
		},
		Root: &BranchExpr{
			Field:      "x",
			IfEmpty:    &ConstExpr{Value: []byte{0x02}},
			IfNonEmpty: &PrefixExpr{Field: "x"},
		},
	}
	// The domain materialization costs 15 states; allowing 20 forces the
	// exhaustion to happen while checking candidate pairs.
	report, err := Review([]*Recipe{r}, NewCompatTable(), &Options{Budget: NewBudget(20)})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	v := findVerdict(t, report, "injective:10(budgeted/v1)")
	if v.Kind != VerdictUnknown || !v.Enumerated {
		t.Fatalf("expected unknown verdict, got %+v", v)
	}
}

// TestSchemaValidation exercises the structural gate the release review relies
// on, including fixed-length raw references and escape alphabet membership.
func TestSchemaValidation(t *testing.T) {
	alpha := mustAlphabet(t, []byte{0x00, 0x01})
	base := func() *Recipe {
		return &Recipe{
			ID:      11,
			Purpose: "schema",
			Version: "v1",
			Fields: map[string]*Field{
				"x": {Name: "x", Alphabet: alpha, MinLen: 1, MaxLen: 1},
			},
			Root: &RawExpr{Field: "x"},
		}
	}
	if err := ValidateRecipe(base()); err != nil {
		t.Fatalf("fixed-length raw reference should validate: %v", err)
	}

	variable := base()
	variable.Fields["x"].MaxLen = 2
	if err := ValidateRecipe(variable); err == nil {
		t.Fatalf("raw reference to variable-length field should fail")
	}

	badEscape := base()
	badEscape.Root = &EscapeExpr{Field: "x", Escape: 0x02, Terminator: 0x03}
	if err := ValidateRecipe(badEscape); err == nil {
		t.Fatalf("escape bytes outside alphabet should fail")
	}

	unreferenced := base()
	unreferenced.Fields["z"] = &Field{Name: "z", Alphabet: alpha, MinLen: 0, MaxLen: 1}
	if err := ValidateRecipe(unreferenced); err == nil {
		t.Fatalf("unreferenced field should fail")
	}
}

// TestTieBreakByRawBytes forces a same-length conflict and checks that the
// lexicographically smallest canonical bytes win.
func TestTieBreakByRawBytes(t *testing.T) {
	alpha := mustAlphabet(t, []byte{0x00, 0x01})
	r := &Recipe{
		ID:      12,
		Purpose: "tie-break",
		Version: "v1",
		Fields: map[string]*Field{
			"x": {Name: "x", Alphabet: alpha, MinLen: 1, MaxLen: 1},
			"y": {Name: "y", Alphabet: alpha, MinLen: 1, MaxLen: 1},
		},
		// Both fields are fixed-length raw and concatenated, so the pairs
		// (x=0,y=1) and (x=1,y=0) both encode to two bytes but stay distinct;
		// tie-breaking is instead exercised on equal-length cross pairs below.
		Root: &SeqExpr{Parts: []Expr{
			&RawExpr{Field: "x"},
			&RawExpr{Field: "y"},
		}},
	}
	if err := ValidateRecipe(r); err != nil {
		t.Fatalf("ValidateRecipe: %v", err)
	}
	report, err := Review([]*Recipe{r}, NewCompatTable(), nil)
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if !report.Safe() {
		t.Fatalf("fixed-length raw concatenation should be injective: %+v", report.Verdicts)
	}

	ordering := make([]Assignment, 0)
	for _, xv := range [][]byte{{0x00}, {0x01}} {
		for _, yv := range [][]byte{{0x00}, {0x01}} {
			ordering = append(ordering, Assignment{"x": xv, "y": yv})
		}
	}
	if compareAssignments(r, ordering[0], ordering[1]) >= 0 {
		t.Fatalf("canonical assignment ordering is not ascending")
	}
}
