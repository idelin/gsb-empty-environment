package pubcheck_test

import (
	"fmt"

	"example.org/netsec/q0398/pubcheck"
)

// 演示一次缺少释放/获取同步的惰性初始化发布，检查器返回具体竞争对与诊断链。
func ExampleChecker_Check_unsafe() {
	c := pubcheck.NewChecker()
	ref := pubcheck.Ref{Object: "config", Generation: 1}

	c.FieldWrite("init", ref, "endpoint")
	c.Publish("init", ref)
	c.AcquireRead("worker", ref)

	for _, race := range c.Check() {
		fmt.Println(race.Message())
		for _, link := range race.Chain {
			if !link.HasEdge {
				fmt.Println("missing:", link.Stage, "-", link.Description)
			}
		}
	}
	// Output:
	// read lacks happens-before edge from field write: E0(FieldWrite init config#g1.endpoint) vs E2(AcquireRead worker config#g1)
	// missing: publish -> release-sync - publish is not followed by a release synchronization
}

// 演示通过屏障（WaitGroup 语义）完成的安全发布，检查器不报告竞争。
func ExampleChecker_Check_safe() {
	c := pubcheck.NewChecker()
	c.NewSync("ready", pubcheck.SyncBarrier)
	ref := pubcheck.Ref{Object: "config", Generation: 1}

	c.FieldWrite("init", ref, "endpoint")
	c.Publish("init", ref)
	c.ReleaseSync("init", "ready")
	c.AcquireSync("worker", "ready")
	c.AcquireRead("worker", ref)

	fmt.Println("races:", len(c.Check()))
	// Output:
	// races: 0
}
