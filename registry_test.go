package wiretypes_test

import (
	"errors"
	"testing"
	"time"

	"example.org/wiretypes"
)

func TestRegistry_Swap(t *testing.T) {
	r := mustRegistry(t, testSpecs())
	d := mustDecoder(t, r, fullMode(), wiretypes.Budget{})

	msg := &wiretypes.Message{
		Root: "b1",
		Objects: []wiretypes.RawObject{
			{
				ID:     "b1",
				Type:   "banner",
				Fields: map[string]wiretypes.RawValue{"Headline": wiretypes.VStr("hi")},
			},
		},
	}
	if _, err := d.Decode(msg, wiretypes.NopCallback); err != nil {
		t.Fatalf("baseline decode: %v", err)
	}

	// Swap to a catalog that drops banner.
	shrunk := []wiretypes.TypeSpec{
		{
			ID:  "person",
			New: newPerson,
			Fields: []wiretypes.FieldSpec{
				{Name: "Name", Kind: wiretypes.KindString},
				{Name: "Age", Kind: wiretypes.KindInt},
				{Name: "Active", Kind: wiretypes.KindBool},
			},
		},
	}
	if err := r.Swap(shrunk); err != nil {
		t.Fatalf("Swap: %v", err)
	}
	if _, err := d.Decode(msg, wiretypes.NopCallback); !errors.Is(err, wiretypes.ErrUnknownType) {
		t.Fatalf("post-swap decode should reject banner, got %v", err)
	}
	if got := r.Snapshot().IDs(); len(got) != 1 || got[0] != "person" {
		t.Fatalf("snapshot ids after swap = %v", got)
	}

	// A failed swap must leave the previous catalog in place.
	bad := []wiretypes.TypeSpec{
		{ID: "x", New: newPerson, Fields: []wiretypes.FieldSpec{
			{Name: "Nope", Kind: wiretypes.KindString},
		}},
	}
	if err := r.Swap(bad); !errors.Is(err, wiretypes.ErrRegistry) {
		t.Fatalf("expected ErrRegistry from bad swap, got %v", err)
	}
	if _, err := d.Decode(&wiretypes.Message{
		Root: "p",
		Objects: []wiretypes.RawObject{{ID: "p", Type: "person", Fields: map[string]wiretypes.RawValue{
			"Name": wiretypes.VStr("n"), "Age": wiretypes.VInt(1), "Active": wiretypes.VBool(true),
		}}},
	}, wiretypes.NopCallback); err != nil {
		t.Fatalf("old snapshot must survive a failed swap: %v", err)
	}
}

func TestRegistry_HotSwapDoesNotAffectInFlightMessage(t *testing.T) {
	r := mustRegistry(t, testSpecs())
	d := mustDecoder(t, r, fullMode(), wiretypes.Budget{})

	msg := &wiretypes.Message{
		Root: "b1",
		Objects: []wiretypes.RawObject{
			{
				ID:     "b1",
				Type:   "banner",
				Fields: map[string]wiretypes.RawValue{"Headline": wiretypes.VStr("hi")},
			},
		},
	}

	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	cb := &recorder{entered: entered, release: release}

	errCh := make(chan error, 1)
	go func() {
		_, err := d.Decode(msg, cb)
		errCh <- err
	}()

	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("decode did not reach callback")
	}

	// While the message is in flight, remove banner from the registry.
	shrunk := []wiretypes.TypeSpec{
		{
			ID:  "person",
			New: newPerson,
			Fields: []wiretypes.FieldSpec{
				{Name: "Name", Kind: wiretypes.KindString},
				{Name: "Age", Kind: wiretypes.KindInt},
				{Name: "Active", Kind: wiretypes.KindBool},
			},
		},
	}
	if err := r.Swap(shrunk); err != nil {
		t.Fatalf("Swap during in-flight decode: %v", err)
	}
	close(release)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("in-flight decode must keep using its snapshot: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight decode never returned")
	}

	got := cb.snapshot()
	if len(got) != 1 || got[0].t != "banner" {
		t.Fatalf("in-flight delivery lost after swap: %+v", got)
	}

	// The next message is bound to the new snapshot and must reject banner.
	if _, err := d.Decode(msg, wiretypes.NopCallback); !errors.Is(err, wiretypes.ErrUnknownType) {
		t.Fatalf("new message must observe swapped registry, got %v", err)
	}
}

func TestRegistry_InvalidSpecs(t *testing.T) {
	cases := []struct {
		name  string
		specs []wiretypes.TypeSpec
	}{
		{"empty", nil},
		{"empty id", []wiretypes.TypeSpec{{New: newBanner}}},
		{"duplicate id", []wiretypes.TypeSpec{
			{ID: "banner", New: newBanner, Fields: []wiretypes.FieldSpec{{Name: "Headline", Kind: wiretypes.KindString}}},
			{ID: "banner", New: newBanner, Fields: []wiretypes.FieldSpec{{Name: "Headline", Kind: wiretypes.KindString}}},
		}},
		{"nil factory", []wiretypes.TypeSpec{{ID: "banner"}}},
		{"non-pointer factory", []wiretypes.TypeSpec{{ID: "banner", New: func() any { return Banner{} }}}},
		{"missing struct field", []wiretypes.TypeSpec{{
			ID:  "banner",
			New: newBanner,
			Fields: []wiretypes.FieldSpec{
				{Name: "Headline", Kind: wiretypes.KindString},
				{Name: "Missing", Kind: wiretypes.KindString},
			},
		}}},
		{"go type mismatch", []wiretypes.TypeSpec{{
			ID:     "banner",
			New:    newBanner,
			Fields: []wiretypes.FieldSpec{{Name: "Headline", Kind: wiretypes.KindInt}},
		}}},
		{"ref missing type", []wiretypes.TypeSpec{{
			ID:  "person",
			New: newPerson,
			Fields: []wiretypes.FieldSpec{
				{Name: "Name", Kind: wiretypes.KindString},
				{Name: "Age", Kind: wiretypes.KindInt},
				{Name: "Active", Kind: wiretypes.KindBool},
				{Name: "Manager", Kind: wiretypes.KindRef, Nullable: true},
			},
		}}},
		{"ref unknown type", []wiretypes.TypeSpec{{
			ID:  "person",
			New: newPerson,
			Fields: []wiretypes.FieldSpec{
				{Name: "Name", Kind: wiretypes.KindString},
				{Name: "Age", Kind: wiretypes.KindInt},
				{Name: "Active", Kind: wiretypes.KindBool},
				{Name: "Manager", Kind: wiretypes.KindRef, RefType: "ghost", Nullable: true},
			},
		}}},
		{"list missing elem", []wiretypes.TypeSpec{{
			ID:  "person",
			New: newPerson,
			Fields: []wiretypes.FieldSpec{
				{Name: "Name", Kind: wiretypes.KindString},
				{Name: "Age", Kind: wiretypes.KindInt},
				{Name: "Active", Kind: wiretypes.KindBool},
				{Name: "Pals", Kind: wiretypes.KindList},
			},
		}}},
		{"base cycle", []wiretypes.TypeSpec{
			{
				ID: "a", Base: "b", New: newImage,
				Fields: []wiretypes.FieldSpec{{Name: "URL", Kind: wiretypes.KindString}, {Name: "Width", Kind: wiretypes.KindInt}},
			},
			{
				ID: "b", Base: "a", New: newImage,
				Fields: []wiretypes.FieldSpec{{Name: "URL", Kind: wiretypes.KindString}, {Name: "Width", Kind: wiretypes.KindInt}},
			},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := wiretypes.NewRegistry(tc.specs); !errors.Is(err, wiretypes.ErrRegistry) {
				t.Fatalf("expected ErrRegistry, got %v", err)
			}
		})
	}
}

func TestNewDecoder_RequiresRegistry(t *testing.T) {
	if _, err := wiretypes.NewDecoder(nil, fullMode(), wiretypes.Budget{}); !errors.Is(err, wiretypes.ErrRegistry) {
		t.Fatalf("expected ErrRegistry, got %v", err)
	}
}
