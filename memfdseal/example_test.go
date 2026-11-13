package memfdseal_test

import (
	"errors"
	"fmt"
	"syscall"

	"example.org/netsec/q0690/memfdseal"
)

// 演示宿主在传递 memfd 描述符之前应当走完的封印流程，以及 EBUSY 如何暴露
// 残留的可写共享映射。
func ExampleMachine_deliveryFlow() {
	m := memfdseal.New(memfdseal.Config{AllowSealing: true})

	if err := m.Truncate(4096); err != nil {
		panic(err)
	}
	if err := m.Write([]byte("trusted-config")); err != nil {
		panic(err)
	}

	// 不可信侧曾拿到的可写共享映射必须先 munmap，否则 F_SEAL_WRITE 加不上。
	mappingID, err := m.Mmap(true, true, 4096)
	if err != nil {
		panic(err)
	}
	err = m.AddSeals(memfdseal.SealWrite)
	fmt.Println("seal while mapped:", errors.Is(err, syscall.EBUSY))

	if err := m.Munmap(mappingID); err != nil {
		panic(err)
	}

	// WRITE 隐含 SHRINK|GROW，最后用 SEAL 锁死封印集合。
	if err := m.AddSeals(memfdseal.SealWrite); err != nil {
		panic(err)
	}
	if err := m.AddSeals(memfdseal.SealSeal); err != nil {
		panic(err)
	}

	fd, err := m.Deliver()
	fmt.Println("deliverable:", err == nil, "fd:", fd)
	fmt.Println("seals:", m.Seals())

	// Output:
	// seal while mapped: true
	// deliverable: true fd: 1
	// seals: F_SEAL_SEAL|F_SEAL_SHRINK|F_SEAL_GROW|F_SEAL_WRITE
}
