package netsec

import "fmt"

// ExampleInstrument 展示编译宿主的典型用法：克隆原图、插桩、解释对照。
func ExampleInstrument() {
	src := buildStraight()
	work := Clone(src)

	checker := RangeChecker(0, 8) // 地址 16 的写入将被拒绝
	n, err := Instrument(work, Policy{Checker: checker})
	if err != nil {
		fmt.Println("instrument error:", err)
		return
	}
	fmt.Println("inserted checks:", n)

	mem := NewMemory()
	mem.Set(0, 7)
	res, err := (&Interpreter{Mem: mem, Checker: checker}).Run(work, 1, nil)
	if err != nil {
		fmt.Println("run error:", err)
		return
	}
	fmt.Println("rejected:", res.Err != nil)
	fmt.Println("byte at 16 after reject:", res.MemSnapshot[16])
	// Output:
	// inserted checks: 2
	// rejected: true
	// byte at 16 after reject: 0
}
