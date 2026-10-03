// 运行样例：弱有序传感器事件流的拓扑重排稳定化窗口。
package main

import (
	"fmt"

	"stabilizer/stabilizer"
)

func main() {
	const K = 3 // 最大逻辑时间乱序窗口
	s := stabilizer.New(K)

	// 含微小时间回退的杂乱传感器事件流。
	stream := []stabilizer.Event{
		{Timestamp: 10, Payload: "temp=21.0"},
		{Timestamp: 12, Payload: "temp=21.2"},
		{Timestamp: 11, Payload: "temp=21.1"}, // 回退 1
		{Timestamp: 15, Payload: "temp=21.5"},
		{Timestamp: 13, Payload: "temp=21.3"}, // 回退 2
		{Timestamp: 14, Payload: "temp=21.4"}, // 回退 1
		{Timestamp: 18, Payload: "temp=21.8"},
		{Timestamp: 16, Payload: "temp=21.6"}, // 回退 2
		{Timestamp: 17, Payload: "temp=21.7"}, // 回退 1
		{Timestamp: 20, Payload: "temp=22.0"},
		{Timestamp: 14, Payload: "temp=late"}, // 迟到：将被丢弃
	}

	fmt.Printf("稳定化窗口 K=%d，乱序输入 %d 条：\n", K, len(stream))
	for _, in := range stream {
		released := s.Push(in.Timestamp, in.Payload)
		fmt.Printf("  输入 ts=%-3d %-12q → 水位线=%-3d 缓冲=%d", in.Timestamp, in.Payload, s.Watermark(), s.Buffered())
		for _, ev := range released {
			fmt.Printf("  [释放 ts=%d %q]", ev.Timestamp, ev.Payload)
		}
		fmt.Println()
	}

	fmt.Println("输入结束，Flush 排空缓冲：")
	for _, ev := range s.Flush() {
		fmt.Printf("  [释放 ts=%d %q]\n", ev.Timestamp, ev.Payload)
	}
	fmt.Printf("统计：释放=%d 迟到丢弃=%d\n", s.Released(), s.Late())
}
