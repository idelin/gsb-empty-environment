package netsec

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestParsePath(t *testing.T) {
	tests := []struct {
		text string
		want string
	}{
		{"m", "m"},
		{"M", "m"},
		{"m/0", "m/0"},
		{"0/1", "m/0/1"},
		{"m/44'/0h/1H/2\u2019/3", "m/44'/0'/1'/2'/3"},
		{"m/2147483648", "m/0'"},
		{"m/2147483650", "m/2'"},
	}
	for _, test := range tests {
		path, err := ParsePath(test.text)
		if err != nil {
			t.Fatalf("ParsePath(%q): %v", test.text, err)
		}
		if got := path.String(); got != test.want {
			t.Fatalf("ParsePath(%q) = %s, want %s", test.text, got, test.want)
		}
	}

	for _, text := range []string{"", "m/", "m//0", "m/x", "m/2147483648'"} {
		if _, err := ParsePath(text); err == nil {
			t.Fatalf("ParsePath(%q) unexpectedly succeeded", text)
		}
	}
}

func TestPathJSONAndRelations(t *testing.T) {
	path := MustParsePath("m/0'/1")
	data, err := json.Marshal(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `"m/0'/1"` {
		t.Fatalf("marshal = %s", data)
	}

	var decoded Path
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.Equal(path) {
		t.Fatalf("decoded = %s, want %s", decoded, path)
	}

	parent, ok := path.Parent()
	if !ok || parent.String() != "m/0'" {
		t.Fatalf("parent = %s, %v", parent, ok)
	}
	if !parent.IsAncestorOf(path) || path.IsAncestorOf(parent) {
		t.Fatal("ancestor relation is wrong")
	}
	if _, ok := (Path{}).Parent(); ok {
		t.Fatal("master path unexpectedly has a parent")
	}
}

func TestTreeConstruction(t *testing.T) {
	tree, err := NewTreeFromStrings([]string{"m/0/1", "m/0/1/0", "m/1"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"m", "m/0", "m/0/1", "m/0/1/0", "m/1"}
	if got := pathStrings(tree.Nodes()); !reflect.DeepEqual(got, want) {
		t.Fatalf("nodes = %v, want %v", got, want)
	}

	structure := &TreeNode{Children: []*TreeNode{
		{Path: MustParsePath("m/0"), Children: []*TreeNode{
			{Path: MustParsePath("m/0/0")},
		}},
		{Path: MustParsePath("m/1")},
	}}
	fromStructure, err := NewTreeFromStructure(structure)
	if err != nil {
		t.Fatal(err)
	}
	wantStructure := []string{"m", "m/0", "m/0/0", "m/1"}
	if got := pathStrings(fromStructure.Nodes()); !reflect.DeepEqual(got, wantStructure) {
		t.Fatalf("structure nodes = %v, want %v", got, wantStructure)
	}

	bad := &TreeNode{Children: []*TreeNode{{Path: MustParsePath("m/0/0")}}}
	if _, err := NewTreeFromStructure(bad); err == nil {
		t.Fatal("non-immediate child unexpectedly accepted")
	}
}

func pathStrings(paths []Path) []string {
	result := make([]string, len(paths))
	for i, path := range paths {
		result[i] = path.String()
	}
	return result
}
