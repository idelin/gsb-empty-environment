// demo 演示批处理器：满批触发、超时触发、取消移除与失败映射。
package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"batcher"
)

func main() {
	// 执行函数：把批内字符串转大写；item 为 "fail" 时整批失败。
	exec := func(_ context.Context, items []any) ([]any, error) {
		fmt.Printf("[exec] 批次触发，共 %d 项: %v\n", len(items), items)
		out := make([]any, len(items))
		for i, it := range items {
			s := it.(string)
			if s == "fail" {
				return nil, errors.New("模拟的批执行失败")
			}
			out[i] = strings.ToUpper(s)
		}
		return out, nil
	}

	b := batcher.New(batcher.Options{MaxSize: 3, MaxWait: 200 * time.Millisecond, Exec: exec})
	wait := func(ch <-chan batcher.Result) {
		r := <-ch
		if r.Err != nil {
			fmt.Printf("  -> 结果: 错误 %v\n", r.Err)
		} else {
			fmt.Printf("  -> 结果: %v\n", r.Value)
		}
	}

	fmt.Println("== 场景 1: 攒满 3 项立即触发 ==")
	s1a := b.Submit(context.Background(), "alpha")
	s1b := b.Submit(context.Background(), "beta")
	s1c := b.Submit(context.Background(), "gamma")
	wait(s1a)
	wait(s1b)
	wait(s1c)

	fmt.Println("== 场景 2: 批未满，等待 200ms 超时触发 ==")
	ch1 := b.Submit(context.Background(), "delta")
	ch2 := b.Submit(context.Background(), "epsilon")
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); wait(ch1) }()
	go func() { defer wg.Done(); wait(ch2) }()
	wg.Wait()

	fmt.Println("== 场景 3: 取消项从批中移除 ==")
	ctx, cancel := context.WithCancel(context.Background())
	chCancel := b.Submit(ctx, "will-be-cancelled")
	chKeep := b.Submit(context.Background(), "zeta")
	cancel()
	wg.Add(2)
	go func() { defer wg.Done(); wait(chCancel) }()
	go func() { defer wg.Done(); wait(chKeep) }()
	wg.Wait()

	fmt.Println("== 场景 4: 批执行失败映射到每个存活请求 ==")
	chA := b.Submit(context.Background(), "fail")
	chB := b.Submit(context.Background(), "eta")
	wg.Add(2)
	go func() { defer wg.Done(); wait(chA) }()
	go func() { defer wg.Done(); wait(chB) }()
	wg.Wait()
}
