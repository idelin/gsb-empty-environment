package reorder

import (
	"strings"
	"testing"
)

// seq builds a base sequence where node content defaults to "content-<id>".
func seq(ids ...string) []Node {
	ns := make([]Node, len(ids))
	for i, id := range ids {
		ns[i] = Node{ID: id, Content: "content-" + id}
	}
	return ns
}

// render flattens a node sequence into a comparable "id:content" list.
func render(ns []Node) string {
	parts := make([]string, len(ns))
	for i, n := range ns {
		parts[i] = n.ID + ":" + n.Content
	}
	return strings.Join(parts, " ")
}

func TestApply(t *testing.T) {
	cases := []struct {
		name  string
		base  []Node
		batch Batch
		want  string
	}{
		{
			name:  "empty batch keeps sequence",
			base:  seq("a", "b", "c"),
			batch: Batch{},
			want:  "a:content-a b:content-b c:content-c",
		},
		{
			name:  "delete removes nodes",
			base:  seq("a", "b", "c", "d"),
			batch: Batch{Deletes: []string{"b", "d"}},
			want:  "a:content-a c:content-c",
		},
		{
			name:  "modify merges new content",
			base:  seq("a", "b"),
			batch: Batch{Modifies: []Modify{{ID: "b", Content: "new"}}},
			want:  "a:content-a b:new",
		},
		{
			name:  "move after predecessor",
			base:  seq("a", "b", "c", "d"),
			batch: Batch{Moves: []Move{{ID: "d", After: "a"}}},
			want:  "a:content-a d:content-d b:content-b c:content-c",
		},
		{
			name:  "move to front with empty predecessor",
			base:  seq("a", "b", "c"),
			batch: Batch{Moves: []Move{{ID: "c", After: ""}}},
			want:  "c:content-c a:content-a b:content-b",
		},
		{
			name:  "move after deleted predecessor uses nearest survivor",
			base:  seq("a", "b", "c", "d"),
			batch: Batch{Deletes: []string{"c"}, Moves: []Move{{ID: "a", After: "c"}}},
			want:  "b:content-b a:content-a d:content-d",
		},
		{
			name:  "deleted predecessor chain walks back along old order",
			base:  seq("a", "b", "c", "d", "e"),
			batch: Batch{Deletes: []string{"b", "c", "d"}, Moves: []Move{{ID: "e", After: "d"}}},
			want:  "a:content-a e:content-e",
		},
		{
			name:  "no surviving predecessor moves to front",
			base:  seq("a", "b", "c"),
			batch: Batch{Deletes: []string{"a", "b"}, Moves: []Move{{ID: "c", After: "b"}}},
			want:  "c:content-c",
		},
		{
			name:  "chained moves keep constraint order",
			base:  seq("a", "b", "c", "d"),
			batch: Batch{Moves: []Move{{ID: "a", After: "b"}, {ID: "b", After: "d"}}},
			want:  "c:content-c d:content-d b:content-b a:content-a",
		},
		{
			name:  "multiple moves to one anchor keep old relative order",
			base:  seq("a", "b", "c", "d"),
			batch: Batch{Moves: []Move{{ID: "d", After: "a"}, {ID: "b", After: "a"}}},
			want:  "a:content-a b:content-b d:content-d c:content-c",
		},
		{
			name:  "anchor resolved through deletion onto a moved node",
			base:  seq("a", "b", "c", "d"),
			batch: Batch{Deletes: []string{"c"}, Moves: []Move{{ID: "b", After: "a"}, {ID: "d", After: "c"}}},
			want:  "a:content-a b:content-b d:content-d",
		},
		{
			name: "delete move and modify merge in one batch",
			base: seq("p1", "p2", "p3", "p4", "p5"),
			batch: Batch{
				Deletes:  []string{"p2"},
				Moves:    []Move{{ID: "p5", After: "p2"}, {ID: "p4", After: "p5"}},
				Modifies: []Modify{{ID: "p5", Content: "moved+edited"}, {ID: "p1", Content: "edited"}},
			},
			want: "p1:edited p5:moved+edited p4:content-p4 p3:content-p3",
		},
		{
			name:  "deleted predecessor resolving past the moved node itself",
			base:  seq("a", "b"),
			batch: Batch{Deletes: []string{"b"}, Moves: []Move{{ID: "a", After: "b"}}},
			want:  "a:content-a",
		},
		{
			name:  "modify on moved node applies at new position",
			base:  seq("a", "b", "c"),
			batch: Batch{Moves: []Move{{ID: "a", After: "c"}}, Modifies: []Modify{{ID: "a", Content: "x"}}},
			want:  "b:content-b c:content-c a:x",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Apply(tc.base, tc.batch)
			if err != nil {
				t.Fatalf("Apply returned error: %v", err)
			}
			if r := render(got); r != tc.want {
				t.Fatalf("got  %s\nwant %s", r, tc.want)
			}
		})
	}
}

func TestApplyErrors(t *testing.T) {
	cases := []struct {
		name  string
		base  []Node
		batch Batch
		want  error
	}{
		{
			name:  "duplicate id in base",
			base:  seq("a", "a"),
			batch: Batch{},
			want:  ErrDuplicateID,
		},
		{
			name:  "delete unknown node",
			base:  seq("a"),
			batch: Batch{Deletes: []string{"z"}},
			want:  ErrUnknownNode,
		},
		{
			name:  "move unknown node",
			base:  seq("a"),
			batch: Batch{Moves: []Move{{ID: "z", After: "a"}}},
			want:  ErrUnknownNode,
		},
		{
			name:  "modify unknown node",
			base:  seq("a"),
			batch: Batch{Modifies: []Modify{{ID: "z", Content: "x"}}},
			want:  ErrUnknownNode,
		},
		{
			name:  "move after unknown predecessor",
			base:  seq("a"),
			batch: Batch{Moves: []Move{{ID: "a", After: "z"}}},
			want:  ErrUnknownAnchor,
		},
		{
			name:  "delete and move conflict",
			base:  seq("a", "b"),
			batch: Batch{Deletes: []string{"a"}, Moves: []Move{{ID: "a", After: "b"}}},
			want:  ErrConflict,
		},
		{
			name:  "delete and modify conflict",
			base:  seq("a"),
			batch: Batch{Deletes: []string{"a"}, Modifies: []Modify{{ID: "a", Content: "x"}}},
			want:  ErrConflict,
		},
		{
			name:  "duplicate move on same node",
			base:  seq("a", "b"),
			batch: Batch{Moves: []Move{{ID: "a", After: "b"}, {ID: "a", After: ""}}},
			want:  ErrConflict,
		},
		{
			name:  "duplicate modify on same node",
			base:  seq("a"),
			batch: Batch{Modifies: []Modify{{ID: "a", Content: "x"}, {ID: "a", Content: "y"}}},
			want:  ErrConflict,
		},
		{
			name:  "duplicate delete on same node",
			base:  seq("a"),
			batch: Batch{Deletes: []string{"a", "a"}},
			want:  ErrConflict,
		},
		{
			name:  "self move",
			base:  seq("a"),
			batch: Batch{Moves: []Move{{ID: "a", After: "a"}}},
			want:  ErrSelfMove,
		},
		{
			name:  "two node move cycle",
			base:  seq("a", "b"),
			batch: Batch{Moves: []Move{{ID: "a", After: "b"}, {ID: "b", After: "a"}}},
			want:  ErrCyclicMove,
		},
		{
			name: "three node move cycle",
			base: seq("a", "b", "c", "d"),
			batch: Batch{Moves: []Move{
				{ID: "a", After: "b"},
				{ID: "b", After: "c"},
				{ID: "c", After: "a"},
			}},
			want: ErrCyclicMove,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Apply(tc.base, tc.batch)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err != tc.want {
				t.Fatalf("got error %v, want %v", err, tc.want)
			}
		})
	}
}

// TestApplyDoesNotMutateBase ensures the input sequence is left untouched.
func TestApplyDoesNotMutateBase(t *testing.T) {
	base := seq("a", "b", "c")
	before := render(base)
	_, err := Apply(base, Batch{
		Deletes:  []string{"b"},
		Moves:    []Move{{ID: "c", After: ""}},
		Modifies: []Modify{{ID: "a", Content: "x"}},
	})
	if err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	if render(base) != before {
		t.Fatalf("base mutated: %s", render(base))
	}
}
