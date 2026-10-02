// 运行样例：复现验收场景。
//
//	go run ./cmd/demo
package main

import (
	"fmt"
	"sync/atomic"
	"time"

	"coroutine"
)

var start = time.Now()

func logf(format string, args ...any) {
	fmt.Printf("[%6.1fms] %s\n", float64(time.Since(start).Microseconds())/1000.0,
		fmt.Sprintf(format, args...))
}

func main() {
	logf("=== 场景：容量为 2 的 Channel，P 写 3 个数据，C 消费 ===")
	ch := coroutine.New(2)

	done := make(chan struct{})
	go func() { // 生产者协程 P
		defer close(done)
		for i := 1; i <= 3; i++ {
			logf("P: Send(%d) 开始", i)
			ch.Send(i)
			logf("P: Send(%d) 完成", i)
		}
	}()

	time.Sleep(100 * time.Millisecond) // 让 P 阻塞在第 3 次写入
	logf("C: Recv() -> 读取一个数据，唤醒挂起的 P")
	v, _ := ch.Recv()
	logf("C: 收到 %v", v)

	<-done // 等 P 完成第 3 次写入
	v, _ = ch.Recv()
	logf("C: 收到 %v", v)
	v, _ = ch.Recv()
	logf("C: 收到 %v", v)

	logf("=== 场景：关闭通道，广播 EOF 给所有等待读的协程 ===")
	ch2 := coroutine.New(0)
	eof := make(chan int, 3)
	for i := 1; i <= 3; i++ {
		go func(id int) { // 等待读的协程
			logf("R%d: Recv() 挂起等待", id)
			_, ok := ch2.Recv()
			if !ok {
				logf("R%d: 收到 EOF", id)
			}
			eof <- id
		}(i)
	}
	time.Sleep(100 * time.Millisecond)
	logf("main: Close() 广播")
	ch2.Close()
	for i := 0; i < 3; i++ {
		<-eof
	}

	logf("=== 场景：无锁环形队列（2 生产者 x 2 消费者） ===")
	q := coroutine.NewRingQueue(8)
	out := make(chan int, 8)
	var consumed atomic.Int64
	for p := 0; p < 2; p++ {
		go func(base int) {
			for i := 1; i <= 4; i++ {
				for !q.Enqueue(base + i) {
				}
			}
		}(p * 10)
	}
	for c := 0; c < 2; c++ {
		go func() {
			for consumed.Load() < 8 {
				if v, ok := q.Dequeue(); ok {
					consumed.Add(1)
					out <- v.(int)
				}
			}
		}()
	}
	sum := 0
	for i := 0; i < 8; i++ {
		sum += <-out
	}
	logf("无锁队列消费 8 个元素，校验和 = %d（期望 1+2+3+4+11+12+13+14 = 60）", sum)
	logf("全部场景通过")
}
