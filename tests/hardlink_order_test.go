package oci_test

import (
	"testing"

	oci "formal.local/w27-7184be2212"
)

func TestSameLayerForwardHardlink(t *testing.T) {
	fs, err := oci.Merge(oci.Layer{Entries: []oci.Entry{
		{Path: "dir", Type: oci.TypeDirectory},
		{Path: "dir/link", Type: oci.TypeHardlink, Linkname: "../target"},
		{Path: "target", Type: oci.TypeRegular, Mode: 0o4755, UID: 0, Content: []byte("payload")},
	}})
	if err != nil {
		t.Fatal(err)
	}

	link, err := fs.Stat("dir/link")
	if err != nil {
		t.Fatal(err)
	}
	if link.ResolvedHardlinkTarget != "target" {
		t.Fatalf("target = %q, want target", link.ResolvedHardlinkTarget)
	}
	if !link.SetUID() || string(link.Content) != "payload" {
		t.Fatalf("hardlink did not inherit inode: mode=%#o content=%q", link.Mode, link.Content)
	}
}
