// 运行样例：动态排班技能资质时态匹配。
package main

import (
	"fmt"

	scheduler "bitemporal-scheduler"
)

func main() {
	s := scheduler.NewScheduler()

	// 员工 E1 的焊工证书：有效时间 [10, 100)（生效期至复审期）
	s.AddCertificate(scheduler.Certificate{
		ID: "C1", EmployeeID: "E1", Skill: "welding",
		ValidFrom: 10, ValidTo: 100,
	})
	fmt.Println("证书 C1: 员工 E1 焊接资质, 有效时间 [10, 100)")

	// 1) 排班 [80, 120)：尾部 [100,120) 超出复审期 → 拒绝
	fmt.Println("\n== 场景 1: 排班 [80, 120) ==")
	err := s.Schedule(scheduler.Shift{ID: "S1", EmployeeID: "E1", Skill: "welding", Start: 80, End: 120}, 0)
	fmt.Println("结果:", err)

	// 2) 排班 [70, 90) 与 [20, 50)：全程覆盖 → 接受
	fmt.Println("\n== 场景 2: 排班 [70, 90) 与 [20, 50) ==")
	for _, sh := range []scheduler.Shift{
		{ID: "SA", EmployeeID: "E1", Skill: "welding", Start: 70, End: 90},
		{ID: "SB", EmployeeID: "E1", Skill: "welding", Start: 20, End: 50},
	} {
		if err := s.Schedule(sh, 0); err != nil {
			fmt.Println("结果:", err)
		} else {
			fmt.Printf("班次 %s [%d, %d) 排班成功\n", sh.ID, sh.Start, sh.End)
		}
	}

	// 3) 追溯吊销：知悉时间 500 收到通知，证书在有效时间 60 被吊销
	fmt.Println("\n== 场景 3: 追溯吊销 (生效点 60, 知悉时间 500) ==")
	violations, err := s.Revoke("C1", 60, 500)
	if err != nil {
		fmt.Println("吊销失败:", err)
		return
	}
	if len(violations) == 0 {
		fmt.Println("无违规班次")
	}
	for _, v := range violations {
		fmt.Println(v)
	}
}
