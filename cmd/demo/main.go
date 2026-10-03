// Demo: build an SSTable with multi-tenant records, then PrefixSeek one
// tenant's orders and observe block-level skipping + boundary truncation.
package main

import (
	"fmt"
	"time"

	prefixseek "prefixseek"
)

func main() {
	const tenants = 6
	const perTenant = 50000 // 600k records total

	start := time.Now()
	b := prefixseek.NewBuilder(prefixseek.DefaultBlockSize)
	for i := 0; i < tenants; i++ {
		tn := fmt.Sprintf("tenant_%c:", 'A'+i)
		for j := 0; j < perTenant; j++ {
			b.Add([]byte(fmt.Sprintf("%sorder_%08d", tn, j)),
				[]byte(fmt.Sprintf(`{"order":%d,"amount":%d}`, j, j*7%1000)))
		}
		for j := 0; j < perTenant; j++ {
			b.Add([]byte(fmt.Sprintf("%sprofile_%08d", tn, j)), []byte("blob"))
		}
	}
	img := b.Finish()
	r, err := prefixseek.Open(img)
	if err != nil {
		panic(err)
	}
	fmt.Printf("built: %d records, %d blocks, %.1f MiB in %v\n\n",
		b.NumRecords(), r.NumBlocks(), float64(len(img))/(1<<20), time.Since(start))

	for _, prefix := range []string{"tenant_A:order_", "tenant_C:order_"} {
		start = time.Now()
		it := r.PrefixSeek([]byte(prefix))
		n := 0
		var first, last string
		for it.Next() {
			if n < 3 { // show a few zero-copy rows
				fmt.Printf("  %s -> %s\n", it.Key(), it.Value())
			}
			if n == 0 {
				first = string(it.Key())
			}
			last = string(it.Key())
			n++
		}
		fmt.Printf("PrefixSeek(%q): %d rows in %v\n", prefix, n, time.Since(start))
		fmt.Printf("  range [%s .. %s]\n", first, last)
		fmt.Printf("  blocks: skipped=%d read=%d total=%d (%.1f%% of table touched)\n\n",
			it.BlocksSkipped, it.BlocksRead, r.NumBlocks(),
			100*float64(it.BlocksRead)/float64(r.NumBlocks()))
	}
}
