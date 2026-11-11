package wiretypes_test

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"example.org/wiretypes"
)

// Test domain: persons and photos, including a photo subtype.

type Person struct {
	Name    string
	Age     int
	Active  bool
	Manager *Person
	Pals    []*Person
	Avatar  *Image
	Nick    *string
}

type Image struct {
	URL     string
	Width   int
	Owner   *Person
	Related []*Image
}

type Banner struct {
	Headline string
}

func newPerson() any { return &Person{} }
func newImage() any  { return &Image{} }
func newBanner() any { return &Banner{} }

func testSpecs() []wiretypes.TypeSpec {
	return []wiretypes.TypeSpec{
		{
			ID:  "person",
			New: newPerson,
			Fields: []wiretypes.FieldSpec{
				{Name: "Name", Kind: wiretypes.KindString},
				{Name: "Age", Kind: wiretypes.KindInt},
				{Name: "Active", Kind: wiretypes.KindBool},
				{Name: "Manager", Kind: wiretypes.KindRef, RefType: "person", Nullable: true},
				{Name: "Pals", Kind: wiretypes.KindList, Nullable: true, Elem: &wiretypes.FieldSpec{Kind: wiretypes.KindRef, RefType: "person"}},
				{Name: "Avatar", Kind: wiretypes.KindRef, RefType: "image", Nullable: true},
				{Name: "Nick", Kind: wiretypes.KindString, Nullable: true},
			},
		},
		{
			ID:  "image",
			New: newImage,
			Fields: []wiretypes.FieldSpec{
				{Name: "URL", Kind: wiretypes.KindString},
				{Name: "Width", Kind: wiretypes.KindInt},
				{Name: "Owner", Kind: wiretypes.KindRef, RefType: "person", Nullable: true},
				{Name: "Related", Kind: wiretypes.KindList, Nullable: true, Elem: &wiretypes.FieldSpec{Kind: wiretypes.KindRef, RefType: "image"}},
			},
		},
		{
			ID: "photo", Base: "image",
			New: newImage,
			Fields: []wiretypes.FieldSpec{
				{Name: "URL", Kind: wiretypes.KindString},
				{Name: "Width", Kind: wiretypes.KindInt},
				{Name: "Owner", Kind: wiretypes.KindRef, RefType: "person", Nullable: true},
				{Name: "Related", Kind: wiretypes.KindList, Nullable: true, Elem: &wiretypes.FieldSpec{Kind: wiretypes.KindRef, RefType: "image"}},
			},
		},
		{
			ID:  "banner",
			New: newBanner,
			Fields: []wiretypes.FieldSpec{
				{Name: "Headline", Kind: wiretypes.KindString},
			},
		},
	}
}

func fullMode() wiretypes.Mode {
	return wiretypes.Mode{"person": true, "image": true, "photo": true, "banner": true}
}

func mustRegistry(t *testing.T, specs []wiretypes.TypeSpec) *wiretypes.Registry {
	t.Helper()
	r, err := wiretypes.NewRegistry(specs)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	return r
}

func mustDecoder(t *testing.T, r *wiretypes.Registry, mode wiretypes.Mode, budget wiretypes.Budget) *wiretypes.Decoder {
	t.Helper()
	d, err := wiretypes.NewDecoder(r, mode, budget)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	return d
}

type received struct {
	obj any
	t   wiretypes.TypeID
	id  wiretypes.RefID
}

type recorder struct {
	mu       sync.Mutex
	received []received
	reject   wiretypes.RefID
	entered  chan struct{}
	release  chan struct{}
}

func (r *recorder) Check(obj any, t wiretypes.TypeID, id wiretypes.RefID) error {
	if r.entered != nil {
		select {
		case r.entered <- struct{}{}:
		default:
		}
		<-r.release
	}
	if r.reject != "" && id == r.reject {
		return fmt.Errorf("business rule violation")
	}
	return nil
}

func (r *recorder) Receive(obj any, t wiretypes.TypeID, id wiretypes.RefID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.received = append(r.received, received{obj: obj, t: t, id: id})
}

func (r *recorder) snapshot() []received {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]received, len(r.received))
	copy(out, r.received)
	return out
}

func TestDecode_ForwardReferenceAndCycle(t *testing.T) {
	r := mustRegistry(t, testSpecs())
	d := mustDecoder(t, r, fullMode(), wiretypes.Budget{})

	// Root references an object appearing later in the table (forward ref),
	// and both managers point at each other (a cycle).
	msg := &wiretypes.Message{
		Root: "p1",
		Objects: []wiretypes.RawObject{
			{
				ID:   "p1",
				Type: "person",
				Fields: map[string]wiretypes.RawValue{
					"Name":    wiretypes.VStr("ada"),
					"Age":     wiretypes.VInt(36),
					"Active":  wiretypes.VBool(true),
					"Manager": wiretypes.VRef("p2"),
					"Pals":    wiretypes.VList(wiretypes.VRef("p2")),
				},
			},
			{
				ID:   "p2",
				Type: "person",
				Fields: map[string]wiretypes.RawValue{
					"Name":    wiretypes.VStr("grace"),
					"Age":     wiretypes.VInt(41),
					"Active":  wiretypes.VBool(false),
					"Manager": wiretypes.VRef("p1"),
					"Pals":    wiretypes.VList(),
				},
			},
		},
	}

	root, err := d.Decode(msg, wiretypes.NopCallback)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	p1 := root.(*Person)
	if p1.Name != "ada" || p1.Age != 36 || !p1.Active {
		t.Fatalf("scalar decode mismatch: %+v", p1)
	}
	p2 := p1.Manager
	if p2 == nil || p2.Name != "grace" {
		t.Fatalf("forward reference not linked: %+v", p1.Manager)
	}
	if p2.Manager != p1 {
		t.Fatal("cycle not preserved: p2.Manager != p1")
	}
	if len(p1.Pals) != 1 || p1.Pals[0] != p2 {
		t.Fatal("list of references not linked")
	}
	if len(p2.Pals) != 0 {
		t.Fatal("empty list should decode to non-nil empty slice")
	}
}

func TestDecode_AliasSharing(t *testing.T) {
	r := mustRegistry(t, testSpecs())
	d := mustDecoder(t, r, fullMode(), wiretypes.Budget{})

	msg := &wiretypes.Message{
		Root: "p1",
		Objects: []wiretypes.RawObject{
			{
				ID:   "p1",
				Type: "person",
				Fields: map[string]wiretypes.RawValue{
					"Name":   wiretypes.VStr("ada"),
					"Age":    wiretypes.VInt(1),
					"Active": wiretypes.VBool(true),
					"Avatar": wiretypes.VRef("img"),
					"Pals":   wiretypes.VList(wiretypes.VRef("owner"), wiretypes.VRef("owner")),
				},
			},
			{
				ID:   "owner",
				Type: "person",
				Fields: map[string]wiretypes.RawValue{
					"Name":   wiretypes.VStr("linus"),
					"Age":    wiretypes.VInt(0),
					"Active": wiretypes.VBool(true),
				},
			},
			{
				ID:   "img",
				Type: "image",
				Fields: map[string]wiretypes.RawValue{
					"URL":     wiretypes.VStr("https://example.test/a.png"),
					"Width":   wiretypes.VInt(800),
					"Owner":   wiretypes.VRef("owner"),
					"Related": wiretypes.VList(wiretypes.VRef("img")),
				},
			},
		},
	}

	root, err := d.Decode(msg, wiretypes.NopCallback)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	p1 := root.(*Person)
	img := p1.Avatar
	if img == nil || img.URL != "https://example.test/a.png" || img.Width != 800 {
		t.Fatalf("image decode mismatch: %+v", img)
	}
	if img.Owner == nil || img.Owner.Name != "linus" {
		t.Fatal("image owner not linked")
	}
	if len(p1.Pals) != 2 || p1.Pals[0] != p1.Pals[1] || p1.Pals[0] != img.Owner {
		t.Fatal("aliased references must share one instance")
	}
	if len(img.Related) != 1 || img.Related[0] != img {
		t.Fatal("self reference through list must share instance")
	}
}

func TestDecode_NullableAndNullRejection(t *testing.T) {
	r := mustRegistry(t, testSpecs())
	d := mustDecoder(t, r, fullMode(), wiretypes.Budget{})

	msg := &wiretypes.Message{
		Root: "p1",
		Objects: []wiretypes.RawObject{
			{
				ID:   "p1",
				Type: "person",
				Fields: map[string]wiretypes.RawValue{
					"Name":   wiretypes.VStr("ada"),
					"Age":    wiretypes.VInt(1),
					"Active": wiretypes.VBool(true),
					"Nick":   wiretypes.VNull(),
				},
			},
		},
	}
	root, err := d.Decode(msg, wiretypes.NopCallback)
	if err != nil {
		t.Fatalf("Decode with null: %v", err)
	}
	if root.(*Person).Nick != nil {
		t.Fatal("null must set nullable field to zero value")
	}

	msg.Objects[0].Fields["Name"] = wiretypes.VNull()
	_, err = d.Decode(msg, wiretypes.NopCallback)
	if !errors.Is(err, wiretypes.ErrNullNotAllowed) {
		t.Fatalf("expected ErrNullNotAllowed, got %v", err)
	}
}

func TestDecode_NegotiatedModeRejectsType(t *testing.T) {
	r := mustRegistry(t, testSpecs())
	d := mustDecoder(t, r, wiretypes.Mode{"person": true}, wiretypes.Budget{})

	msg := &wiretypes.Message{
		Root: "p1",
		Objects: []wiretypes.RawObject{
			{
				ID:   "p1",
				Type: "person",
				Fields: map[string]wiretypes.RawValue{
					"Name":   wiretypes.VStr("ada"),
					"Age":    wiretypes.VInt(1),
					"Active": wiretypes.VBool(true),
					"Avatar": wiretypes.VRef("img"),
				},
			},
			{
				ID:   "img",
				Type: "image",
				Fields: map[string]wiretypes.RawValue{
					"URL":   wiretypes.VStr("u"),
					"Width": wiretypes.VInt(1),
				},
			},
		},
	}
	_, err := d.Decode(msg, wiretypes.NopCallback)
	if !errors.Is(err, wiretypes.ErrTypeNotNegotiated) {
		t.Fatalf("expected ErrTypeNotNegotiated, got %v", err)
	}

	var got []wiretypes.TypeID
	cb := wiretypes.FuncCallback{
		ReceiveFn: func(obj any, typ wiretypes.TypeID, id wiretypes.RefID) { got = append(got, typ) },
	}
	if _, err := d.Decode(msg, cb); err == nil {
		t.Fatal("expected rejection error")
	}
	if len(got) != 0 {
		t.Fatalf("rejected message must deliver nothing, got %v", got)
	}
}

func TestDecode_BudgetEnforced(t *testing.T) {
	r := mustRegistry(t, testSpecs())

	msg := &wiretypes.Message{
		Root: "p1",
		Objects: []wiretypes.RawObject{
			{
				ID:   "p1",
				Type: "person",
				Fields: map[string]wiretypes.RawValue{
					"Name":    wiretypes.VStr("ada"),
					"Age":     wiretypes.VInt(1),
					"Active":  wiretypes.VBool(true),
					"Manager": wiretypes.VRef("p2"),
					"Pals":    wiretypes.VList(wiretypes.VRef("p2")),
				},
			},
			{
				ID:   "p2",
				Type: "person",
				Fields: map[string]wiretypes.RawValue{
					"Name":    wiretypes.VStr("g"),
					"Age":     wiretypes.VInt(2),
					"Active":  wiretypes.VBool(true),
					"Manager": wiretypes.VRef("p1"),
					"Pals":    wiretypes.VList(),
				},
			},
		},
	}

	cases := []struct {
		name   string
		budget wiretypes.Budget
		want   error
	}{
		{"objects", wiretypes.Budget{MaxObjects: 1}, wiretypes.ErrBudgetExceeded},
		{"refs", wiretypes.Budget{MaxRefs: 2}, wiretypes.ErrBudgetExceeded},
		{"fields", wiretypes.Budget{MaxFields: 9}, wiretypes.ErrBudgetExceeded},
		{"depth", wiretypes.Budget{MaxDepth: 1}, wiretypes.ErrBudgetExceeded},
		{"within-budget", wiretypes.Budget{MaxObjects: 2, MaxRefs: 4, MaxFields: 10, MaxDepth: 3}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := mustDecoder(t, r, fullMode(), tc.budget)
			_, err := d.Decode(msg, wiretypes.NopCallback)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("expected success, got %v", err)
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("expected %v, got %v", tc.want, err)
			}
		})
	}
}

func TestDecode_ValidationFailures(t *testing.T) {
	r := mustRegistry(t, testSpecs())
	d := mustDecoder(t, r, fullMode(), wiretypes.Budget{})

	validPerson := func(id string) wiretypes.RawObject {
		return wiretypes.RawObject{
			ID:   wiretypes.RefID(id),
			Type: "person",
			Fields: map[string]wiretypes.RawValue{
				"Name":   wiretypes.VStr(id),
				"Age":    wiretypes.VInt(1),
				"Active": wiretypes.VBool(true),
			},
		}
	}

	cases := []struct {
		name string
		msg  *wiretypes.Message
		want error
	}{
		{
			"empty message",
			&wiretypes.Message{Root: "x"},
			wiretypes.ErrEmptyMessage,
		},
		{
			"duplicate id",
			&wiretypes.Message{Root: "a", Objects: []wiretypes.RawObject{validPerson("a"), validPerson("a")}},
			wiretypes.ErrDuplicateID,
		},
		{
			"unknown root",
			&wiretypes.Message{Root: "ghost", Objects: []wiretypes.RawObject{validPerson("a")}},
			wiretypes.ErrUnknownRoot,
		},
		{
			"dangling reference",
			&wiretypes.Message{Root: "a", Objects: []wiretypes.RawObject{
				{
					ID:   "a",
					Type: "person",
					Fields: map[string]wiretypes.RawValue{
						"Name":   wiretypes.VStr("a"),
						"Age":    wiretypes.VInt(1),
						"Active": wiretypes.VBool(true),
						"Avatar": wiretypes.VRef("missing"),
					},
				},
			}},
			wiretypes.ErrUnknownRef,
		},
		{
			"kind mismatch",
			&wiretypes.Message{Root: "a", Objects: []wiretypes.RawObject{
				{
					ID:   "a",
					Type: "person",
					Fields: map[string]wiretypes.RawValue{
						"Name":   wiretypes.VInt(7),
						"Age":    wiretypes.VInt(1),
						"Active": wiretypes.VBool(true),
					},
				},
			}},
			wiretypes.ErrTypeMismatch,
		},
		{
			"unknown field",
			&wiretypes.Message{Root: "a", Objects: []wiretypes.RawObject{
				{
					ID:   "a",
					Type: "person",
					Fields: map[string]wiretypes.RawValue{
						"Name":   wiretypes.VStr("a"),
						"Age":    wiretypes.VInt(1),
						"Active": wiretypes.VBool(true),
						"Ghost":  wiretypes.VStr("x"),
					},
				},
			}},
			wiretypes.ErrUnknownField,
		},
		{
			"missing required field",
			&wiretypes.Message{Root: "a", Objects: []wiretypes.RawObject{
				{
					ID:     "a",
					Type:   "person",
					Fields: map[string]wiretypes.RawValue{"Name": wiretypes.VStr("a"), "Age": wiretypes.VInt(1)},
				},
			}},
			wiretypes.ErrMissingField,
		},
		{
			"unregistered type",
			&wiretypes.Message{Root: "a", Objects: []wiretypes.RawObject{
				{ID: "a", Type: "robot", Fields: map[string]wiretypes.RawValue{}},
			}},
			wiretypes.ErrUnknownType,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := d.Decode(tc.msg, wiretypes.NopCallback)
			if !errors.Is(err, tc.want) {
				t.Fatalf("expected %v, got %v", tc.want, err)
			}
		})
	}
}

func TestDecode_SubtypeReference(t *testing.T) {
	r := mustRegistry(t, testSpecs())
	d := mustDecoder(t, r, fullMode(), wiretypes.Budget{})

	msg := &wiretypes.Message{
		Root: "p1",
		Objects: []wiretypes.RawObject{
			{
				ID:   "p1",
				Type: "person",
				Fields: map[string]wiretypes.RawValue{
					"Name":   wiretypes.VStr("ada"),
					"Age":    wiretypes.VInt(1),
					"Active": wiretypes.VBool(true),
					"Avatar": wiretypes.VRef("ph"),
				},
			},
			{
				ID:   "ph",
				Type: "photo",
				Fields: map[string]wiretypes.RawValue{
					"URL":   wiretypes.VStr("photo.png"),
					"Width": wiretypes.VInt(10),
				},
			},
		},
	}
	root, err := d.Decode(msg, wiretypes.NopCallback)
	if err != nil {
		t.Fatalf("subtype ref: %v", err)
	}
	if root.(*Person).Avatar == nil || root.(*Person).Avatar.URL != "photo.png" {
		t.Fatal("subtype instance not accepted where base type required")
	}
}

func TestDecode_CallbackReceivesTableOrderAndFullGraph(t *testing.T) {
	r := mustRegistry(t, testSpecs())
	d := mustDecoder(t, r, fullMode(), wiretypes.Budget{})

	msg := &wiretypes.Message{
		Root: "p1",
		Objects: []wiretypes.RawObject{
			{
				ID:   "p1",
				Type: "person",
				Fields: map[string]wiretypes.RawValue{
					"Name":    wiretypes.VStr("ada"),
					"Age":     wiretypes.VInt(1),
					"Active":  wiretypes.VBool(true),
					"Manager": wiretypes.VRef("p2"),
				},
			},
			{
				ID:   "p2",
				Type: "person",
				Fields: map[string]wiretypes.RawValue{
					"Name":   wiretypes.VStr("grace"),
					"Age":    wiretypes.VInt(2),
					"Active": wiretypes.VBool(true),
				},
			},
		},
	}

	var checked []wiretypes.RefID
	cb := wiretypes.FuncCallback{
		CheckFn: func(obj any, typ wiretypes.TypeID, id wiretypes.RefID) error {
			p := obj.(*Person)
			if id == "p1" && p.Manager == nil {
				t.Error("Check must see fully linked graph, including forward refs")
			}
			checked = append(checked, id)
			return nil
		},
	}
	if _, err := d.Decode(msg, cb); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	wantOrder := []wiretypes.RefID{"p1", "p2"}
	if !reflect.DeepEqual(checked, wantOrder) {
		t.Fatalf("check order = %v, want %v", checked, wantOrder)
	}
}

func TestDecode_RejectedMessageHasZeroSideEffects(t *testing.T) {
	r := mustRegistry(t, testSpecs())
	d := mustDecoder(t, r, fullMode(), wiretypes.Budget{})

	calls := 0
	specs := testSpecs()
	specs[0].New = func() any { calls++; return newPerson() }
	r2 := mustRegistry(t, specs)
	d2 := mustDecoder(t, r2, fullMode(), wiretypes.Budget{})
	// Registry construction probes each factory once; record that baseline.
	baseline := calls

	// First: validation rejection must never invoke a factory.
	bad := &wiretypes.Message{
		Root: "p1",
		Objects: []wiretypes.RawObject{
			{
				ID:   "p1",
				Type: "person",
				Fields: map[string]wiretypes.RawValue{
					"Name":    wiretypes.VStr("ada"),
					"Age":     wiretypes.VInt(1),
					"Active":  wiretypes.VBool(true),
					"Manager": wiretypes.VRef("ghost"),
				},
			},
		},
	}
	if _, err := d2.Decode(bad, wiretypes.NopCallback); !errors.Is(err, wiretypes.ErrUnknownRef) {
		t.Fatalf("expected dangling ref error, got %v", err)
	}
	if calls != baseline {
		t.Fatalf("factories ran %d time(s) before validation completed", calls-baseline)
	}

	// Second: callback rejection after construction must deliver nothing.
	msg := &wiretypes.Message{
		Root: "p1",
		Objects: []wiretypes.RawObject{
			{
				ID:   "p1",
				Type: "person",
				Fields: map[string]wiretypes.RawValue{
					"Name":   wiretypes.VStr("ada"),
					"Age":    wiretypes.VInt(1),
					"Active": wiretypes.VBool(true),
				},
			},
		},
	}
	before := calls
	rec := &recorder{reject: "p1"}
	_, err := d2.Decode(msg, rec)
	if !errors.Is(err, wiretypes.ErrRejected) {
		t.Fatalf("expected ErrRejected, got %v", err)
	}
	if calls != before+1 {
		t.Fatalf("factory should run exactly once for constructed graph, calls=%d", calls)
	}
	if len(rec.snapshot()) != 0 {
		t.Fatal("no Receive may run when Check rejects")
	}

	// Rejected message input must remain untouched.
	if msg.Objects[0].Fields["Name"].Str != "ada" {
		t.Fatal("decoder mutated the input message")
	}

	// The same message is still decodable once the business rule allows it.
	rec2 := &recorder{}
	if _, err := d.Decode(msg, rec2); err != nil {
		t.Fatalf("retry after rejection failed: %v", err)
	}
	if len(rec2.snapshot()) != 1 {
		t.Fatal("retry must deliver exactly one object")
	}
}
