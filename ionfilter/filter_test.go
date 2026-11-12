package ionfilter

import (
	"errors"
	"testing"
)

func violationKind(t *testing.T, err error) Kind {
	t.Helper()
	if err == nil {
		t.Fatal("expected a violation, got nil")
	}
	var v *Violation
	if !errors.As(err, &v) {
		t.Fatalf("expected *Violation, got %T: %v", err, err)
	}
	return v.Kind
}

func TestText_AllowedAnnotationsPass(t *testing.T) {
	stream := []byte(`$ion_1_0
{
  user_id: 42,
  profile: audit:: { role: "admin" }
}`)
	err := Validate(stream, false, []string{"audit"})
	if err != nil {
		t.Fatalf("legitimate stream rejected: %v", err)
	}
}

func TestText_AnnotationOutsideAllowListRejected(t *testing.T) {
	stream := []byte(`{ profile: evil:: { role: "admin" } }`)
	err := Validate(stream, false, []string{"audit"})
	if kind := violationKind(t, err); kind != KindAnnotation {
		t.Fatalf("got kind %q, want %q (err=%v)", kind, KindAnnotation, err)
	}
}

func TestText_EmptyAllowListRejectsEveryAnnotation(t *testing.T) {
	stream := []byte(`audit:: 1`)
	err := Validate(stream, false, nil)
	if kind := violationKind(t, err); kind != KindAnnotation {
		t.Fatalf("got kind %q, want %q (err=%v)", kind, KindAnnotation, err)
	}
}

func TestText_EmbeddedIVMRejected(t *testing.T) {
	stream := []byte(`{ role: "user" } $ion_1_0 { role: "admin" }`)
	err := Validate(stream, false, []string{"audit"})
	if kind := violationKind(t, err); kind != KindIVM {
		t.Fatalf("got kind %q, want %q (err=%v)", kind, KindIVM, err)
	}
}

func TestText_LeadingIVMAccepted(t *testing.T) {
	stream := []byte("$ion_1_0 1 2 3")
	if err := Validate(stream, false, nil); err != nil {
		t.Fatalf("leading IVM must be accepted: %v", err)
	}
}

func TestText_SymbolTableDirectiveInUserDataRejected(t *testing.T) {
	// The classic rebinding attack: an uploaded value carries its own local
	// symbol table to remap SIDs for everything that follows it.
	stream := []byte(`
$ion_symbol_table::{
  symbols: ["evil_role"]
}
evil_role:: "root"`)
	err := New(nil, UserSegment(stream)).Validate()
	if kind := violationKind(t, err); kind != KindSymbolTable {
		t.Fatalf("got kind %q, want %q (err=%v)", kind, KindSymbolTable, err)
	}
}

func TestText_QuotedDirectiveInUserDataRejected(t *testing.T) {
	stream := []byte("'$ion_symbol_table'::{symbols:[\"x\"]}")
	err := New(nil, UserSegment(stream)).Validate()
	if kind := violationKind(t, err); kind != KindSymbolTable {
		t.Fatalf("got kind %q, want %q (err=%v)", kind, KindSymbolTable, err)
	}
}

func TestText_DirectiveViaSymbolIDReferenceRejected(t *testing.T) {
	// $3 is the fixed SID of $ion_symbol_table. Referencing it by number
	// must not evade the directive name check.
	stream := []byte(`$3::{symbols:["evil_role"]}`)
	err := New(nil, UserSegment(stream)).Validate()
	if kind := violationKind(t, err); kind != KindSymbolTable {
		t.Fatalf("got kind %q, want %q (err=%v)", kind, KindSymbolTable, err)
	}
}

func TestText_MidstreamRebindDoesNotChangeAllowList(t *testing.T) {
	// Even if a trusted header binds "audit", an attempted second directive
	// inside user data is rejected before any rebinding can happen.
	trusted := []byte(`$ion_symbol_table::{symbols:["audit"]}`)
	user := []byte(`
$ion_symbol_table::{symbols:["audit"]}
audit:: "ok"`)
	err := New([]string{"audit"}, TrustedSegment(trusted), UserSegment(user)).Validate()
	if kind := violationKind(t, err); kind != KindSymbolTable {
		t.Fatalf("got kind %q, want %q (err=%v)", kind, KindSymbolTable, err)
	}
}

func TestText_TrustedDirectiveBindsSymbols(t *testing.T) {
	trusted := []byte(`$ion_symbol_table::{symbols:["audit"]}`)
	user := []byte(`audit:: { role: "admin" }`)
	err := New([]string{"audit"}, TrustedSegment(trusted), UserSegment(user)).Validate()
	if err != nil {
		t.Fatalf("trusted directive should bind symbols and allow the value: %v", err)
	}
}

func TestText_TrustBoundaryStopsDirective(t *testing.T) {
	trusted := []byte(`$ion_symbol_table::{symbols:["audit"]}`)
	// Same directive text, but this copy lives in the uploaded segment.
	user := []byte(`$ion_symbol_table::{symbols:["evil_role"]}`)
	err := New(nil, TrustedSegment(trusted), UserSegment(user)).Validate()
	if kind := violationKind(t, err); kind != KindSymbolTable {
		t.Fatalf("got kind %q, want %q (err=%v)", kind, KindSymbolTable, err)
	}
}

func TestText_TrustedSymbolNotInAllowListStillRejected(t *testing.T) {
	trusted := []byte(`$ion_symbol_table::{symbols:["internal_tag"]}`)
	user := []byte(`internal_tag:: 7`)
	err := New([]string{"audit"}, TrustedSegment(trusted), UserSegment(user)).Validate()
	if kind := violationKind(t, err); kind != KindAnnotation {
		t.Fatalf("got kind %q, want %q (err=%v)", kind, KindAnnotation, err)
	}
}

func TestText_MalformedInputRejected(t *testing.T) {
	cases := [][]byte{
		[]byte(`{ unclosed: 1`),
		[]byte(`[1, 2`),
		[]byte(`'unclosed symbol`),
		[]byte(`{ : 1 }`),
	}
	for _, input := range cases {
		err := Validate(input, false, nil)
		if kind := violationKind(t, err); kind != KindMalformed {
			t.Fatalf("input %q: got kind %q, want %q (err=%v)", input, kind, KindMalformed, err)
		}
	}
}

func TestText_NestedDirectiveInUserDataRejected(t *testing.T) {
	stream := []byte(`{ wrapper: $ion_symbol_table::{symbols:["x"]} }`)
	err := New(nil, UserSegment(stream)).Validate()
	if kind := violationKind(t, err); kind != KindSymbolTable {
		t.Fatalf("got kind %q, want %q (err=%v)", kind, KindSymbolTable, err)
	}
}

func TestText_SharedTableDirectiveInUserDataRejected(t *testing.T) {
	stream := []byte(`$ion_shared_symbol_table::{name:"foo", version:1, symbols:["bar"]}`)
	err := New([]string{"audit"}, UserSegment(stream)).Validate()
	if kind := violationKind(t, err); kind != KindSymbolTable {
		t.Fatalf("got kind %q, want %q (err=%v)", kind, KindSymbolTable, err)
	}
}

func TestText_AllowListDoesNotGrantDirectiveTrust(t *testing.T) {
	// Listing the directive name in the allow list must not make a user
	// segment directive acceptable.
	stream := []byte(`$ion_symbol_table::{symbols:["x"]}`)
	err := New([]string{annSymbolTable}, UserSegment(stream)).Validate()
	if kind := violationKind(t, err); kind != KindSymbolTable {
		t.Fatalf("got kind %q, want %q (err=%v)", kind, KindSymbolTable, err)
	}
}

func TestText_PlainDataWithoutAnnotationsAccepted(t *testing.T) {
	stream := []byte(`[1, "two", true, null, {a: 3}]`)
	if err := Validate(stream, false, nil); err != nil {
		t.Fatalf("annotation-free data must pass: %v", err)
	}
}

func TestText_SymbolIDZeroAnnotationRejected(t *testing.T) {
	stream := []byte(`$0:: 1`)
	err := New(nil, UserSegment(stream)).Validate()
	if kind := violationKind(t, err); kind != KindAnnotation {
		t.Fatalf("got kind %q, want %q (err=%v)", kind, KindAnnotation, err)
	}
}
