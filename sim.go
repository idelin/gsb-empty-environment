package main

import (
	"fmt"
	"math/rand"
	"strings"
)

// message 是网络中在途的请求或回复。
type message struct {
	isReply   bool
	req       AppendRequest
	rep       AppendReply
	deliverAt int
}

// NetConfig 控制网络行为：乱序投递与重复重试。
type NetConfig struct {
	Reorder   bool // 请求/回复可能乱序、迟到
	Duplicate bool // 请求可能被重复发送（重试）
	Seed      int64
}

// SimResult 记录一次模拟的统计与事件。
type SimResult struct {
	Events     []string
	Requests   int // 领导者实际发出的请求数
	Rejections int // 被拒绝的次数
	Converged  bool
}

// simulate 运行复制直到跟随者前缀与领导者日志一致且提交索引同步。
// 每一步都校验：已提交前缀永远不被截断。
func simulate(leader *Leader, follower *Follower, cfg NetConfig) SimResult {
	rng := rand.New(rand.NewSource(cfg.Seed))
	res := SimResult{}
	nextIndex := len(leader.log) + 1
	matchIndex := 0
	var pending []message
	tick := 0
	maxTicks := 100000

	// 已提交前缀快照，用于检测非法截断。
	committed := append([]Entry(nil), follower.log[:follower.commitIndex]...)

	send := func() {
		req := leader.requestFor(nextIndex)
		delay := 0
		if cfg.Reorder {
			delay = rng.Intn(4)
		}
		pending = append(pending, message{req: req, deliverAt: tick + delay})
		res.Requests++
		if cfg.Duplicate && rng.Intn(3) == 0 {
			// 重试：同一请求稍后再发一份。
			pending = append(pending, message{req: req, deliverAt: tick + delay + 1 + rng.Intn(3)})
			res.Requests++
		}
	}

	checkCommitted := func() {
		for i := 0; i < follower.commitIndex && i < len(committed); i++ {
			if follower.log[i] != committed[i] {
				panic(fmt.Sprintf("committed entry %d was truncated: %v -> %v",
					i+1, committed[i], follower.log[i]))
			}
		}
		if follower.commitIndex > len(committed) {
			committed = append(committed, follower.log[len(committed):follower.commitIndex]...)
		}
	}

	send()
	for tick = 0; tick < maxTicks; tick++ {
		var next []message
		// 投递到期的请求。
		for _, m := range pending {
			if m.isReply || m.deliverAt > tick {
				next = append(next, m)
				continue
			}
			rep, events := follower.handleAppend(m.req)
			for _, e := range events {
				res.Events = append(res.Events, fmt.Sprintf("tick %03d  %s", tick, e))
			}
			checkCommitted()
			if !rep.Success {
				res.Rejections++
			}
			delay := 0
			if cfg.Reorder {
				delay = rng.Intn(4)
			}
			next = append(next, message{isReply: true, rep: rep, deliverAt: tick + delay})
		}
		pending = next
		next = nil
		// 投递到期的回复。
		for _, m := range pending {
			if !m.isReply || m.deliverAt > tick {
				next = append(next, m)
				continue
			}
			if m.rep.Success {
				if m.rep.MatchIndex > matchIndex {
					matchIndex = m.rep.MatchIndex
				}
				if nextIndex < matchIndex+1 {
					nextIndex = matchIndex + 1
				}
			} else {
				back := leader.backup(m.rep)
				// 迟到的旧拒绝不得把 nextIndex 退到已确认匹配的前缀之内。
				if back < matchIndex+1 {
					back = matchIndex + 1
				}
				if back < nextIndex {
					nextIndex = back
				}
			}
		}
		pending = next

		inflightReq := false
		for _, m := range pending {
			if !m.isReply {
				inflightReq = true
			}
		}
		if matchIndex == len(leader.log) && leader.commitIndex < len(leader.log) {
			leader.commitIndex = len(leader.log)
		}
		done := matchIndex == len(leader.log) &&
			follower.commitIndex == leader.commitIndex &&
			len(pending) == 0
		if done {
			res.Converged = true
			break
		}
		if !inflightReq {
			send()
		}
	}
	return res
}

// verifyPrefix 校验跟随者日志前缀与领导者日志完全一致。
func verifyPrefix(leader *Leader, follower *Follower) error {
	if len(follower.log) < len(leader.log) {
		return fmt.Errorf("follower log too short: %d < %d", len(follower.log), len(leader.log))
	}
	for i, e := range leader.log {
		if follower.log[i] != e {
			return fmt.Errorf("prefix mismatch at index %d: follower=%v leader=%v", i+1, follower.log[i], e)
		}
	}
	return nil
}

func formatLog(log []Entry) string {
	if len(log) == 0 {
		return "<empty>"
	}
	parts := make([]string, len(log))
	for i, e := range log {
		parts[i] = fmt.Sprintf("%d:%s", i+1, e)
	}
	return strings.Join(parts, " ")
}

func makeLog(terms ...int) []Entry {
	log := make([]Entry, len(terms))
	for i, t := range terms {
		log[i] = Entry{Term: t, Command: fmt.Sprintf("cmd%d", i+1)}
	}
	return log
}
