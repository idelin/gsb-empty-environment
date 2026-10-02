package main

import "fmt"

func main() {
	s := NewScheduler()

	// 员工 E1 持有电焊证书，生效期 [10, 100)，复审期截止 95。
	s.AddCertificate(Certificate{
		ID: "CERT-WELD-1", EmployeeID: "E1", Skill: "电焊",
		Valid: Interval{Start: 10, End: 100}, ReviewDue: 95,
	})

	fmt.Println("== 场景 1：排班 [80, 120)，证书仅覆盖到 95（复审期） ==")
	ok, missing := s.Schedule(Shift{ID: "SHIFT-A", EmployeeID: "E1", Skill: "电焊", Period: Interval{Start: 80, End: 120}})
	if ok {
		fmt.Println("排班成功")
	} else {
		fmt.Printf("拒绝排班：时段 %v 缺少「电焊」资质\n", missing)
	}

	fmt.Println("\n== 场景 2：排班 [80, 95)，资质全程有效 ==")
	ok, _ = s.Schedule(Shift{ID: "SHIFT-B", EmployeeID: "E1", Skill: "电焊", Period: Interval{Start: 80, End: 95}})
	fmt.Printf("排班结果: %v\n", ok)

	fmt.Println("\n== 场景 3：知悉时间 200 收到通知，证书在有效时间 60 被追溯吊销 ==")
	violations, err := s.RevokeCertificate("CERT-WELD-1", 60, 200)
	if err != nil {
		fmt.Println("吊销失败:", err)
		return
	}
	if len(violations) == 0 {
		fmt.Println("无受影响班次")
	}
	for _, v := range violations {
		fmt.Println("【预警】", v)
	}
}
