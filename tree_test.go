package rpki

import (
	"context"
	"errors"
	"testing"
)

func TestHappyPathTopDownTree(t *testing.T) {
	w := newHappyWorld(t)
	v := w.validator()
	report, err := v.ValidateTAL(context.Background(), w.h.tal)
	if err != nil {
		t.Fatalf("ValidateTAL returned error: %v", err)
	}
	if !report.Valid {
		t.Fatalf("root report invalid: %v", report.Err)
	}
	if len(report.Children) != 1 || report.Children[0].Name != "child" {
		t.Fatalf("expected one child CA, got %#v", report.Children)
	}
	childReport := report.Children[0]
	if !childReport.Valid {
		t.Fatalf("child invalid: %v", childReport.Err)
	}
	if len(childReport.Children) != 1 || childReport.Children[0].Name != "grand" {
		t.Fatalf("expected one grandchild, got %#v", childReport.Children)
	}
	if !childReport.Children[0].Valid {
		t.Fatalf("grandchild invalid: %v", childReport.Children[0].Err)
	}

	wm, ok, err := w.store.LoadWatermark(context.Background(), w.h.root.Cert.StateKey())
	if err != nil || !ok {
		t.Fatalf("root watermark not persisted: ok=%v err=%v", ok, err)
	}
	if wm.ManifestNumber.Int64() != 1 {
		t.Fatalf("root watermark = %s", wm.ManifestNumber)
	}
	if _, ok, err := w.store.LoadWatermark(context.Background(), w.child.Cert.StateKey()); err != nil || !ok {
		t.Fatalf("child watermark missing: ok=%v err=%v", ok, err)
	}
	if _, ok, err := w.store.LoadWatermark(context.Background(), w.grand.Cert.StateKey()); err != nil || !ok {
		t.Fatalf("grand watermark missing: ok=%v err=%v", ok, err)
	}
}

func TestResourceContainmentUnit(t *testing.T) {
	parent := ResourceSet{
		IPv4: mustIPRanges(t, "10.0.0.0/8", "192.168.0.0/16"),
		IPv6: mustIPRanges(t, "2001:db8::/32"),
		ASNs: []ASRange{{Start: 64500, End: 64600}},
	}.Normalize()

	goodV4, err := ParseIPRange("10.255.255.254", "10.255.255.255")
	if err != nil {
		t.Fatal(err)
	}
	if !parent.ContainsIPRange(goodV4) {
		t.Fatal("expected boundary upper address to be contained")
	}
	badV4, err := ParseIPRange("10.255.255.255", "11.0.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if parent.ContainsIPRange(badV4) {
		t.Fatal("range crossing parent boundary must not be contained")
	}
	if parent.ContainsASRange(ASRange{Start: 64599, End: 64601}) {
		t.Fatal("AS range extending by one must be rejected")
	}
	child := ResourceSet{
		IPv6: mustIPRanges(t, "2001:db8:ffff::/48"),
		ASNs: []ASRange{{Start: 64550, End: 64560}},
	}
	if !parent.Contains(child) {
		t.Fatal("expected nested resources to be contained")
	}
	child.ASNs[0].End = 64700
	if parent.Contains(child) {
		t.Fatal("expected enlarged AS set to fail")
	}
}

func mustIPRanges(t *testing.T, cidrs ...string) []IPRange {
	t.Helper()
	var out []IPRange
	for _, cidr := range cidrs {
		p, err := ParseIPPrefix(cidr)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, p.Range())
	}
	return out
}

func TestErrorsAreSentinels(t *testing.T) {
	if !errors.Is(ErrRollback, ErrManifestSequence) {
		t.Fatal("ErrRollback should wrap ErrManifestSequence")
	}
}
