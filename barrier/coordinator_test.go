package barrier

import (
	"math/rand"
	"reflect"
	"testing"
)

// 乱序应用 + 缺口补齐：高号但中间有缺口不能当进度。
func TestOutOfOrderGapFill(t *testing.T) {
	clock := NewTestClock()
	c := NewCoordinator(clock)

	id, out := c.Submit(5, []ShardID{"s1", "s2"}, 100)
	if out != nil {
		t.Fatalf("request should be pending, got %+v", out)
	}

	// s1 顺序应用到 5。
	if got := c.HandleReport(Report{Shard: "s1", Epoch: 1, Applied: []uint64{1, 2, 3, 4, 5}}); len(got) != 0 {
		t.Fatalf("s2 not ready, unexpected grant: %+v", got)
	}
	// s2 乱序：1,2,4,5 —— 4、5 高于连续点但 3 缺失，不能算进度。
	if got := c.HandleReport(Report{Shard: "s2", Epoch: 1, Applied: []uint64{5, 1, 4, 2}}); len(got) != 0 {
		t.Fatalf("gap at 3 must block progress, unexpected grant: %+v", got)
	}
	st := c.Stats()
	if st.PendingHoles != 2 {
		t.Fatalf("want 2 pending holes (4,5), got %d", st.PendingHoles)
	}
	// 重复上报同样的事件是幂等的。
	if got := c.HandleReport(Report{Shard: "s2", Epoch: 1, Applied: []uint64{4, 5, 2}}); len(got) != 0 {
		t.Fatalf("duplicate report must be a no-op, got %+v", got)
	}
	if st := c.Stats(); st.PendingHoles != 2 {
		t.Fatalf("duplicates changed state, holes=%d", st.PendingHoles)
	}
	// 补齐缺口 3：连续进度一次性跳到 5，发放视图。
	got := c.HandleReport(Report{Shard: "s2", Epoch: 1, Applied: []uint64{3}})
	if len(got) != 1 || got[0].Kind != Granted || got[0].Req != id {
		t.Fatalf("want grant after gap fill, got %+v", got)
	}
	if got[0].View.Target != 5 {
		t.Fatalf("view must be pinned at target 5, got %d", got[0].View.Target)
	}
	if st := c.Stats(); st.PendingRequests != 0 || st.WaitIndexEntries != 0 || st.PendingHoles != 0 {
		t.Fatalf("resources leaked after grant: %+v", st)
	}
}

// 截止时刻与进度同时到达：先应用再判超时。
func TestProgressBeforeTimeoutAtSameTick(t *testing.T) {
	clock := NewTestClock()
	c := NewCoordinator(clock)

	okID, _ := c.Submit(3, []ShardID{"s1"}, 50)
	badID, _ := c.Submit(9, []ShardID{"s1"}, 50)

	clock.Advance(49)
	// tick 50：进度先到达。
	got := c.HandleReport(Report{Shard: "s1", Epoch: 1, Applied: []uint64{1, 2, 3}})
	if len(got) != 1 || got[0].Req != okID || got[0].Kind != Granted {
		t.Fatalf("progress at deadline tick must grant first, got %+v", got)
	}
	// 然后推进时钟到 50：只有未满足的 badID 超时。
	timeouts := c.Advance(50)
	if len(timeouts) != 1 || timeouts[0].Req != badID || timeouts[0].Kind != TimedOut {
		t.Fatalf("want exactly one timeout for badID, got %+v", timeouts)
	}
	if len(timeouts[0].Lagging) != 1 || timeouts[0].Lagging[0].Missing != 6 {
		t.Fatalf("want gap 9-3=6, got %+v", timeouts[0].Lagging)
	}
}

// 超时返回每个落后分片及缺口，不返回伪成功部分结果。
func TestTimeoutReportsLagging(t *testing.T) {
	clock := NewTestClock()
	c := NewCoordinator(clock)

	id, _ := c.Submit(10, []ShardID{"a", "b", "c"}, 30)
	c.HandleReport(Report{Shard: "a", Epoch: 1, Applied: []uint64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}})
	c.HandleReport(Report{Shard: "b", Epoch: 1, Applied: []uint64{1, 2, 3, 4, 5, 6, 7}})
	// c 从未上报。

	outs := c.Advance(30)
	if len(outs) != 1 || outs[0].Req != id || outs[0].Kind != TimedOut {
		t.Fatalf("want single timeout, got %+v", outs)
	}
	if outs[0].View != nil {
		t.Fatalf("timeout must not carry a partial view: %+v", outs[0].View)
	}
	want := []ShardGap{
		{Shard: "b", Contig: 7, Missing: 3},
		{Shard: "c", Unknown: true},
	}
	if !reflect.DeepEqual(outs[0].Lagging, want) {
		t.Fatalf("lagging mismatch:\n got %+v\nwant %+v", outs[0].Lagging, want)
	}
	if st := c.Stats(); st.PendingRequests != 0 || st.WaitIndexEntries != 0 {
		t.Fatalf("timed-out request must leave the wait index: %+v", st)
	}
}

// 多个客户端共享等待索引，取消只移除本请求。
func TestCancelRemovesOnlyOwnRequest(t *testing.T) {
	clock := NewTestClock()
	c := NewCoordinator(clock)

	id1, _ := c.Submit(2, []ShardID{"s1"}, 100)
	id2, _ := c.Submit(2, []ShardID{"s1"}, 100)
	id3, _ := c.Submit(2, []ShardID{"s1", "s2"}, 100)

	if st := c.Stats(); st.PendingRequests != 3 || st.WaitIndexEntries != 4 {
		t.Fatalf("want 3 pending / 4 index entries, got %+v", st)
	}
	if !c.Cancel(id2) {
		t.Fatal("cancel of live request should succeed")
	}
	if c.Cancel(id2) {
		t.Fatal("double cancel must be a no-op")
	}
	if st := c.Stats(); st.PendingRequests != 2 || st.WaitIndexEntries != 3 {
		t.Fatalf("cancel must remove only its own entries: %+v", st)
	}
	// 其余请求不受影响，正常发放。
	got := c.HandleReport(Report{Shard: "s1", Epoch: 1, Applied: []uint64{1, 2}})
	if len(got) != 1 || got[0].Req != id1 {
		t.Fatalf("id1 should grant, id3 still waits on s2: %+v", got)
	}
	got = c.HandleReport(Report{Shard: "s2", Epoch: 1, Applied: []uint64{1, 2}})
	if len(got) != 1 || got[0].Req != id3 {
		t.Fatalf("id3 should grant now: %+v", got)
	}
	if st := c.Stats(); st.PendingRequests != 0 || st.WaitIndexEntries != 0 {
		t.Fatalf("all requests finished, index must be empty: %+v", st)
	}
}

// 节点重启带 epoch：旧 epoch 报告不能推进新实例；恢复证明到达前进度未知。
func TestRestartEpochAndRecoveryProof(t *testing.T) {
	clock := NewTestClock()
	c := NewCoordinator(clock)

	c.HandleReport(Report{Shard: "s1", Epoch: 1, Applied: []uint64{1, 2, 3, 4, 5}})
	c.Restart("s1", 2)

	id, out := c.Submit(3, []ShardID{"s1"}, 100)
	if out != nil {
		t.Fatalf("after restart progress is unknown, must not grant: %+v", out)
	}
	// 旧 epoch 的报告被忽略。
	if got := c.HandleReport(Report{Shard: "s1", Epoch: 1, Applied: []uint64{1, 2, 3, 4, 5, 6, 7}}); len(got) != 0 {
		t.Fatalf("stale epoch report must be ignored, got %+v", got)
	}
	// 新 epoch 的普通报告：先缓冲，进度仍未知。
	if got := c.HandleReport(Report{Shard: "s1", Epoch: 2, Applied: []uint64{3}}); len(got) != 0 {
		t.Fatalf("no grant before recovery proof, got %+v", got)
	}
	if st := c.Stats(); st.PreProofBuffered != 1 {
		t.Fatalf("want 1 pre-proof buffered commit, got %+v", st)
	}
	// 恢复证明到达：基线 2，重放缓冲的 3，连续进度到 3，发放。
	got := c.HandleReport(Report{Shard: "s1", Epoch: 2, Proof: true, ProofContig: 2})
	if len(got) != 1 || got[0].Req != id || got[0].Kind != Granted {
		t.Fatalf("proof should unblock and grant, got %+v", got)
	}
	// 超时视角验证：另一个目标更高的请求在证明前超时，应报告 Unknown。
	id2, _ := c.Submit(9, []ShardID{"s1"}, 60)
	outs := c.Advance(60)
	if len(outs) != 1 || outs[0].Req != id2 {
		t.Fatalf("want timeout for id2, got %+v", outs)
	}
	if len(outs[0].Lagging) != 1 || outs[0].Lagging[0].Missing != 6 {
		t.Fatalf("after proof contig=3, gap to 9 is 6: %+v", outs[0].Lagging)
	}
}

// 分片下线不会无限占用已结束请求；进行中的请求在超时里看到 Offline。
func TestOfflineDoesNotHoldFinishedRequests(t *testing.T) {
	clock := NewTestClock()
	c := NewCoordinator(clock)

	done, _ := c.Submit(2, []ShardID{"s1"}, 100)
	got := c.HandleReport(Report{Shard: "s1", Epoch: 1, Applied: []uint64{1, 2}})
	if len(got) != 1 || got[0].Req != done {
		t.Fatalf("want grant, got %+v", got)
	}
	// 已结束的请求已离开等待索引；下线动作没有可占用的条目。
	c.SetOffline("s1", false)
	if st := c.Stats(); st.PendingRequests != 0 || st.WaitIndexEntries != 0 {
		t.Fatalf("finished request must not be held by offline shard: %+v", st)
	}
	// 进行中的请求在超时结果里把下线分片标为 Offline。
	pending, _ := c.Submit(5, []ShardID{"s1"}, 40)
	outs := c.Advance(40)
	if len(outs) != 1 || outs[0].Req != pending {
		t.Fatalf("want timeout, got %+v", outs)
	}
	if len(outs[0].Lagging) != 1 || !outs[0].Lagging[0].Offline {
		t.Fatalf("offline shard must be flagged in lagging: %+v", outs[0].Lagging)
	}
}

// 与全扫描参考实现对拍：乱序应用、缺口、重启、取消、截止混合回放。
func TestReferenceCrossCheck(t *testing.T) {
	clockA, clockB := NewTestClock(), NewTestClock()
	coord := NewCoordinator(clockA)
	ref := NewRefCoordinator(clockB)

	shards := []ShardID{"s1", "s2", "s3", "s4"}
	rng := rand.New(rand.NewSource(20261005))
	epoch := map[ShardID]Epoch{"s1": 1, "s2": 1, "s3": 1, "s4": 1}
	frontier := map[ShardID]uint64{}
	var pending []ReqID

	assertEqual := func(step string, got, want []Outcome) {
		t.Helper()
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: coordinator/reference mismatch\n coord: %+v\n  ref: %+v", step, got, want)
		}
	}
	dropPending := func(id ReqID) {
		for i, p := range pending {
			if p == id {
				pending = append(pending[:i], pending[i+1:]...)
				return
			}
		}
	}
	absorb := func(outs []Outcome) {
		for _, o := range outs {
			dropPending(o.Req)
		}
	}
	doReport := func(step string, rep Report) {
		got, want := coord.HandleReport(rep), ref.HandleReport(rep)
		assertEqual(step, got, want)
		absorb(got)
	}

	for tick := Tick(1); tick <= 80; tick++ {
		// 1) 事件：上报 / 重启 / 上下线。
		for _, s := range shards {
			switch rng.Intn(10) {
			case 0, 1, 2, 3: // 上报：围绕前沿随机取，制造乱序与缺口
				base := frontier[s]
				var applied []uint64
				for k := 0; k < 1+rng.Intn(3); k++ {
					n := base + 1 + uint64(rng.Intn(4))
					applied = append(applied, n)
					if n > frontier[s] {
						frontier[s] = n
					}
				}
				rep := Report{Shard: s, Epoch: epoch[s], Applied: applied}
				// 偶发重复上报（幂等性）。
				if rng.Intn(4) == 0 {
					doReport("dup-report", rep)
				}
				doReport("report", rep)
			case 4: // 重启：新 epoch，进度未知
				if rng.Intn(3) == 0 {
					epoch[s]++
					coord.Restart(s, epoch[s])
					ref.Restart(s, epoch[s])
					// 恢复证明随后到达，基线为重启前连续进度的一部分。
					proof := Report{Shard: s, Epoch: epoch[s], Proof: true, ProofContig: frontier[s] / 2}
					doReport("proof", proof)
				}
			case 5: // 偶发下线/上线
				if rng.Intn(5) == 0 {
					online := rng.Intn(2) == 0
					coord.SetOffline(s, online)
					ref.SetOffline(s, online)
				}
			}
		}
		// 2) 偶发新请求。
		if rng.Intn(3) == 0 {
			n := 1 + rng.Intn(len(shards))
			perm := rng.Perm(len(shards))[:n]
			set := make([]ShardID, n)
			for i, p := range perm {
				set[i] = shards[p]
			}
			target := 1 + uint64(rng.Intn(12))
			deadline := tick + Tick(rng.Intn(15))
			idA, outA := coord.Submit(target, set, deadline)
			idB, outB := ref.Submit(target, set, deadline)
			if idA != idB {
				t.Fatalf("request id divergence: %d vs %d", idA, idB)
			}
			if (outA == nil) != (outB == nil) || (outA != nil && !reflect.DeepEqual(*outA, *outB)) {
				t.Fatalf("submit outcome mismatch:\n coord: %+v\n  ref: %+v", outA, outB)
			}
			if outA == nil {
				pending = append(pending, idA)
			}
		}
		// 3) 偶发取消一个等待中的请求。
		if len(pending) > 0 && rng.Intn(4) == 0 {
			victim := pending[rng.Intn(len(pending))]
			if coord.Cancel(victim) != ref.Cancel(victim) {
				t.Fatalf("cancel divergence for %d", victim)
			}
			dropPending(victim)
		}
		// 4) 推进时钟：先应用再判超时。
		doReport("noise", Report{Shard: shards[rng.Intn(len(shards))], Epoch: 1}) // stale/no-op noise
		got := coord.Advance(tick)
		want := ref.Advance(tick)
		assertEqual("advance", got, want)
		absorb(got)
	}
	t.Logf("final coordinator stats: %+v", coord.Stats())
}
