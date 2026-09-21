// demo 演示有界队列发布订阅：
//   - Block 订阅者通过背压保证不丢消息；
//   - DropNewest 订阅者在队列满时显式丢弃并统计；
//   - Close 保证在途发布排空后再关闭订阅 channel。
package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"pairwise-gsb/pubsub/pubsub"
)

func main() {
	b := pubsub.New[string]()

	blocker, err := b.Subscribe(
		pubsub.WithCapacity[string](2),
		pubsub.WithPolicy[string](pubsub.PolicyBlock),
	)
	must(err)

	dropper, err := b.Subscribe(
		pubsub.WithCapacity[string](2),
		pubsub.WithPolicy[string](pubsub.PolicyDropNewest),
		pubsub.WithOnDrop[string](func(m string) {
			fmt.Printf("[dropper] 显式丢弃: %s\n", m)
		}),
	)
	must(err)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for m := range blocker.C() {
			fmt.Printf("[blocker] 收到: %s\n", m)
		}
		fmt.Println("[blocker] channel 已关闭")
	}()
	go func() {
		defer wg.Done()
		for m := range dropper.C() {
			fmt.Printf("[dropper] 收到: %s\n", m)
			time.Sleep(10 * time.Millisecond) // 故意放慢，制造丢弃
		}
		fmt.Println("[dropper] channel 已关闭")
	}()

	messages := []string{"m1", "m2", "m3", "m4", "m5"}
	for _, m := range messages {
		res, err := b.Publish(context.Background(), m)
		must(err)
		fmt.Printf("发布 %s: 入队=%d 丢弃=%d\n", m, res.Delivered, res.Dropped)
	}

	must(b.Close(context.Background()))
	wg.Wait()

	fmt.Printf("dropper 累计丢弃: %d\n", dropper.Dropped())

	// 关闭后发布返回 ErrClosed。
	if _, err := b.Publish(context.Background(), "late"); err != nil {
		fmt.Printf("关闭后发布: %v\n", err)
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
