package netsec

import (
	"errors"
	"testing"
)

func TestBenignInternalEntitiesRoundTrip(t *testing.T) {
	doc := `<!DOCTYPE root [
		<!ENTITY org "Example &quot;Org&quot;">
		<!ENTITY greeting "Hello &org;">
	]><root>&greeting;</root>`

	guard := New(1<<20, nil)
	if err := guard.Check([]byte(doc)); err != nil {
		t.Fatalf("benign DTD rejected: %v", err)
	}
	table := guard.Entities()
	if table["greeting"] != "Hello Example \"Org\"" {
		t.Fatalf("nested expansion = %q", table["greeting"])
	}

	dec, err := guard.NewDecoder([]byte(doc))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	var root struct {
		Inner string `xml:",chardata"`
	}
	if err := dec.Decode(&root); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if root.Inner != `Hello Example "Org"` {
		t.Fatalf("decoded text = %q", root.Inner)
	}
}

func TestStandardDecoderRejectsExternalEntityOnItsOwn(t *testing.T) {
	// Defense in depth: confirm the guarded path rejects before the
	// standard library ever sees the external entity.
	doc := []byte(`<!DOCTYPE root [<!ENTITY xxe SYSTEM "file:///etc/passwd">]><root>&xxe;</root>`)
	err := New(1<<20, nil).Check(doc)
	if !errors.Is(err, ErrExternalEntity) {
		t.Fatalf("want ErrExternalEntity, got %v", err)
	}
	if _, derr := New(1<<20, nil).NewDecoder(doc); !errors.Is(derr, ErrExternalEntity) {
		t.Fatalf("NewDecoder should propagate the guard error, got %v", derr)
	}
}

func TestExpandedTableCannotReintroduceMarkup(t *testing.T) {
	// Even an entity whose value textually contains another reference is
	// stored fully expanded, so the standard decoder never re-parses it.
	doc := []byte(`<!DOCTYPE root [
		<!ENTITY inner "INNER">
		<!ENTITY outer "x&inner;y">
	]><root>&outer;</root>`)
	guard := New(1<<20, nil)
	dec, err := guard.NewDecoder(doc)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	var root struct {
		Inner string `xml:",chardata"`
	}
	if err := dec.Decode(&root); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if root.Inner != "xINNERy" {
		t.Fatalf("decoded text = %q", root.Inner)
	}

	// A value containing the literal text of an external entity reference
	// is inert because it never reaches the parser as markup.
	inert := []byte(`<!DOCTYPE root [<!ENTITY a "not an &amp;xxe; SYSTEM call">]><root>&a;</root>`)
	guard2 := New(1<<20, nil)
	if err := guard2.Check(inert); err != nil {
		t.Fatalf("inert entity rejected: %v", err)
	}
	if got := guard2.Entities()["a"]; got != "not an &xxe; SYSTEM call" {
		t.Fatalf("inert expansion = %q", got)
	}
}

func TestResolverCallbackSemantics(t *testing.T) {
	// No DOCTYPE: the callback is never invoked, including via NewDecoder.
	spy := &resolveSpy{}
	guard := New(1<<20, spy.resolve)
	dec, err := guard.NewDecoder([]byte(samlMetadata))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	if _, err := dec.Token(); err != nil {
		t.Fatalf("token: %v", err)
	}
	if spy.calls != 0 {
		t.Fatalf("resolver called %d times for DOCTYPE-free document", spy.calls)
	}

	// Rejected attack documents never reach the resolver either.
	attacks := []string{
		`<!DOCTYPE root [<!ENTITY xxe SYSTEM "file:///etc/passwd">]><root/>`,
		`<!DOCTYPE root [<!ENTITY % pe "x">]><root/>`,
		`<!DOCTYPE root [<!ENTITY a "&a;">]><root/>`,
	}
	for idx, doc := range attacks {
		local := &resolveSpy{}
		g := New(1<<20, local.resolve)
		if err := g.Check([]byte(doc)); err == nil {
			t.Fatalf("attack %d was accepted", idx)
		}
		if local.calls != 0 {
			t.Fatalf("attack %d invoked resolver %d times", idx, local.calls)
		}
	}

	// An optional caller resolver is only consulted for an undeclared name
	// inside an otherwise internal DTD, and its output is budget bounded.
	g := New(1<<20, func(name string) (string, bool) {
		if name == "provided" {
			return "TRUSTED", true
		}
		return "", false
	})
	doc := `<!DOCTYPE root [<!ENTITY a "x&provided;y">]><root>&a;</root>`
	if err := g.Check([]byte(doc)); err != nil {
		t.Fatalf("resolver-backed entity rejected: %v", err)
	}
	if got := g.Entities()["a"]; got != "xTRUSTEDy" {
		t.Fatalf("resolver value = %q", got)
	}

	missing := `<!DOCTYPE root [<!ENTITY a "&ghost;">]><root/>`
	missingSpy := &resolveSpy{}
	if err := New(1<<20, missingSpy.resolve).Check([]byte(missing)); !errors.Is(err, ErrUndeclaredEntity) {
		t.Fatalf("want ErrUndeclaredEntity, got %v", err)
	}
	if missingSpy.calls != 1 || len(missingSpy.names) != 1 || missingSpy.names[0] != "ghost" {
		t.Fatalf("resolver spy = %+v", missingSpy)
	}

	// Resolver output is charged against the same budget.
	limited := New(4, func(name string) (string, bool) {
		return "TOOLONG", true
	})
	if err := limited.Check([]byte(doc)); !errors.Is(err, ErrEntityLimit) {
		t.Fatalf("resolver output must be budget bounded, got %v", err)
	}
}

func TestPositionErrorUnwraps(t *testing.T) {
	err := New(1<<20, nil).Check([]byte(`<!DOCTYPE root [<!ENTITY x SYSTEM "x">]><r/>`))
	var pe *PositionError
	if !errors.As(err, &pe) {
		t.Fatalf("want *PositionError, got %T", err)
	}
	if !errors.Is(err, ErrExternalEntity) {
		t.Fatalf("want ErrExternalEntity via unwrap, got %v", err)
	}
	if pe.Offset <= 0 {
		t.Fatalf("offset not recorded: %+v", pe)
	}
}

func TestValidateConvenienceAndEmptyInput(t *testing.T) {
	if err := Validate([]byte("<root/>"), 1<<20, nil); err != nil {
		t.Fatalf("simple document rejected: %v", err)
	}
	for _, input := range [][]byte{nil, {}, []byte("   "), []byte("garbage")} {
		if err := Validate(input, 1<<20, nil); !errors.Is(err, ErrNoProlog) {
			t.Fatalf("input %q: want ErrNoProlog, got %v", input, err)
		}
	}
}
