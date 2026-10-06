package main

import (
	"fmt"
	"math"
	"os"
)

var failures int

func check(name string, ok bool, detail string) {
	status := "PASS"
	if !ok {
		status = "FAIL"
		failures++
	}
	fmt.Printf("  [%s] %s: %s\n", status, name, detail)
}

// 场景 1：三个持续积压的租户，权重 1:2:3，
// 长期服务比例应收敛到权重比。
func scenarioFairShare() {
	fmt.Println("场景1: 长期服务比例 (权重 1:2:3)")
	s := NewSim(8)
	s.AddTenant("A", 1, 4)
	s.AddTenant("B", 2, 4)
	s.AddTenant("C", 3, 4)
	// 每 tick 每租户到达 2 个 cost=2 作业（需求 4 > 各租户容量），保证持续积压
	for tick := 0; tick < 20000; tick++ {
		for id := 0; id < 3; id++ {
			s.Submit(tick, id, 2, "")
			s.Submit(tick, id, 2, "")
		}
	}
	s.Run(20000)
	s.Report()

	total := 0
	for _, t := range s.tenants {
		total += t.Served
	}
	want := []float64{1.0 / 6, 2.0 / 6, 3.0 / 6}
	for i, t := range s.tenants {
		got := float64(t.Served) / float64(total)
		check(fmt.Sprintf("租户%s服务占比", t.Name),
			math.Abs(got-want[i]) < 0.02,
			fmt.Sprintf("got=%.4f want=%.4f", got, want[i]))
	}
	check("并发上限", s.capViolations == 0 && s.maxRunning <= 8,
		fmt.Sprintf("峰值=%d 违例=%d", s.maxRunning, s.capViolations))
}

// 场景 2：租户 A 提交一个 cost=500 的巨作业（权重仅 1）。
// 它应靠逐轮积攒额度最终完成，且不饿死、不破坏他人比例。
func scenarioHugeJob() {
	fmt.Println("场景2: 巨成本作业 (cost=500, 权重=1)")
	s := NewSim(8)
	s.AddTenant("A", 1, 4)
	s.AddTenant("B", 2, 4)
	s.AddTenant("C", 3, 4)
	for tick := 0; tick < 20000; tick++ {
		for id := 0; id < 3; id++ {
			s.Submit(tick, id, 2, "")
			s.Submit(tick, id, 2, "")
		}
	}
	s.Submit(0, 0, 500, "huge") // tick 0 提交，前方仅 2 个小作业
	s.Run(20000)
	s.Report()

	huge := s.FindJob("huge")
	check("巨作业完成", huge != nil,
		func() string {
			if huge == nil {
				return "未完成"
			}
			return fmt.Sprintf("到达=%d 开始=%d 完成=%d (攒额度等待 %d tick)",
				huge.Arrive, huge.Start, huge.Finish, huge.Start-huge.Arrive)
		}())
	// 巨作业开始时刻：先排空 2 个 cost=2 小作业(约 4 轮)，再攒 500 额度(权重 1)
	if huge != nil {
		wait := huge.Start - huge.Arrive
		check("等待轮数合理", wait >= 495 && wait <= 525,
			fmt.Sprintf("wait=%d (期望≈504)", wait))
	}
	// B、C 的服务比例仍应为 2:3
	b, c := s.tenants[1], s.tenants[2]
	ratio := float64(b.Served) / float64(c.Served)
	check("B:C 比例不受巨作业影响", math.Abs(ratio-2.0/3.0) < 0.03,
		fmt.Sprintf("B/C=%.4f want=%.4f", ratio, 2.0/3.0))
	check("并发上限", s.capViolations == 0, fmt.Sprintf("违例=%d", s.capViolations))
}

// 场景 3：租户 C 带着积攒的额度（队里有巨作业）退出，
// 额度清零、排队作业丢弃、在跑作业跑完归还槽位；重新加入后从零开始。
func scenarioExitReset() {
	fmt.Println("场景3: 租户退出清零")
	s := NewSim(6)
	s.AddTenant("A", 1, 3)
	s.AddTenant("B", 1, 3)
	s.AddTenant("C", 1, 3)
	for tick := 0; tick < 3000; tick++ {
		s.Submit(tick, 0, 1, "")
		s.Submit(tick, 1, 1, "")
	}
	s.Submit(0, 2, 300, "c-huge") // C 队首巨作业 => 持续积压并攒额度
	s.Run(200) // C 权重 1，200 tick 后攒到约 200 额度，巨作业(300)仍未发

	cBefore := s.tenants[2].Credit
	s.Exit(200, 2)
	s.Run(1)
	c := s.tenants[2]
	check("退出前确有积攒", cBefore > 0, fmt.Sprintf("退出前额度=%d", cBefore))
	check("退出即清零", c.Credit == 0 && len(c.Queue) == 0,
		fmt.Sprintf("退出前额度=%d 退出后额度=%d 队列=%d", cBefore, c.Credit, len(c.Queue)))
	check("排队作业丢弃", c.Dropped == 1, fmt.Sprintf("dropped=%d", c.Dropped))
	check("退出不积攒", func() bool {
		for i := 0; i < 200; i++ {
			s.Step()
			if c.Credit != 0 {
				return false
			}
		}
		return true
	}(), "退出后 200 tick 额度保持 0")

	s.Rejoin(s.tick, 2)
	s.Step() // 应用重入事件，此 tick 尚无新作业
	check("重入从零开始", c.Active && c.Credit == 0,
		fmt.Sprintf("重入后额度=%d", c.Credit))
	s.Submit(s.tick+1, 2, 1, "c-new")
	s.Run(2000)
	check("重入后恢复服务", c.Finished >= 1,
		fmt.Sprintf("重入后完成=%d", c.Finished))
	check("槽位全部归还", s.running == cRunningAfterDrain(s),
		fmt.Sprintf("全局在跑=%d", s.running))
	s.Report()
	check("并发上限", s.capViolations == 0, fmt.Sprintf("违例=%d", s.capViolations))
}

func cRunningAfterDrain(s *Sim) int {
	// 所有到达停止后再跑到排空：A/B 队列里的作业也应全部完成
	s.Run(5000)
	n := 0
	for _, t := range s.tenants {
		n += len(t.Running)
	}
	return n // 应为 0
}

func main() {
	scenarioFairShare()
	fmt.Println()
	scenarioHugeJob()
	fmt.Println()
	scenarioExitReset()
	fmt.Println()
	if failures > 0 {
		fmt.Printf("共 %d 项检查失败\n", failures)
		os.Exit(1)
	}
	fmt.Println("全部检查通过")
}
