package sim

import (
	"container/heap"
	"math"
	"time"

	"prescreen.local/ps-9f073c0c88/retry"
)

// Run 执行仿真并返回测量结果。
func (s *Simulator) Run() *Result {
	heap.Init(&s.evQ)

	// 调度各入口节点的首个外部到达。
	for name, rate := range s.cfg.Offered {
		if rate > 0 {
			s.scheduleArrival(name, 0)
		}
	}

	for s.evQ.Len() > 0 {
		ev := heap.Pop(&s.evQ).(*event)
		if ev.at > s.cfg.Duration {
			break
		}
		s.now = ev.at
		switch ev.kind {
		case evArrival:
			s.handleArrival(ev)
		case evAttemptComplete:
			s.handleComplete(ev)
		case evHedge:
			s.handleHedge(ev)
		case evRetry:
			s.handleRetry(ev)
		}
	}

	return s.measure()
}

func (s *Simulator) scheduleArrival(node string, at float64) {
	s.cid++
	c := &call{id: s.cid, node: node, arrival: at, measured: at >= s.cfg.Warmup}
	heap.Push(&s.evQ, &event{at: at, kind: evArrival, seq: s.nextSeq(), call: c})
}

func (s *Simulator) nextSeq() int64 {
	s.evSeq++
	return s.evSeq
}

func (s *Simulator) push(ev *event) {
	ev.seq = s.nextSeq()
	heap.Push(&s.evQ, ev)
}

func (s *Simulator) handleArrival(ev *event) {
	c := ev.call
	node := s.nodes[c.node]
	if c.measured {
		s.metrics[c.node].LogicalArrivals++
	}

	// 泊松过程：调度下一个外部到达。
	if rate := s.cfg.Offered[c.node]; rate > 0 {
		gap := -math.Log(1-s.rng.Float64()) / rate
		nextAt := s.now + gap
		if nextAt < s.cfg.Duration {
			s.scheduleArrival(c.node, nextAt)
		}
	}

	// 熔断器准入（与 retry.Breaker 三态一致，虚拟时间驱动）。
	if !node.breaker.Allow() {
		if c.measured {
			s.metrics[c.node].BreakerRejected++
		}
		node.breaker.Release()
		s.settle(c, false)
		return
	}

	maxAttempts := 1
	if len(s.in[c.node]) > 0 {
		maxAttempts = s.edgeFor(c.node).cfg.Retry.MaxAttempts
	}
	if maxAttempts <= 0 {
		maxAttempts = 1
	}
	c.maxAttempts = maxAttempts
	if dl := s.deadlineFor(c.node); dl > 0 {
		c.deadline = s.now + dl.Seconds()
	}
	c.attempts = 1
	c.pending = 1
	// 在入边滑动窗口登记该逻辑请求（入口节点登记到虚拟入边）。
	if re := s.edgeFor(c.node); re != nil {
		re.logical = append(re.logical, attemptRecord{at: s.now})
	}
	s.fireAttempt(c, false)
}

func (s *Simulator) deadlineFor(node string) time.Duration {
	if ins := s.in[node]; len(ins) > 0 {
		return ins[0].Deadline
	}
	return 0
}

// currentLoadRatio 用窗口内物理尝试速率估计节点当前归一化负载。
func (s *Simulator) currentLoadRatio(n *rtNode) float64 {
	cutoff := s.now - s.cfg.Window.Seconds()
	kept := n.attempts[:0]
	for _, r := range n.attempts {
		if r.at > cutoff {
			kept = append(kept, r)
		}
	}
	n.attempts = kept
	if n.cfg.Capacity <= 0 {
		return 0
	}
	rate := float64(len(n.attempts)) / s.cfg.Window.Seconds()
	return rate / n.cfg.Capacity
}

func (s *Simulator) fireAttempt(c *call, hedged bool) {
	n := s.nodes[c.node]
	n.attempts = append(n.attempts, attemptRecord{at: s.now})
	if c.measured {
		s.metrics[c.node].Attempts++
	}
	svc := n.cfg.ServiceTime
	if svc <= 0 {
		svc = 1e-6
	}
	s.push(&event{at: s.now + svc, kind: evAttemptComplete, call: c, edge: s.edgeFor(c.node)})
}

func (s *Simulator) edgeFor(node string) *rtEdge {
	if ins := s.in[node]; len(ins) > 0 {
		return s.edges[edgeKey(ins[0].From, ins[0].To)]
	}
	return nil
}

func (s *Simulator) handleComplete(ev *event) {
	c := ev.call
	if c.settled {
		return
	}
	n := s.nodes[c.node]
	c.pending--
	re := s.edgeFor(c.node)

	// 按当前负载采样成功/失败。
	p := n.cfg.Curve.PFail(s.currentLoadRatio(n))
	fail := s.rng.Float64() < p
	if c.measured {
		if fail {
			n.breaker.OnFailure()
			s.metrics[c.node].Failures++
		} else {
			n.breaker.OnSuccess()
			s.metrics[c.node].Successes++
		}
	} else {
		if fail {
			n.breaker.OnFailure()
		} else {
			n.breaker.OnSuccess()
		}
	}

	if !fail {
		n.breaker.Release()
		s.settle(c, true)
		return
	}
	c.failedAttempt = c.attempts

	// 失败后调度对冲或退避重试。
	if re != nil {
		cfg := re.cfg
		if c.pending > 0 && cfg.HedgeDelay > 0 && c.hedges == 0 && c.attempts < c.maxAttempts {
			// 仍有在途尝试：考虑对冲。
			s.push(&event{at: s.now + cfg.HedgeDelay.Seconds(), kind: evHedge, call: c, edge: re})
			// 无对冲可发，等待在途尝试完成即可（其完成事件会继续处理）。
			return
		}
		if c.attempts >= c.maxAttempts {
			n.breaker.Release()
			s.settle(c, false)
			return
		}
		// 无在途尝试：退避后普通重试。
		delay := retry.Backoff(cfg.BaseBackoff, cfg.BackoffMultiplier, cfg.Jitter,
			requestID(c.id), c.attempts)
		s.push(&event{at: s.now + delay.Seconds(), kind: evRetry, call: c, edge: re})
		return
	}
	n.breaker.Release()
	s.settle(c, false)
}

func (s *Simulator) handleHedge(ev *event) {
	c := ev.call
	re := ev.edge
	if c.settled || c.pending == 0 {
		return
	}
	if c.attempts >= c.maxAttempts {
		return
	}
	if reason := s.admitExtra(re, c, true); reason != retry.RejectNone {
		s.recordReject(c, reason)
		// 对冲被预算/截止期拒绝时仍有在途尝试，熔断名额由其完成路径释放，
		// 这里仅放弃对冲，不结算整个请求。
		return
	}
	c.attempts++
	c.hedges++
	c.pending++
	s.fireAttempt(c, true)
}

func (s *Simulator) handleRetry(ev *event) {
	c := ev.call
	re := ev.edge
	if c.settled {
		return
	}
	if c.attempts >= c.maxAttempts {
		s.nodes[c.node].breaker.Release()
		s.settle(c, false)
		return
	}
	if reason := s.admitExtra(re, c, false); reason != retry.RejectNone {
		s.recordReject(c, reason)
		s.nodes[c.node].breaker.Release()
		s.settle(c, false)
		return
	}
	c.attempts++
	c.pending++
	s.fireAttempt(c, false)
}

// admitExtra 复现运行时预算公式与截止期校验（熔断器在尝试发起时已占名额）。
func (s *Simulator) admitExtra(re *rtEdge, c *call, hedge bool) retry.RejectReason {
	cutoff := s.now - re.window
	kept := re.logical[:0]
	for _, r := range re.logical {
		if r.at > cutoff {
			kept = append(kept, r)
		}
	}
	re.logical = kept
	keptExtras := re.extras[:0]
	for _, r := range re.extras {
		if r.at > cutoff {
			keptExtras = append(keptExtras, r)
		}
	}
	re.extras = keptExtras

	// 速率口径（与 steady 求解器一致）：窗口内额外尝试速率不得超过
	// max(MinRetryRate, BudgetRatio × 常规逻辑速率)。
	logicalRate := float64(len(re.logical)) / re.window
	extraRate := float64(len(re.extras)) / re.window
	allowedRate := re.cfg.Retry.MinRetryRate
	if b := re.cfg.Retry.BudgetRatio * logicalRate; b > allowedRate {
		allowedRate = b
	}
	if !re.cfg.Retry.UnlimitedBudget && extraRate >= allowedRate {
		return retry.RejectBudget
	}
	// 截止期：剩余时间必须覆盖（退避/对冲延迟）+ 下游最小处理耗时。
	if c.deadline > 0 {
		minProc := re.cfg.MinProcessingTime.Seconds()
		delay := 0.0
		if hedge {
			delay = re.cfg.HedgeDelay.Seconds()
		} else {
			delay = retry.Backoff(re.cfg.BaseBackoff, re.cfg.BackoffMultiplier,
				re.cfg.Jitter, requestID(c.id), c.attempts).Seconds()
		}
		if c.deadline-s.now <= delay+minProc {
			return retry.RejectDeadline
		}
	}
	re.extras = append(re.extras, attemptRecord{at: s.now})
	return retry.RejectNone
}

func (s *Simulator) recordReject(c *call, reason retry.RejectReason) {
	if !c.measured {
		return
	}
	m := s.metrics[c.node]
	switch reason {
	case retry.RejectBudget:
		m.RetriesRejected++
	case retry.RejectBreaker:
		m.BreakerRejected++
	case retry.RejectDeadline:
		m.DeadlineReject++
	}
}

// settle 结束一个逻辑请求；成功时沿出边按 fanout 发起下游调用。
func (s *Simulator) settle(c *call, success bool) {
	if c.settled {
		return
	}
	c.settled = true
	if !success && c.measured {
		// 最终失败已在 Failures 中按尝试累计，这里不重复计数。
	}
	if !success {
		// 释放可能仍占有的熔断名额由调用方处理。
	}

	for _, eg := range s.out[c.node] {
		n := int(math.Round(eg.Fanout))
		if eg.Fanout <= 0 {
			continue
		}
		if n <= 0 {
			n = 1
		}
		for i := 0; i < n; i++ {
			s.cid++
			child := &call{
				id:       s.cid,
				node:     eg.To,
				arrival:  s.now,
				measured: s.now >= s.cfg.Warmup,
			}
			s.push(&event{at: s.now, kind: evArrival, call: child})
		}
	}
}

func requestID(id uint64) string {
	const digits = "0123456789abcdef"
	buf := make([]byte, 16)
	for i := 15; i >= 0; i-- {
		buf[i] = digits[id&0xf]
		id >>= 4
	}
	return string(buf)
}
