// demo 回放一次典型故障：旧主被暂停，租约早已过期，恢复后仍把排队写
// 请求发给存储；fencing token 保证它无法覆盖新主写入的值。
package main

import (
	"fmt"

	"leasim"
)

func main() {
	s := leasim.New(10) // 租约长度 10 tick

	s.Run([]leasim.Command{
		// 旧主 p1 以 epoch 1 获权并写入 v1
		{Op: "campaign", Node: "p1", Epoch: 1},
		{Op: "deliver"},
		{Op: "write", Node: "p1", ReqID: "w1", Value: "v1"},
		{Op: "deliver"},
		// p1 被暂停；暂停期间应用层仍在排队写请求
		{Op: "pause", Node: "p1"},
		{Op: "write", Node: "p1", ReqID: "w2", Value: "stale"},
		// 时钟推进到 20：租约 [0,10) 早已过期
		{Op: "advance", To: 20},
		// 新主 p2 以 epoch 2 获权（投票者承诺在盘上，重启也不丢）
		{Op: "campaign", Node: "p2", Epoch: 2},
		{Op: "deliver"},
		{Op: "write", Node: "p2", ReqID: "w3", Value: "v2"},
		{Op: "deliver"},
		// 旧主恢复，把排队的过期写发给存储 —— 必须被拒绝
		{Op: "resume", Node: "p1"},
		{Op: "deliver"},
	})

	fmt.Println("=== 事件日志 ===")
	for _, r := range s.Records() {
		fmt.Println(r)
	}

	fmt.Println("\n=== 拒绝原因 ===")
	for _, r := range s.Rejections() {
		fmt.Println(r)
	}

	fmt.Println("\n=== 已提交写序列 ===")
	for _, w := range s.Committed() {
		fmt.Printf("tick %d: %s=%s (epoch %d)\n", w.At, w.ReqID, w.Value, w.Epoch)
	}

	// 日志回放验证确定性
	replay := leasim.Replay(10, s.Log())
	if fmt.Sprint(replay.Records()) != fmt.Sprint(s.Records()) {
		fmt.Println("\n回放结果不一致！")
	} else {
		fmt.Printf("\n日志回放一致：%d 条命令、%d 条事件、%d 条提交\n",
			len(s.Log()), len(s.Records()), len(s.Committed()))
	}
}
