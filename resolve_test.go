package netsec

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

func doc(id string, trust TrustLevel, settings map[string]string, includes ...string) Document {
	return Document{ID: id, Trust: trust, Settings: settings, Includes: includes}
}

func docsOf(docs ...Document) map[string]Document {
	m := make(map[string]Document, len(docs))
	for _, d := range docs {
		m[d.ID] = d
	}
	return m
}

// Diamond: root includes A and B, both include C. C is expanded once per
// occurrence (no global caching), so the second occurrence overrides the
// first, and the including document's own settings win over its includes.
func TestDiamondInclude(t *testing.T) {
	docs := docsOf(
		doc("root", Trusted, map[string]string{"k": "root"}, "a", "b"),
		doc("a", Trusted, map[string]string{"k": "a", "a": "1"}, "c"),
		doc("b", Trusted, map[string]string{"k": "b"}, "c"),
		doc("c", Trusted, map[string]string{"k": "c", "c": "1"}),
	)
	res, err := Resolve(docs, "root")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := res.Values["k"].Value; got != "root" {
		t.Errorf("k = %q, want root (document overrides its includes)", got)
	}
	if got := res.Values["a"].Value; got != "1" {
		t.Errorf("a = %q, want 1", got)
	}
	// c is applied twice; the winning occurrence is the one via b (later).
	got := res.Values["c"]
	wantSource := []string{"root", "b", "c"}
	if got.Value != "1" || !reflect.DeepEqual(got.Source, wantSource) {
		t.Errorf("c = %+v, want value 1 with source %v", got, wantSource)
	}
	if len(res.Denied) != 0 {
		t.Errorf("Denied = %v, want none", res.Denied)
	}
}

// Trust laundering: an untrusted root pulls in a trusted document; the
// trusted document is demoted by its untrusted ancestor and cannot override
// keys written on a fully trusted path. An untrusted document also cannot
// override trusted keys even when it is processed later.
func TestTrustDemotionAndLaundering(t *testing.T) {
	docs := docsOf(
		doc("root", Trusted, nil, "base", "tenant"),
		doc("base", Trusted, map[string]string{"mode": "strict", "shared": "base"}),
		// tenant is untrusted; it includes the trusted "signed" doc, which
		// is demoted on this path and must not override "mode".
		doc("tenant", Untrusted, map[string]string{"mode": "lax"}, "signed"),
		doc("signed", Trusted, map[string]string{"mode": "signed", "extra": "ok"}),
	)
	res, err := Resolve(docs, "root")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := res.Values["mode"].Value; got != "strict" {
		t.Errorf("mode = %q, want strict (trusted write must survive)", got)
	}
	if got := res.Values["shared"].Value; got != "base" {
		t.Errorf("shared = %q, want base", got)
	}
	// "extra" is only written by the demoted path, so it is present but
	// marked untrusted.
	got := res.Values["extra"]
	if got.Value != "ok" || got.Trusted {
		t.Errorf("extra = %+v, want value ok with Trusted=false", got)
	}
	wantSource := []string{"root", "tenant", "signed"}
	if !reflect.DeepEqual(got.Source, wantSource) {
		t.Errorf("extra source = %v, want %v", got.Source, wantSource)
	}
	// Two denied overrides: the demoted signed doc and tenant both tried
	// to override the trusted "mode" (includes expand before own settings).
	if len(res.Denied) != 2 {
		t.Fatalf("len(Denied) = %d, want 2: %+v", len(res.Denied), res.Denied)
	}
	first := res.Denied[0]
	if first.Key != "mode" || first.AttemptedValue != "signed" ||
		!reflect.DeepEqual(first.AttemptedSource, wantSource) ||
		first.KeptValue != "strict" ||
		!reflect.DeepEqual(first.KeptSource, []string{"root", "base"}) {
		t.Errorf("Denied[0] = %+v, unexpected", first)
	}
	second := res.Denied[1]
	if second.Key != "mode" || second.AttemptedValue != "lax" ||
		!reflect.DeepEqual(second.AttemptedSource, []string{"root", "tenant"}) {
		t.Errorf("Denied[1] = %+v, unexpected", second)
	}
}

// Same-value override: a later write carrying the identical value still
// counts as an override (source path updates) when allowed, and is still
// denied when an untrusted path targets a trusted key.
func TestSameValueOverride(t *testing.T) {
	docs := docsOf(
		doc("root", Trusted, nil, "t1", "t2", "u1"),
		doc("t1", Trusted, map[string]string{"k": "v"}),
		doc("t2", Trusted, map[string]string{"k": "v"}),
		doc("u1", Untrusted, map[string]string{"k": "v"}),
	)
	res, err := Resolve(docs, "root")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	got := res.Values["k"]
	if got.Value != "v" || !reflect.DeepEqual(got.Source, []string{"root", "t2"}) {
		t.Errorf("k = %+v, want value v with source [root t2]", got)
	}
	if len(res.Denied) != 1 {
		t.Fatalf("len(Denied) = %d, want 1: %+v", len(res.Denied), res.Denied)
	}
	d := res.Denied[0]
	if d.Key != "k" || d.AttemptedValue != "v" || d.KeptValue != "v" ||
		!reflect.DeepEqual(d.AttemptedSource, []string{"root", "u1"}) {
		t.Errorf("Denied[0] = %+v, unexpected", d)
	}
}

// Untrusted-over-untrusted and trusted-over-untrusted overrides are allowed.
func TestUntrustedChainOverride(t *testing.T) {
	docs := docsOf(
		doc("root", Trusted, nil, "u1", "u2", "t1"),
		doc("u1", Untrusted, map[string]string{"k": "u1"}),
		doc("u2", Untrusted, map[string]string{"k": "u2"}),
		doc("t1", Trusted, map[string]string{"k": "t1"}),
	)
	res, err := Resolve(docs, "root")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	got := res.Values["k"]
	if got.Value != "t1" || !got.Trusted {
		t.Errorf("k = %+v, want trusted t1 (later trusted write wins)", got)
	}
	if len(res.Denied) != 0 {
		t.Errorf("Denied = %v, want none", res.Denied)
	}
}

// Cycles report the full cycle path and publish no partial configuration.
func TestCycle(t *testing.T) {
	docs := docsOf(
		doc("root", Trusted, map[string]string{"k": "root"}, "a"),
		doc("a", Trusted, map[string]string{"k": "a"}, "b"),
		doc("b", Trusted, map[string]string{"k": "b"}, "c"),
		doc("c", Trusted, map[string]string{"k": "c"}, "b"),
	)
	res, err := Resolve(docs, "root")
	if err == nil {
		t.Fatalf("Resolve succeeded with %+v, want cycle error", res)
	}
	if res != nil {
		t.Errorf("res = %+v, want nil (no partial configuration)", res)
	}
	var cyc *CycleError
	if !errors.As(err, &cyc) {
		t.Fatalf("err = %v (%T), want *CycleError", err, err)
	}
	want := []string{"root", "a", "b", "c", "b"}
	if !reflect.DeepEqual(cyc.Path, want) {
		t.Errorf("cycle path = %v, want %v", cyc.Path, want)
	}
}

func TestSelfIncludeCycle(t *testing.T) {
	docs := docsOf(doc("x", Trusted, nil, "x"))
	res, err := Resolve(docs, "x")
	if res != nil || err == nil {
		t.Fatalf("res=%v err=%v, want nil res and cycle error", res, err)
	}
	var cyc *CycleError
	if !errors.As(err, &cyc) || !reflect.DeepEqual(cyc.Path, []string{"x", "x"}) {
		t.Errorf("err = %v, want cycle path [x x]", err)
	}
}

func TestLimitsAndValidation(t *testing.T) {
	tooMany := make(map[string]Document, MaxDocuments+1)
	for i := 0; i <= MaxDocuments; i++ {
		id := fmt.Sprintf("d%03d", i)
		tooMany[id] = doc(id, Trusted, nil)
	}
	if _, err := Resolve(tooMany, "d000"); !errors.Is(err, ErrTooManyDocuments) {
		t.Errorf("err = %v, want ErrTooManyDocuments", err)
	}

	if _, err := Resolve(docsOf(doc("a", Trusted, nil, "ghost")), "a"); err == nil {
		t.Error("unknown include: want error")
	} else {
		var unk *UnknownDocumentError
		if !errors.As(err, &unk) || unk.From != "a" || unk.ID != "ghost" {
			t.Errorf("err = %v, want UnknownDocumentError{a, ghost}", err)
		}
	}

	if _, err := Resolve(docsOf(doc("a", Trusted, nil)), "missing"); err == nil {
		t.Error("unknown root: want error")
	}

	bad := map[string]Document{"a": doc("b", Trusted, nil)}
	if _, err := Resolve(bad, "a"); err == nil {
		t.Error("inconsistent ID: want error")
	}
}

// referenceResolve is an independent, deliberately naive reference
// implementation: it materializes the full ordered write list with a plain
// recursive walk (no caching of any kind) and then folds the writes into a
// resolution. The differential test below cross-checks Resolve against it.

type refWrite struct {
	path    []string
	key     string
	value   string
	trusted bool
}

func referenceResolve(docs map[string]Document, rootID string) (*Resolution, error) {
	if len(docs) > MaxDocuments {
		return nil, fmt.Errorf("%w: got %d, max %d", ErrTooManyDocuments, len(docs), MaxDocuments)
	}
	if err := validate(docs); err != nil {
		return nil, err
	}
	if _, ok := docs[rootID]; !ok {
		return nil, &UnknownDocumentError{ID: rootID}
	}
	var writes []refWrite
	steps := 0
	var walk func(id string, stack []string, trusted bool) error
	walk = func(id string, stack []string, trusted bool) error {
		for _, ancestor := range stack {
			if ancestor == id {
				path := make([]string, len(stack)+1)
				copy(path, stack)
				path[len(stack)] = id
				return &CycleError{Path: path}
			}
		}
		steps++
		if steps > MaxExpandSteps {
			return ErrTooManyExpansions
		}
		d := docs[id]
		eff := trusted && d.Trust == Trusted
		path := make([]string, len(stack)+1)
		copy(path, stack)
		path[len(stack)] = id
		for _, inc := range d.Includes {
			if err := walk(inc, path, eff); err != nil {
				return err
			}
		}
		keys := make([]string, 0, len(d.Settings))
		for k := range d.Settings {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			writes = append(writes, refWrite{path: path, key: k, value: d.Settings[k], trusted: eff})
		}
		return nil
	}
	if err := walk(rootID, nil, true); err != nil {
		return nil, err
	}
	res := &Resolution{Values: make(map[string]EffectiveValue)}
	for _, w := range writes {
		current, ok := res.Values[w.key]
		if ok && current.Trusted && !w.trusted {
			res.Denied = append(res.Denied, DeniedOverride{
				Key:             w.key,
				AttemptedValue:  w.value,
				AttemptedSource: w.path,
				KeptValue:       current.Value,
				KeptSource:      current.Source,
			})
			continue
		}
		res.Values[w.key] = EffectiveValue{Value: w.value, Source: w.path, Trusted: w.trusted}
	}
	return res, nil
}

func randomGraph(rng *rand.Rand) (map[string]Document, string) {
	n := 1 + rng.Intn(8)
	ids := make([]string, n)
	docs := make(map[string]Document, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("d%d", i)
	}
	for _, id := range ids {
		trust := Trusted
		if rng.Intn(2) == 0 {
			trust = Untrusted
		}
		var includes []string
		for j := 0; j < rng.Intn(4); j++ {
			includes = append(includes, ids[rng.Intn(n)])
		}
		settings := make(map[string]string)
		for j := 0; j < rng.Intn(4); j++ {
			settings[fmt.Sprintf("k%d", rng.Intn(4))] = fmt.Sprintf("v%d", rng.Intn(3))
		}
		docs[id] = Document{ID: id, Trust: trust, Settings: settings, Includes: includes}
	}
	return docs, ids[rng.Intn(n)]
}

// Differential check: Resolve must agree with the non-caching reference on
// random small graphs, including identical cycle paths and denied records.
func TestResolveMatchesReference(t *testing.T) {
	for seed := int64(0); seed < 5000; seed++ {
		rng := rand.New(rand.NewSource(seed))
		docs, root := randomGraph(rng)
		gotRes, gotErr := Resolve(docs, root)
		wantRes, wantErr := referenceResolve(docs, root)
		if (gotErr == nil) != (wantErr == nil) {
			t.Fatalf("seed %d: Resolve err %v, reference err %v", seed, gotErr, wantErr)
		}
		if gotErr != nil {
			var gotCyc, wantCyc *CycleError
			if errors.As(gotErr, &gotCyc) != errors.As(wantErr, &wantCyc) {
				t.Fatalf("seed %d: error kinds differ: %v vs %v", seed, gotErr, wantErr)
			}
			if gotCyc != nil && !reflect.DeepEqual(gotCyc.Path, wantCyc.Path) {
				t.Fatalf("seed %d: cycle paths differ: %v vs %v", seed, gotCyc.Path, wantCyc.Path)
			}
			continue
		}
		if !reflect.DeepEqual(gotRes, wantRes) {
			t.Fatalf("seed %d: resolutions differ\n got: %+v\nwant: %+v", seed, gotRes, wantRes)
		}
	}
}
