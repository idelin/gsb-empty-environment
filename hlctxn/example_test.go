package hlctxn_test

import (
	"fmt"
	"time"

	"prescreen.local/ps-dc61858403/hlctxn"
)

// ExampleCluster 演示带时钟偏斜的多节点集群上的事务提交流程。
func ExampleCluster() {
	maxOffset := 50 * time.Millisecond
	// 三个节点注入不同偏移，绝对值均 <= maxOffset。
	c := hlctxn.NewRealCluster(3, []time.Duration{
		-50 * time.Millisecond, 0, 50 * time.Millisecond,
	}, maxOffset)

	// 写事务：在键上放置意图并原子提交。
	txn := c.Begin(0, 0)
	if err := txn.Put("greeting", "hello-hlc"); err != nil {
		fmt.Println("put error:", err)
		return
	}
	commitTS, err := txn.Commit()
	if err != nil {
		fmt.Println("commit error:", err)
		return
	}

	// 读事务：自动处理可能的不确定性读重启。
	reader := c.Begin(1, 0)
	value, found, restarts, err := reader.GetWithRestart("greeting")
	if err != nil {
		fmt.Println("get error:", err)
		return
	}
	if _, err := reader.Commit(); err != nil {
		fmt.Println("reader commit error:", err)
		return
	}

	fmt.Printf("value=%q found=%v restarts=%d commitLogical=%d\n",
		value, found, restarts, commitTS.Logical)
	// Output: value="hello-hlc" found=true restarts=0 commitLogical=0
}

// ExampleCluster_runWithRetry 演示冲突下的有界自动重试与优先级推戳。
func ExampleCluster_runWithRetry() {
	c := hlctxn.NewRealCluster(2,
		[]time.Duration{-time.Millisecond, time.Millisecond}, 10*time.Millisecond)

	// 高优事务先持有键的 PENDING 意图。
	holder := c.Begin(0, 100)
	if err := holder.Put("hot", "held"); err != nil {
		fmt.Println("holder put:", err)
		return
	}

	// 低优写者通过 RunWithRetry 在冲突退避后重试；这里直接演示高优读者推戳。
	pusher := c.Begin(1, 200)
	_, _, _, err := pusher.GetWithRestart("hot")
	fmt.Println("high-priority read error:", err)

	commitTS, _ := holder.Commit()
	view, _ := c.LookupTxn(holder.ID())
	fmt.Printf("holder status=%s commitBoundHeld=%v\n",
		view.Status, !commitTS.Less(view.WriteTS))
	// Output:
	// high-priority read error: <nil>
	// holder status=COMMITTED commitBoundHeld=true
}
