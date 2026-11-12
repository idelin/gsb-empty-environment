package netsec_test

import (
	"errors"
	"testing"
	"time"

	netsec "example.org/netsec/q0390"
)

// 随机分配的 QPN 必须落在 24 位有效范围且保留号 0 永不分配。
func TestAllocateRandomQPN(t *testing.T) {
	m, _ := newTestManager(t, netsec.Config{})
	seen := make(map[uint32]struct{})
	for range 1000 {
		qpn, err := m.AllocateQP()
		if err != nil {
			t.Fatalf("AllocateQP: %v", err)
		}
		if qpn < netsec.MinQPN || qpn > netsec.MaxQPN {
			t.Fatalf("QPN %d 超出 24 位有效范围", qpn)
		}
		if _, dup := seen[qpn]; dup {
			t.Fatalf("存活 QPN 不应重复分配: %d", qpn)
		}
		seen[qpn] = struct{}{}
	}

	// 随机源恒为 0 时，保留号 0 必须被跳过，分配出 1。
	zeroMgr := netsec.NewManager(netsec.Config{Random: fixedReader(0)})
	qpn, err := zeroMgr.AllocateQP()
	if err != nil {
		t.Fatalf("AllocateQP: %v", err)
	}
	if qpn != 1 {
		t.Fatalf("保留 QPN 0 应被跳过并分配 1, 得到 %d", qpn)
	}
}

// 销毁后 QPN 进入隔离期：期内不可复用，期满后可重新分配。
func TestQPNQuarantineAndReuse(t *testing.T) {
	const quarantine = time.Minute
	m, clock := newTestManager(t, netsec.Config{
		Quarantine: quarantine,
		Random:     fixedReader(0x42), // 三次随机读出 0x424242，保证 QPN 确定
	})

	qpn, err := m.AllocateQP()
	if err != nil {
		t.Fatalf("AllocateQP: %v", err)
	}
	wantQPN := uint32(0x424242)
	if qpn != wantQPN {
		t.Fatalf("确定性随机源下应分配 %#x, 得到 %#x", wantQPN, qpn)
	}

	peer := gid(0x05)
	mustConnect(t, m, qpn, peer, 10)
	if err := m.DestroyQP(qpn); err != nil {
		t.Fatalf("DestroyQP: %v", err)
	}

	until, held := m.QuarantinedUntil(qpn)
	if !held {
		t.Fatal("销毁后 QPN 应出现在隔离表中")
	}
	if !until.Equal(clock.t.Add(quarantine)) {
		t.Fatalf("隔离解除时刻=%v, 期望 %v", until, clock.t.Add(quarantine))
	}

	// 隔离期内分配器必须跳过该 QPN（随机源仍指向它，靠线性扫描兜底）。
	other, err := m.AllocateQP()
	if err != nil {
		t.Fatalf("隔离期内再次分配失败: %v", err)
	}
	if other == qpn {
		t.Fatalf("隔离期内 QPN %#x 不得复用", qpn)
	}
	if _, err := m.Inspect(qpn); !errors.Is(err, netsec.ErrQPNNotFound) {
		t.Fatalf("销毁后 Inspect 应返回 ErrQPNNotFound, 得到 %v", err)
	}

	// 隔离期未到，推进 59 秒仍不可复用。
	clock.Advance(quarantine - time.Second)
	if _, held := m.QuarantinedUntil(qpn); !held {
		t.Fatal("隔离期未满前 QPN 应继续被隔离")
	}

	// 隔离期满后，同一 QPN 可被重新分配并重新建立连接。
	clock.Advance(time.Second)
	reused, err := m.AllocateQP()
	if err != nil {
		t.Fatalf("隔离期满后分配失败: %v", err)
	}
	if reused != qpn {
		t.Fatalf("隔离期满后应允许复用 QPN %#x, 实际 %#x", qpn, reused)
	}
	if _, held := m.QuarantinedUntil(qpn); held {
		t.Fatal("复用后 QPN 不应再留在隔离表")
	}
	if err := m.Connect(reused, peer, 99); err != nil {
		t.Fatalf("复用 QPN 重新 Connect: %v", err)
	}
}

// 随机尝试全部撞号时，线性扫描兜底仍能找到空闲 QPN，并跳过存活与隔离号。
func TestAllocateLinearFallback(t *testing.T) {
	// scriptedReader：每次 3 字节读出一个完整 QPN。第 0 次及之后的“候选”
	// 读都返回 1（制造撞号），第 33 次（线性扫描起点读）返回 0，使扫描
	// 从保留号开始、确定性地回绕到最小空闲号。
	scriptedReader := &scriptedQPNReader{
		candidate: 1,
		startRead: 33,
		startQPN:  0,
	}
	m, clock := newTestManager(t, netsec.Config{
		Quarantine: time.Minute,
		Random:     scriptedReader,
	})

	first, err := m.AllocateQP()
	if err != nil {
		t.Fatalf("AllocateQP: %v", err)
	}
	if first != 1 {
		t.Fatalf("期望首次分配 QPN 1, 得到 %d", first)
	}

	// 再次分配：32 次随机尝试全部撞到存活的 1；扫描从 100 回绕，首个空闲为 2。
	second, err := m.AllocateQP()
	if err != nil {
		t.Fatalf("兜底扫描分配失败: %v", err)
	}
	if second != 2 {
		t.Fatalf("期望兜底扫描分配 QPN 2, 得到 %d", second)
	}

	// 销毁 QPN 2 后，在隔离期内再次分配应跳过 1（存活）与 2（隔离），得到 3。
	if err := m.DestroyQP(2); err != nil {
		t.Fatalf("DestroyQP: %v", err)
	}

	// 隔离期内：扫描仍从 100 回绕，跳过存活 1、隔离 2，首个空闲为 3。
	third, err := m.AllocateQP()
	if err != nil {
		t.Fatalf("隔离跳过分配失败: %v", err)
	}
	if third != 3 {
		t.Fatalf("期望跳过存活/隔离号后分配 QPN 3, 得到 %d", third)
	}

	// 隔离期满后 QPN 2 重新可用。
	clock.Advance(time.Minute + time.Second)
	fourth, err := m.AllocateQP()
	if err != nil {
		t.Fatalf("隔离期满分配失败: %v", err)
	}
	if fourth != 2 {
		t.Fatalf("期望复用刚解除隔离的 QPN 2, 得到 %d", fourth)
	}
	if _, held := m.QuarantinedUntil(2); held {
		t.Fatal("隔离期满后 QPN 2 应已离开隔离表")
	}

	// 销毁不存在的 QPN 应报错。
	if err := m.DestroyQP(0x765432); !errors.Is(err, netsec.ErrQPNNotFound) {
		t.Fatalf("销毁未知 QPN 应返回 ErrQPNNotFound, 得到 %v", err)
	}
}

// 并发分配/准入/销毁不应触发数据竞争或返回重复的存活 QPN。
func TestManagerConcurrent(t *testing.T) {
	m, _ := newTestManager(t, netsec.Config{ErrorThreshold: 1 << 30})
	peer := gid(0x0c)

	const workers = 8
	const rounds = 200
	done := make(chan error, workers)

	for range workers {
		go func() {
			for range rounds {
				qpn, err := m.AllocateQP()
				if err != nil {
					done <- err
					return
				}
				if err := m.Connect(qpn, peer, 0); err != nil {
					done <- err
					return
				}
				m.Admit(netsec.InboundPacket{SourceGID: peer, DestQPN: qpn, PSN: 0})
				if _, err := m.Inspect(qpn); err != nil {
					done <- err
					return
				}
				if err := m.DestroyQP(qpn); err != nil {
					done <- err
					return
				}
			}
			done <- nil
		}()
	}

	for range workers {
		if err := <-done; err != nil {
			t.Fatalf("并发访问出错: %v", err)
		}
	}
}
