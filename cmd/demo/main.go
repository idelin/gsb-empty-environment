// Demo: build a multi-tenant SSTable with 300k+ records, then run
// PrefixSeek("tenant_A:order_") and show that the engine binary-seeks
// past irrelevant blocks, streams matching orders zero-copy, and stops
// immediately at the next tenant boundary.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	prefixseek "prefixseek"
)

func main() {
	path := filepath.Join(os.TempDir(), "demo_multitenant.sst")
	defer os.Remove(path)

	const tenants = 5
	const ordersPerTenant = 60000

	start := time.Now()
	b, err := prefixseek.NewBuilder(path, prefixseek.DefaultBlockSize)
	must(err)
	total := 0
	for t := 0; t < tenants; t++ {
		tenant := fmt.Sprintf("tenant_%c:", 'A'+t)
		for i := 0; i < ordersPerTenant; i++ {
			key := fmt.Sprintf("%sorder_%06d", tenant, i)
			val := fmt.Sprintf(`{"order":%d,"amount":%d.99,"status":"paid"}`, i, i%1000)
			must(b.Add([]byte(key), []byte(val)))
			total++
		}
		must(b.Add([]byte(tenant+"profile"), []byte(`{"tier":"gold"}`)))
		total++
	}
	must(b.Finish())
	fmt.Printf("[build] %d records in %s\n", total, time.Since(start).Round(time.Millisecond))

	r, err := prefixseek.Open(path)
	must(err)
	defer r.Close()
	fmt.Printf("[open ] %d data blocks, index block resident in memory\n", r.NumBlocks())

	start = time.Now()
	it := r.PrefixSeek([]byte("tenant_A:order_"))
	count := 0
	for ; it.Valid(); it.Next() {
		if count < 3 || count == ordersPerTenant-1 {
			fmt.Printf("[scan  ] %s => %s\n", it.Key(), it.Value())
		}
		count++
	}
	must(it.Err())
	fmt.Printf("[done  ] PrefixSeek('tenant_A:order_') -> %d orders in %s\n",
		count, time.Since(start).Round(time.Microsecond))
	fmt.Printf("[stats ] blocks read: %d / %d, stopped at block #%d (boundary truncation)\n",
		r.BlocksRead, r.NumBlocks(), it.StopBlock)
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
