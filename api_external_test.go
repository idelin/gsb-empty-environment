package ftl_test

import (
	"bytes"
	"testing"

	ftl "prescreen.local/ps-73dfc2e0b1"
)

func TestExternalImportableAPI(t *testing.T) {
	cfg := ftl.Config{
		Blocks:        4,
		PagesPerBlock: 4,
		PageSize:      32,
	}
	dev, err := ftl.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var lba ftl.LBA = 3
	if err := dev.Write(lba, []byte("external")); err != nil {
		t.Fatal(err)
	}
	if _, ok := dev.Lookup(lba); !ok {
		t.Fatal("lba not mapped")
	}
	if err := dev.Trim(lba, 1); err != nil {
		t.Fatal(err)
	}
	if err := dev.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if err := dev.PowerCycle(); err != nil {
		t.Fatal(err)
	}
	value, err := dev.Read(lba)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(value, make([]byte, cfg.PageSize)) {
		t.Fatalf("trimmed read = %x", value)
	}
	reopened, err := ftl.Open(dev.Flash(), dev.Meta(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.Verify(); err != nil {
		t.Fatal(err)
	}
}
