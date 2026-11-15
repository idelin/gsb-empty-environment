package include

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// referenceResolve is the deliberately simple, non-caching reference
// implementation used to cross-check Resolve. It expands the include graph
// with plain recursion, detects cycles by scanning the current path, and
// applies the override rules directly, sharing no code with the iterative
// production implementation.
func referenceResolve(docs map[string]Document, root string) (*Result, error) {
	if len(docs) > MaxDocuments {
		return nil, &TooManyDocumentsError{Count: len(docs)}
	}
	for id, doc := range docs {
		if doc.ID != id {
			return nil, fmt.Errorf("include: document ID %q does not match map key %q", doc.ID, id)
		}
	}
	if _, ok := docs[root]; !ok {
		return nil, &UnknownDocumentError{ID: root}
	}

	r := &Result{
		Values:  make(map[string]string),
		Sources: make(map[string]Source),
	}

	var visit func(id string, inheritedTrusted bool, stack []string) error
	visit = func(id string, inheritedTrusted bool, stack []string) error {
		for _, ancestor := range stack {
			if ancestor == id {
				cycle := append([]string(nil), stack...)
				return &CycleError{Path: append(cycle, id)}
			}
		}
		doc := docs[id]
		stack = append(stack, id)
		trusted := inheritedTrusted && doc.Trust == Trusted
		for _, inc := range doc.Includes {
			if _, ok := docs[inc]; !ok {
				return &UnknownDocumentError{
					ID:   inc,
					From: append([]string(nil), stack...),
				}
			}
			if err := visit(inc, trusted, stack); err != nil {
				return err
			}
		}
		for key, value := range doc.Settings {
			if src, exists := r.Sources[key]; exists && src.Trusted && !trusted {
				r.Rejects = append(r.Rejects, Rejection{
					Key:       key,
					Value:     value,
					Path:      append([]string(nil), stack...),
					BlockedBy: append([]string(nil), src.Path...),
				})
				continue
			}
			r.Values[key] = value
			r.Sources[key] = Source{
				Path:    append([]string(nil), stack...),
				Trusted: trusted,
			}
		}
		return nil
	}

	if err := visit(root, docs[root].Trust == Trusted, nil); err != nil {
		return nil, err
	}
	return r, nil
}

// randomDocs builds a small random document graph. Key and value pools are
// intentionally tiny so collisions, same-value writes, and trust conflicts
// are frequent; includes may repeat, self-reference, form cycles, or (with
// low probability) point at a missing document.
func randomDocs(rng *rand.Rand) map[string]Document {
	n := 1 + rng.IntN(8)
	docs := make(map[string]Document, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("d%d", i)
		trust := Trusted
		if rng.IntN(2) == 0 {
			trust = Untrusted
		}
		settings := make(map[string]string)
		for k := 0; k < 4; k++ {
			if rng.IntN(2) == 0 {
				settings[fmt.Sprintf("k%d", k)] = fmt.Sprintf("v%d", rng.IntN(4))
			}
		}
		var includes []string
		for j := 0; j < rng.IntN(4); j++ {
			if rng.IntN(20) == 0 {
				includes = append(includes, "missing")
				continue
			}
			includes = append(includes, fmt.Sprintf("d%d", rng.IntN(n)))
		}
		docs[id] = Document{ID: id, Trust: trust, Settings: settings, Includes: includes}
	}
	return docs
}

func dumpDocs(docs map[string]Document) string {
	ids := make([]string, 0, len(docs))
	for id := range docs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var b strings.Builder
	for _, id := range ids {
		d := docs[id]
		fmt.Fprintf(&b, "  %s trust=%v settings=%v includes=%v\n", id, d.Trust, d.Settings, d.Includes)
	}
	return b.String()
}

func TestResolveMatchesReference(t *testing.T) {
	const iterations = 2000
	for i := 0; i < iterations; i++ {
		rng := rand.New(rand.NewPCG(1, uint64(i)))
		docs := randomDocs(rng)

		got, gotErr := Resolve(docs, "d0")
		want, wantErr := referenceResolve(docs, "d0")

		if !reflect.DeepEqual(gotErr, wantErr) {
			t.Fatalf("iter %d: error mismatch:\n got: %#v\nwant: %#v\ndocs:\n%s",
				i, gotErr, wantErr, dumpDocs(docs))
		}
		if gotErr != nil {
			if got != nil {
				t.Fatalf("iter %d: Resolve published partial result on error\ndocs:\n%s",
					i, dumpDocs(docs))
			}
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("iter %d: result mismatch:\n got: %+v\nwant: %+v\ndocs:\n%s",
				i, got, want, dumpDocs(docs))
		}
	}
}
