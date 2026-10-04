package main

import "fmt"

func printResult(title string, leader *Leader, follower *Follower, res SimResult) {
	fmt.Printf("=== %s ===\n", title)
	fmt.Printf("leader  log: %s\n", formatLog(leader.log))
	fmt.Printf("follower 前: %s\n", formatLog(followerInitial))
	for _, e := range res.Events {
		fmt.Println("  " + e)
	}
	fmt.Printf("follower 后: %s\n", formatLog(follower.log))
	fmt.Printf("requests=%d rejections=%d converged=%v\n", res.Requests, res.Rejections, res.Converged)
	if err := verifyPrefix(leader, follower); err != nil {
		fmt.Printf("验证失败: %v\n\n", err)
	} else {
		fmt.Printf("验证通过: 跟随者前缀与领导者日志一致 (commit=%d)\n\n", follower.commitIndex)
	}
}

// followerInitial 记录场景运行前的跟随者日志，便于展示。
var followerInitial []Entry

func run(title string, leader *Leader, follower *Follower, cfg NetConfig) SimResult {
	followerInitial = append([]Entry(nil), follower.log...)
	res := simulate(leader, follower, cfg)
	printResult(title, leader, follower, res)
	return res
}

func main() {
	// 场景 1：空日志跟随者，顺序网络，全量复制。
	run("场景1: 空日志",
		NewLeader(3, makeLog(1, 1, 2, 2, 3)),
		NewFollower("F1", nil, 0),
		NetConfig{Seed: 1})

	// 场景 2：跟随者整段同一任期，与领导者后缀冲突 —— 冲突任期快速回退。
	run("场景2: 整段同任期冲突",
		NewLeader(2, makeLog(1, 1, 1, 2, 2, 2, 2, 2)),
		NewFollower("F2", makeLog(1, 1, 1, 1, 1, 1, 1, 1), 0),
		NetConfig{Seed: 2})

	// 场景 3：请求乱序、迟到、重复重试；跟随者带有分歧后缀与已提交前缀。
	run("场景3: 乱序+重复",
		NewLeader(4, makeLog(1, 1, 2, 3, 3, 4, 4, 4, 4, 4)),
		NewFollower("F3", makeLog(1, 1, 2, 2, 2, 2, 2), 3),
		NetConfig{Reorder: true, Duplicate: true, Seed: 7})
}
