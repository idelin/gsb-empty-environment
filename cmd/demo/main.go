// demo 是 diskdeque 的运行样例：双端 push/pop、随机区间读取、
// 事务回滚（提交点前故障）、断电/崩溃前滚（提交点后故障）。
package main

import (
	"errors"
	"fmt"
	"os"

	"diskdeque"
)

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func dump(d *diskdeque.Deque, start, n uint64) {
	vals, err := d.ReadRange(start, n)
	must(err)
	fmt.Printf("  ReadRange(%d,%d) = [", start, n)
	for i, v := range vals {
		if i > 0 {
			fmt.Print(" ")
		}
		fmt.Printf("%s", v)
	}
	fmt.Println("]")
}

func main() {
	dir := "demo-data"
	must(os.RemoveAll(dir))

	// 1) 基本双端操作（块容量 3，便于看到多块布局）
	d, err := diskdeque.Open(dir, &diskdeque.Options{BlockCapacity: 3, MaxBlocks: 256})
	must(err)

	tx := d.Begin()
	for _, v := range []string{"1", "2", "3", "4", "5"} {
		must(tx.PushRight([]byte(v)))
	}
	must(tx.Commit())

	tx = d.Begin()
	must(tx.PushLeft([]byte("L1")))
	must(tx.PushLeft([]byte("L2")))
	must(tx.Commit())

	fmt.Printf("1) len=%d headBlock=%d tailBlock=%d\n", d.Len(), d.Head(), d.Tail())
	dump(d, 0, d.Len())
	dump(d, 2, 3)

	// 2) 同事务左入 + 右出，提交点前注入故障 -> 两端修改一起回滚
	tx = d.Begin()
	must(tx.PushLeft([]byte("X")))
	popped, err := tx.PopRight()
	must(err)
	fmt.Printf("2) tx 内右出元素: %s\n", popped)
	diskdeque.SetFaultHook(diskdeque.FaultBeforeJournal, func() error {
		fmt.Println("   [注入] journal 写入前失败 -> 干净回滚")
		return diskdeque.ErrInjected
	})
	if err := tx.Commit(); errors.Is(err, diskdeque.ErrInjected) {
		fmt.Println("   提交失败，两端修改均未生效")
	} else {
		must(err)
	}
	diskdeque.ClearFaultHooks()
	fmt.Printf("   回滚后 len=%d\n", d.Len())
	dump(d, 0, d.Len())
	must(d.Close())

	// 3) 提交点后注入“断电/进程崩溃” -> 重开目录自动前滚重放
	d, err = diskdeque.Open(dir, nil)
	must(err)
	tx = d.Begin()
	must(tx.PushLeft([]byte("X")))
	must(tx.PushRight([]byte("Z")))
	diskdeque.SetFaultHook(diskdeque.FaultAfterJournal, func() error {
		fmt.Println("   [注入] journal 已 fsync 后断电（进程终止，不做清理）")
		return diskdeque.ErrInjected
	})
	if err := tx.Commit(); errors.Is(err, diskdeque.ErrInjected) {
		fmt.Println("   句柄失效，按崩溃处理；重新 Open 目录执行恢复")
	} else {
		must(err)
	}
	diskdeque.ClearFaultHooks()
	must(d.Close())

	d, err = diskdeque.Open(dir, nil)
	must(err)
	fmt.Printf("3) 恢复后 len=%d（事务完整生效）\n", d.Len())
	dump(d, 0, d.Len())

	// 4) 弹出最后一个元素：头尾一致
	for {
		tx = d.Begin()
		_, err := tx.PopLeft()
		if errors.Is(err, diskdeque.ErrEmpty) {
			must(tx.Rollback())
			break
		}
		must(err)
		must(tx.Commit())
	}
	fmt.Printf("4) 清空后 len=%d headBlock=%d tailBlock=%d（Head==Tail）\n",
		d.Len(), d.Head(), d.Tail())
	must(d.Close())
}
