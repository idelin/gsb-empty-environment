// demo 演示聚合器在故障点崩溃后的恢复过程，
// 并分别打印“计算状态恢复”与“效果确认”两个进度。
package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"aggregator"
)

func printProgress(tag string, p aggregator.Progress) {
	fmt.Printf("[%s]\n", tag)
	fmt.Printf("  计算状态: restored=%v inputSeq=%d sums=%v\n",
		p.Compute.RestoredFromCheckpoint, p.Compute.InputSeq, p.Compute.Sums)
	fmt.Printf("  效果确认: emitted=%d pending=%d%v confirmed=%d\n",
		p.Effect.Emitted, p.Effect.Pending, p.Effect.PendingIDs, p.Effect.Confirmed)
}

func main() {
	dir, err := os.MkdirTemp("", "aggregator-demo")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)

	store := aggregator.NewFileStore(filepath.Join(dir, "checkpoint.json"))
	recv := aggregator.NewReceiver()

	// 阶段 1：正常处理 4 条事件（每 2 条结算一次），发布但不确认，模拟崩溃。
	agg, err := aggregator.Open(store, recv, 2)
	if err != nil {
		log.Fatal(err)
	}
	for _, v := range []int64{3, 4, 1, 2} {
		if err := agg.Add("views", v); err != nil {
			log.Fatal(err)
		}
	}
	if err := agg.PublishPending(); err != nil {
		log.Fatal(err)
	}
	printProgress("阶段1: 发布后崩溃前", agg.Progress())
	fmt.Println("  *** 崩溃（确认未发出） ***")

	// 阶段 2：重启恢复，重发同 ID 结果，幂等接收方去重。
	agg2, err := aggregator.Open(store, recv, 2)
	if err != nil {
		log.Fatal(err)
	}
	printProgress("阶段2: 恢复后（重发前）", agg2.Progress())
	if err := agg2.PublishPending(); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("  接收方: unique=%d total=%d duplicateDeliveries=%d\n",
		recv.UniqueCount(), recv.Total("views"), recv.DuplicateCalls())

	// 阶段 3：确认所有待发结果，效果确认进度收口。
	for _, id := range agg2.Progress().Effect.PendingIDs {
		if err := agg2.Ack(id); err != nil {
			log.Fatal(err)
		}
	}
	printProgress("阶段3: 全部确认后", agg2.Progress())
}
