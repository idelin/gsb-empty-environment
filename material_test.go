package netsec

import (
	"reflect"
	"strings"
	"testing"
)

func TestReadMaterials(t *testing.T) {
	document := `{"materials":[
		{"kind":"extended_public_key","path":"m/0"},
		{"kind":"leaf-private-key","path":"m/0/0"},
		{"kind":"xprv","path":"m/1"}
	]}`
	materials, err := ReadMaterials(strings.NewReader(document))
	if err != nil {
		t.Fatal(err)
	}
	want := []Material{
		{Kind: ExtendedPublicKey, Path: MustParsePath("m/0")},
		{Kind: PrivateKey, Path: MustParsePath("m/0/0")},
		{Kind: ExtendedPrivateKey, Path: MustParsePath("m/1")},
	}
	if !reflect.DeepEqual(materials, want) {
		t.Fatalf("materials = %#v, want %#v", materials, want)
	}

	array := `[{"kind":"xpub","path":"m"}]`
	materials, err = ReadMaterials(strings.NewReader(array))
	if err != nil {
		t.Fatal(err)
	}
	if len(materials) != 1 || materials[0].Kind != ExtendedPublicKey || materials[0].Path.String() != "m" {
		t.Fatalf("array materials = %#v", materials)
	}
}

func TestParseMaterial(t *testing.T) {
	tests := []struct {
		text string
		kind MaterialKind
		path string
	}{
		{"xpub:m/0", ExtendedPublicKey, "m/0"},
		{"xprv m/1'", ExtendedPrivateKey, "m/1'"},
		{"private= m/2", PrivateKey, "m/2"},
	}
	for _, test := range tests {
		material, err := ParseMaterial(test.text)
		if err != nil {
			t.Fatalf("ParseMaterial(%q): %v", test.text, err)
		}
		if material.Kind != test.kind || material.Path.String() != test.path {
			t.Fatalf("ParseMaterial(%q) = %#v", test.text, material)
		}
	}
	if _, err := ParseMaterial("xpub"); err == nil {
		t.Fatal("material without a path unexpectedly accepted")
	}
	if _, err := ParseMaterial("watch:m/0"); err == nil {
		t.Fatal("unknown material kind unexpectedly accepted")
	}
}
