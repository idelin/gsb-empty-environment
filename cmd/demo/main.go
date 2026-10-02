// 运行样例：医生值班表「至少 1 人值班」不变量的写倾斜检测。
package main

import (
	"fmt"
	"sync"

	skew "skewguard"
)

const (
	onDuty  = "on"
	offDuty = "off"
)

func doctorKey(name string) string { return "doctor:" + name }

// atLeastOneOnDuty 是声明式不变量谓词：值班表至少 1 人在岗。
func atLeastOneOnDuty(v skew.View) bool {
	for _, kv := range v.Scan("doctor:") {
		if kv.Value == onDuty {
			return true
		}
	}
	return false
}

func onDutyCount(db *skew.DB) int {
	n := 0
	for _, kv := range db.ScanCommitted("doctor:") {
		if kv.Value == onDuty {
			n++
		}
	}
	return n
}

// requestLeave 模拟一次请假事务：先断言「我离开后仍有人值班」，再写请假并提交。
func requestLeave(tx *skew.Tx, name string) error {
	if err := tx.Assert("at-least-one-on-duty", atLeastOneOnDuty); err != nil {
		return err
	}
	tx.Set(doctorKey(name), offDuty)
	return tx.Commit()
}

func newRoster() *skew.DB {
	db := skew.New()
	db.RegisterConstraint("at-least-one-on-duty", atLeastOneOnDuty)
	db.Set(doctorKey("A"), onDuty)
	db.Set(doctorKey("B"), onDuty)
	return db
}

func main() {
	fmt.Println("=== 场景 1：顺序请假（无并发，第二人被不变量拦截）===")
	db := newRoster()
	if err := requestLeave(db.Begin(), "A"); err != nil {
		fmt.Println("A 请假失败:", err)
	} else {
		fmt.Println("A 请假成功，当前值班人数:", onDutyCount(db))
	}
	if err := requestLeave(db.Begin(), "B"); err != nil {
		fmt.Println("B 请假被中止:", err)
	}
	fmt.Println("最终值班人数:", onDutyCount(db))

	fmt.Println()
	fmt.Println("=== 场景 2：并发请假（写倾斜，提交阶段精准中止一人）===")
	db = newRoster()

	// 会合点：保证两个事务都先在各自快照上读到「对方在值班」，再同时进入提交阶段。
	ready := make(chan string, 2)
	release := make(chan struct{})
	var wg sync.WaitGroup
	results := make(map[string]error)
	var mu sync.Mutex

	for _, name := range []string{"A", "B"} {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			tx := db.Begin()
			other := "B"
			if name == "B" {
				other = "A"
			}
			v, _ := tx.Get(doctorKey(other))
			fmt.Printf("%s 的事务读取快照：看到 %s 状态 = %s（认为可以请假）\n", name, other, v)
			ready <- name
			<-release // 双方都读完快照后，同时提交
			err := requestLeave(tx, name)
			mu.Lock()
			results[name] = err
			mu.Unlock()
		}(name)
	}
	<-ready
	<-ready
	close(release)
	wg.Wait()

	for _, name := range []string{"A", "B"} {
		if err := results[name]; err == nil {
			fmt.Printf("%s 请假事务：提交成功\n", name)
		} else {
			fmt.Printf("%s 请假事务：%v（已安全回滚）\n", name, err)
		}
	}
	fmt.Println("最终值班人数:", onDutyCount(db), "（不变量守住：>= 1）")
}
