package mpu_test

import (
	"errors"
	"fmt"

	"example.org/offlinecli/mpu"
)

// 演示完整工作流：区间编译、按优先级解释、安全切换时序与逐步验证。
func Example() {
	const space = uint64(128)

	intervals := []mpu.Interval{
		{Name: "flash", Start: 0, End: 64, Perm: mpu.PermR | mpu.PermX, Priority: 1},
		{Name: "ram", Start: 64, End: 128, Perm: mpu.PermR | mpu.PermW, Priority: 1},
		// 高优先级守卫区覆盖 flash 头部，撤销执行权限。
		{Name: "guard", Start: 0, End: 16, Perm: mpu.PermR, Priority: 5},
	}

	regions, err := mpu.Compile(intervals, space, mpu.PermNone, 8)
	if err != nil {
		panic(err)
	}
	dev, err := mpu.NewDevice(space, 8, regions)
	if err != nil {
		panic(err)
	}

	fmt.Println("addr 0 effective:", dev.Effective(0))   // 守卫区：只读
	fmt.Println("addr 32 effective:", dev.Effective(32)) // flash：RX
	fmt.Println("addr 96 effective:", dev.Effective(96)) // ram：RW

	target := []mpu.Region{
		{Slot: 0, Base: 0, SizeLog2: 6, SRD: 0, Perm: mpu.PermR},
		{Slot: 1, Base: 64, SizeLog2: 6, SRD: 0, Perm: mpu.PermR | mpu.PermW},
	}
	plan, err := dev.PlanUpdate(target)
	if err != nil {
		panic(err)
	}
	fmt.Println("register writes:", len(plan.Steps))

	// VerifyPlan 在微型地址空间上逐步穷举 W/X，保证无瞬时扩大。
	if err := mpu.VerifyPlan(plan); err != nil {
		panic(err)
	}
	fmt.Println("plan verified safe")

	// Output:
	// addr 0 effective: R
	// addr 32 effective: RX
	// addr 96 effective: RW
	// register writes: 10
	// plan verified safe
}

// 演示不可表达时的明确失败。
func ExampleCompile_misaligned() {
	ivs := []mpu.Interval{
		{Name: "bad", Start: 2, End: 10, Perm: mpu.PermR, Priority: 1},
	}
	_, err := mpu.Compile(ivs, 128, mpu.PermNone, 8)
	fmt.Println("is not representable:", errors.Is(err, mpu.ErrNotRepresentable))
	// Output:
	// is not representable: true
}
