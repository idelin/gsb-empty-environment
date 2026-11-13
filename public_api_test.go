package netsec_test

import (
	"path/filepath"
	"testing"

	netsec "example.org/netsec/q0165"
)

func TestPublicImportableAPI(t *testing.T) {
	h, err := netsec.NewHost(netsec.Options{
		LogPath:            filepath.Join(t.TempDir(), "public.log"),
		AuthKey:            []byte("0123456789abcdef0123456789abcdef"),
		DisableAutoReclaim: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()

	dir, err := h.CreateDirectory(netsec.PermAll)
	if err != nil {
		t.Fatal(err)
	}
	inode, err := h.CreateInode(netsec.CreateInodeOptions{Data: []byte("public")})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Link(inode, dir, "file"); err != nil {
		t.Fatal(err)
	}
	entry, err := h.Lookup(dir, "file")
	if err != nil {
		t.Fatal(err)
	}
	lease, err := h.Open(entry)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Unlink(entry); err != nil {
		t.Fatal(err)
	}
	if got, ok := h.GetInode(inode); !ok || got.Status != netsec.Orphan {
		t.Fatalf("inode after unlink: %+v, %v", got, ok)
	}
	if err := h.CloseLease(lease); err != nil {
		t.Fatal(err)
	}
	reclaimed, err := h.Sweep()
	if err != nil {
		t.Fatal(err)
	}
	if len(reclaimed) != 1 || reclaimed[0] != inode {
		t.Fatalf("reclaimed = %+v", reclaimed)
	}
}
