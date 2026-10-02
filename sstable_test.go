package prefixseek

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"unsafe"
)

const (
	testTenants     = 5 // tenant_A .. tenant_E
	ordersPerTenant = 60000
	testBlockSize   = 4096
)

// sharedTable is built once in TestMain and reused by all tests.
var sharedTable string

func TestMain(m *testing.M) {
	path, err := buildSharedTable()
	if err != nil {
		fmt.Println("setup:", err)
		os.Exit(1)
	}
	sharedTable = path
	code := m.Run()
	os.Remove(path)
	os.Exit(code)
}

func buildSharedTable() (string, error) {
	path := filepath.Join(os.TempDir(), "prefixseek_multitenant.sst")
	os.Remove(path)

	b, err := NewBuilder(path, testBlockSize)
	if err != nil {
		return "", err
	}
	for tIdx := 0; tIdx < testTenants; tIdx++ {
		tenant := fmt.Sprintf("tenant_%c:", 'A'+tIdx)
		for i := 0; i < ordersPerTenant; i++ {
			key := fmt.Sprintf("%sorder_%06d", tenant, i)
			val := fmt.Sprintf(`{"tenant":"%s","order":%d,"amount":%d.99,"status":"paid"}`,
				tenant[:len(tenant)-1], i, i%1000)
			if err := b.Add([]byte(key), []byte(val)); err != nil {
				return "", err
			}
		}
		// A few non-order attributes after the orders of each tenant.
		for _, attr := range []string{"profile", "settings"} {
			if err := b.Add([]byte(tenant+attr), []byte("{}")); err != nil {
				return "", err
			}
		}
	}
	if err := b.Finish(); err != nil {
		return "", err
	}
	return path, nil
}

func openShared(t *testing.T) *Reader {
	t.Helper()
	r, err := Open(sharedTable)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return r
}

func TestPrefixSeekMultiTenant(t *testing.T) {
	r := openShared(t)
	defer r.Close()
	t.Logf("table holds %d tenants x %d orders in %d data blocks",
		testTenants, ordersPerTenant, r.NumBlocks())

	// A single tenant occupies roughly NumBlocks/testTenants blocks; a
	// correct prefix seek must read only those, not the whole table.
	maxBlocks := r.NumBlocks()/testTenants + 10

	// The acceptance scenario from the spec.
	it := r.PrefixSeek([]byte("tenant_A:order_"))
	count := 0
	var prev []byte
	for ; it.Valid(); it.Next() {
		k := it.Key()
		if !bytes.HasPrefix(k, []byte("tenant_A:order_")) {
			t.Fatalf("key %q escaped the prefix", k)
		}
		if prev != nil && bytes.Compare(prev, k) >= 0 {
			t.Fatalf("keys out of order: %q >= %q", prev, k)
		}
		prev = append(prev[:0], k...)
		count++
	}
	if it.Err() != nil {
		t.Fatalf("iterator error: %v", it.Err())
	}
	if count != ordersPerTenant {
		t.Fatalf("got %d orders for tenant_A, want %d", count, ordersPerTenant)
	}
	if r.BlocksRead > maxBlocks {
		t.Fatalf("read %d blocks, want <= %d (should skip irrelevant blocks)",
			r.BlocksRead, maxBlocks)
	}
	if it.StopBlock >= r.NumBlocks()-1 {
		t.Fatalf("scan ran to the last block (%d/%d); should stop at the tenant boundary",
			it.StopBlock, r.NumBlocks())
	}
	t.Logf("tenant_A:order_ -> %d orders, %d/%d blocks read, stopped at block %d",
		count, r.BlocksRead, r.NumBlocks(), it.StopBlock)
}

func TestPrefixSeekSkipsPrecedingBlocks(t *testing.T) {
	r := openShared(t)
	defer r.Close()
	maxBlocks := r.NumBlocks()/testTenants + 10

	// tenant_C sits in the middle: the engine must binary-seek past all
	// blocks of tenant_A and tenant_B without reading them.
	it := r.PrefixSeek([]byte("tenant_C:order_"))
	count := 0
	for ; it.Valid(); it.Next() {
		count++
	}
	if it.Err() != nil {
		t.Fatalf("iterator error: %v", it.Err())
	}
	if count != ordersPerTenant {
		t.Fatalf("got %d orders for tenant_C, want %d", count, ordersPerTenant)
	}
	if r.BlocksRead > maxBlocks {
		t.Fatalf("read %d blocks, want <= %d (preceding tenants must be skipped)",
			r.BlocksRead, maxBlocks)
	}
	t.Logf("tenant_C:order_ -> %d orders, %d/%d blocks read", count, r.BlocksRead, r.NumBlocks())
}

func TestPrefixSeekLastTenantStopsAtEOF(t *testing.T) {
	r := openShared(t)
	defer r.Close()

	// The last tenant's orders run to the final data block; the scan must
	// terminate cleanly at EOF instead of erroring.
	it := r.PrefixSeek([]byte("tenant_E:order_"))
	count := 0
	for ; it.Valid(); it.Next() {
		count++
	}
	if it.Err() != nil {
		t.Fatalf("iterator error: %v", it.Err())
	}
	if count != ordersPerTenant {
		t.Fatalf("got %d orders for tenant_E, want %d", count, ordersPerTenant)
	}
}

func TestPrefixSeekNoMatch(t *testing.T) {
	r := openShared(t)
	defer r.Close()

	cases := [][]byte{
		[]byte("tenant_Z:order_"), // beyond the last key
		[]byte("tenant_A:zz"),     // gap between order_ and profile
		[]byte("aaa"),             // before the first key
	}
	for _, prefix := range cases {
		before := r.BlocksRead
		it := r.PrefixSeek(prefix)
		if it.Valid() {
			t.Fatalf("prefix %q unexpectedly matched key %q", prefix, it.Key())
		}
		if it.Err() != nil {
			t.Fatalf("prefix %q: %v", prefix, it.Err())
		}
		if read := r.BlocksRead - before; read > 2 {
			t.Fatalf("prefix %q read %d blocks, want <= 2", prefix, read)
		}
	}
}

func TestPrefixSeekZeroCopy(t *testing.T) {
	path := filepath.Join(os.TempDir(), "prefixseek_zerocopy.sst")
	defer os.Remove(path)

	b, err := NewBuilder(path, testBlockSize)
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}
	for i := 0; i < 1000; i++ {
		key := fmt.Sprintf("user:1001:attr_%04d", i)
		if err := b.Add([]byte(key), []byte("value-payload")); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}
	if err := b.Finish(); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	r, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()

	it := r.PrefixSeek([]byte("user:1001:"))
	if !it.Valid() {
		t.Fatal("iterator invalid, want a match")
	}
	// The returned key/value must be sub-slices of the raw block buffer:
	// their backing pointers must lie inside it.block.
	blkStart := uintptr(unsafe.Pointer(&it.block[0]))
	blkEnd := blkStart + uintptr(len(it.block))
	keyPtr := uintptr(unsafe.Pointer(&it.key[0]))
	valPtr := uintptr(unsafe.Pointer(&it.val[0]))
	if keyPtr < blkStart || keyPtr >= blkEnd {
		t.Fatal("Key() copied data out of the block buffer")
	}
	if valPtr < blkStart || valPtr >= blkEnd {
		t.Fatal("Value() copied data out of the block buffer")
	}
}

func TestBuilderRejectsUnsortedKeys(t *testing.T) {
	path := filepath.Join(os.TempDir(), "prefixseek_unsorted.sst")
	defer os.Remove(path)

	b, err := NewBuilder(path, testBlockSize)
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}
	if err := b.Add([]byte("b"), []byte("1")); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := b.Add([]byte("a"), []byte("2")); err != ErrUnsortedKeys {
		t.Fatalf("got %v, want ErrUnsortedKeys", err)
	}
	b.Finish()
}
