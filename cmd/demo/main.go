// 演示：同样的写倾斜场景在纯 SI 与 SSI 下的不同结局。
package main

import (
	"fmt"

	"ssi"
)

func main() {
	fmt.Println("场景: A=100, B=100, 业务约束 A+B>=0")
	fmt.Println("T1 读 A、B 后扣 A；T2 并发读 A、B 后扣 B")
	fmt.Println()

	fmt.Println("=== 1) 纯快照隔离 (SI)，扣款 80 ===")
	runWriteSkew(true, 80)
	fmt.Println()
	fmt.Println("=== 2) 可串行化快照隔离 (SSI)，扣款 80 ===")
	runWriteSkew(false, 80)
	fmt.Println()
	fmt.Println("=== 3) 纯 SI，扣款 150（写倾斜导致真正违约）===")
	runWriteSkew(true, 150)
	fmt.Println()
	fmt.Println("=== 4) SSI，扣款 150（异常被阻止）===")
	runWriteSkew(false, 150)
}

func runWriteSkew(disableSSI bool, amount int64) {
	db := ssi.New()
	db.DisableSSI = disableSSI
	db.Put("A", 100)
	db.Put("B", 100)

	t1 := db.Begin()
	t2 := db.Begin()

	a1, _ := t1.Get("A")
	b1, _ := t1.Get("B")
	fmt.Printf("T1 读到 A=%d B=%d (sum=%d)，扣 A %d\n", a1, b1, a1+b1, amount)
	if a1+b1-amount >= 0 {
		t1.Set("A", a1-amount)
	}

	a2, _ := t2.Get("A")
	b2, _ := t2.Get("B")
	fmt.Printf("T2 读到 A=%d B=%d (sum=%d)，扣 B %d\n", a2, b2, a2+b2, amount)

	fmt.Printf("T1 commit: %s\n", result(t1.Commit()))

	var t2Err error
	if a2+b2-amount >= 0 {
		t2Err = t2.Set("B", b2-amount)
	}
	if t2Err != nil {
		fmt.Printf("T2 写 B 被中止: %v\n", t2Err)
	} else {
		t2Err = t2.Commit()
		fmt.Printf("T2 commit: %s\n", result(t2Err))
	}

	fa, fb := db.Get("A"), db.Get("B")
	status := "满足"
	if fa+fb < 0 {
		status = ">>> 违约! <<<"
	}
	fmt.Printf("最终: A=%d B=%d, A+B=%d, 约束 A+B>=0 %s\n", fa, fb, fa+fb, status)
}

func result(err error) string {
	if err == nil {
		return "OK"
	}
	return err.Error()
}
