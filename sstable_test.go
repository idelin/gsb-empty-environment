package prefixseek

import (
	"bytes"
	"fmt"
	"testing"
	"unsafe"
)

// buildMultiTenantTable creates an SSTable holding several tenants, each
// with "order_" and "profile_" sub-attributes, plus interleaved noise
// tenants, totaling `perTenant*tenants*2` records.
func buildMultiTenantTable(t *testing.T, tenants, perTenant, blockSize int) (*Reader, []byte, []string) {
	t.Helper()
	b := NewBuilder(blockSize)
	var wantA []string
	for i := 0; i < tenants; i++ {
		tenant := fmt.Sprintf("tenant_%c:", 'A'+i)
		for j := 0; j < perTenant; j++ {
			key := fmt.Sprintf("%sorder_%08d", tenant, j)
			val := fmt.Sprintf(`{"tenant":%q,"order":%d,"amount":%d}`, tenant, j, j*7%1000)
			if err := b.Add([]byte(key), []byte(val)); err != nil {
				t.Fatal(err)
			}
			if i == 0 {
				wantA = append(wantA, key)
			}
		}
		for j := 0; j < perTenant; j++ {
			key := fmt.Sprintf("%sprofile_%08d", tenant, j)
			if err := b.Add([]byte(key), []byte("profile-blob")); err != nil {
				t.Fatal(err)
			}
		}
	}
	img := b.Finish()
	r, err := Open(img)
	if err != nil {
		t.Fatal(err)
	}
	return r, img, wantA
}

// TestPrefixSeekMultiTenant is the acceptance test: hundreds of thousands
// of multi-tenant records; PrefixSeek("tenant_A:order_") must skip the
// leading unrelated blocks via the index, stream all of tenant_A's orders
// zero-copy and in order, and terminate immediately at the next range.
func TestPrefixSeekMultiTenant(t *testing.T) {
	const tenants = 6
	const perTenant = 50000 // 6 * 2 * 50k = 600k records total
	r, img, want := buildMultiTenantTable(t, tenants, perTenant, DefaultBlockSize)
	t.Logf("table: %d records, %d data blocks, %.2f MiB",
		tenants*perTenant*2, r.NumBlocks(), float64(len(img))/(1<<20))

	it := r.PrefixSeek([]byte("tenant_A:order_"))
	var got []string
	for it.Next() {
		got = append(got, string(it.Key()))
	}
	if len(got) != len(want) {
		t.Fatalf("got %d keys, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("key %d: got %q, want %q", i, got[i], want[i])
		}
	}
	// The leading tenant_A:order_ blocks are the very first ones, so
	// BlocksSkipped may be 0 here; what matters is that trailing blocks
	// (profiles + tenants B..F) are never touched.
	if it.BlocksRead >= r.NumBlocks() {
		t.Fatalf("expected boundary truncation: read %d of %d blocks", it.BlocksRead, r.NumBlocks())
	}
	t.Logf("blocks: skipped=%d read=%d total=%d", it.BlocksSkipped, it.BlocksRead, r.NumBlocks())
}

// TestPrefixSeekSkipsLeadingBlocks seeks a middle tenant and verifies the
// index binary search skips all preceding blocks without reading them.
func TestPrefixSeekSkipsLeadingBlocks(t *testing.T) {
	r, _, _ := buildMultiTenantTable(t, 6, 20000, DefaultBlockSize)

	it := r.PrefixSeek([]byte("tenant_C:order_"))
	n := 0
	var first, last string
	for it.Next() {
		if n == 0 {
			first = string(it.Key())
		}
		last = string(it.Key())
		n++
	}
	if n != 20000 {
		t.Fatalf("got %d keys, want 20000", n)
	}
	if first != "tenant_C:order_00000000" || last != "tenant_C:order_00019999" {
		t.Fatalf("range [%q, %q]", first, last)
	}
	if it.BlocksSkipped == 0 {
		t.Fatal("expected leading blocks to be skipped by index binary search")
	}
	if it.BlocksSkipped+it.BlocksRead > r.NumBlocks() {
		t.Fatalf("impossible block accounting: skipped=%d read=%d total=%d",
			it.BlocksSkipped, it.BlocksRead, r.NumBlocks())
	}
	// Must terminate well before scanning the whole table.
	if it.BlocksRead > r.NumBlocks()/3 {
		t.Fatalf("read too many blocks: %d of %d", it.BlocksRead, r.NumBlocks())
	}
	t.Logf("blocks: skipped=%d read=%d total=%d", it.BlocksSkipped, it.BlocksRead, r.NumBlocks())
}

// TestBoundaryTruncation verifies the scan stops exactly at the first key
// outside the prefix, even when prefix is a prefix of other keys.
func TestBoundaryTruncation(t *testing.T) {
	b := NewBuilder(64) // tiny blocks to stress block boundaries
	keys := []string{
		"user:1000:a", "user:1001:", "user:1001:a", "user:1001:b",
		"user:1001:c", "user:1002:a", "user:1003:a",
	}
	for _, k := range keys {
		if err := b.Add([]byte(k), []byte("v-"+k)); err != nil {
			t.Fatal(err)
		}
	}
	r, err := Open(b.Finish())
	if err != nil {
		t.Fatal(err)
	}
	it := r.PrefixSeek([]byte("user:1001:"))
	var got []string
	for it.Next() {
		got = append(got, string(it.Key())+"="+string(it.Value()))
	}
	want := []string{
		"user:1001:=v-user:1001:", "user:1001:a=v-user:1001:a",
		"user:1001:b=v-user:1001:b", "user:1001:c=v-user:1001:c",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// TestPrefixSeekMiss covers prefixes that match nothing.
func TestPrefixSeekMiss(t *testing.T) {
	r, _, _ := buildMultiTenantTable(t, 3, 1000, DefaultBlockSize)
	for _, p := range []string{"tenant_Z:", "tenant_A:nonexist_", "zzz", "tenant_A:order_\xff"} {
		it := r.PrefixSeek([]byte(p))
		if it.Next() {
			t.Fatalf("prefix %q unexpectedly matched %q", p, it.Key())
		}
	}
}

// TestPrefixSeekLastRange seeks a prefix in the final block of the table.
func TestPrefixSeekLastRange(t *testing.T) {
	r, _, _ := buildMultiTenantTable(t, 3, 1000, DefaultBlockSize)
	it := r.PrefixSeek([]byte("tenant_C:profile_"))
	n := 0
	for it.Next() {
		n++
	}
	if n != 1000 {
		t.Fatalf("got %d, want 1000", n)
	}
}

// TestZeroCopy asserts returned keys/values alias the table image.
func TestZeroCopy(t *testing.T) {
	r, img, _ := buildMultiTenantTable(t, 2, 100, DefaultBlockSize)
	it := r.PrefixSeek([]byte("tenant_A:order_"))
	if !it.Next() {
		t.Fatal("no entries")
	}
	lo := uintptr(unsafe.Pointer(&img[0]))
	hi := lo + uintptr(len(img))
	for _, p := range [][]byte{it.Key(), it.Value()} {
		addr := uintptr(unsafe.Pointer(&p[0]))
		if addr < lo || addr >= hi {
			t.Fatalf("pointer %#x outside image [%#x, %#x): copy detected", addr, lo, hi)
		}
	}
}

// TestOutOfOrderRejected ensures the builder enforces sorted input.
func TestOutOfOrderRejected(t *testing.T) {
	b := NewBuilder(0)
	if err := b.Add([]byte("b"), nil); err != nil {
		t.Fatal(err)
	}
	if err := b.Add([]byte("a"), nil); err == nil {
		t.Fatal("expected out-of-order error")
	}
}

// TestCorrupted ensures Open rejects bad images.
func TestCorrupted(t *testing.T) {
	if _, err := Open([]byte("tiny")); err == nil {
		t.Fatal("expected error for short image")
	}
	b := NewBuilder(0)
	b.Add([]byte("k"), []byte("v"))
	img := b.Finish()
	img[len(img)-1] ^= 0xff // break the magic
	if _, err := Open(img); err == nil {
		t.Fatal("expected error for bad magic")
	}
}

// TestSingleBytePrefix scans with a 1-byte prefix across block boundaries.
func TestSingleBytePrefix(t *testing.T) {
	b := NewBuilder(128)
	for i := 0; i < 500; i++ {
		k := fmt.Sprintf("a%04d", i)
		b.Add([]byte(k), []byte{byte(i)})
	}
	for i := 0; i < 500; i++ {
		k := fmt.Sprintf("b%04d", i)
		b.Add([]byte(k), []byte{byte(i)})
	}
	r, err := Open(b.Finish())
	if err != nil {
		t.Fatal(err)
	}
	it := r.PrefixSeek([]byte("b"))
	n := 0
	for it.Next() {
		if !bytes.HasPrefix(it.Key(), []byte("b")) {
			t.Fatalf("bad key %q", it.Key())
		}
		n++
	}
	if n != 500 {
		t.Fatalf("got %d, want 500", n)
	}
}

// ExampleReader_PrefixSeek is the runnable sample: insert multi-tenant
// records, then PrefixSeek one tenant's orders.
func ExampleReader_PrefixSeek() {
	b := NewBuilder(256)
	tenants := []string{"tenant_A:", "tenant_B:", "tenant_C:"}
	for _, tn := range tenants {
		for i := 0; i < 5; i++ {
			b.Add([]byte(fmt.Sprintf("%sorder_%03d", tn, i)),
				[]byte(fmt.Sprintf("amount=%d", i*10)))
		}
		b.Add([]byte(tn+"profile"), []byte("..."))
	}
	r, _ := Open(b.Finish())

	it := r.PrefixSeek([]byte("tenant_A:order_"))
	for it.Next() {
		fmt.Printf("%s -> %s\n", it.Key(), it.Value())
	}
	fmt.Printf("blocks skipped=%d read=%d total=%d\n",
		it.BlocksSkipped, it.BlocksRead, r.NumBlocks())
	// Output:
	// tenant_A:order_000 -> amount=0
	// tenant_A:order_001 -> amount=10
	// tenant_A:order_002 -> amount=20
	// tenant_A:order_003 -> amount=30
	// tenant_A:order_004 -> amount=40
	// blocks skipped=0 read=1 total=2
}
