package main

import (
	"io/ioutil"
	"os"
	"testing"
)

func runInTempDir(t *testing.T, fp FaultPoint) Report {
	dir, err := ioutil.TempDir("", "agg-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	return runScenario(dir, fp)
}

// 每个故障点：计算状态恢复与效果确认两项进度都必须达标。
func TestEveryFaultPoint(t *testing.T) {
	for _, fp := range allFaultPoints {
		r := runInTempDir(t, fp)
		if !r.ComputeOK {
			t.Errorf("%s: 计算状态恢复失败 offset=%d sum=%d (期望 %d)",
				faultPointNames[fp], r.FinalOffset, r.FinalSum, r.ExpectedSum)
		}
		if !r.EffectOK {
			t.Errorf("%s: 效果确认失败 pending=%d total=%d gaps=%d (期望 %d)",
				faultPointNames[fp], r.PendingLeft, r.ReceiverTotal, r.GapErrors, r.ExpectedSum)
		}
	}
}

// 恢复重发必须使用同一结果 ID：接收方看到重复投递但不得重复计数。
// F4/F5 中接收方已应用过结果，恢复重发必然表现为重复投递（被幂等去重）。
func TestResendReusesSameResultID(t *testing.T) {
	for _, fp := range []FaultPoint{FaultAfterPublish, FaultAfterAck} {
		r := runInTempDir(t, fp)
		if r.Duplicates == 0 {
			t.Errorf("%s: 期望发生重发（重复投递>0），实际为 0", faultPointNames[fp])
		}
		if r.ReceiverTotal != r.ExpectedSum {
			t.Errorf("%s: 重复投递被重复计数 total=%d 期望 %d",
				faultPointNames[fp], r.ReceiverTotal, r.ExpectedSum)
		}
	}
}

// 结果 ID 来自持久化序号：崩溃恢复后不会生成替代 ID。
func TestNoReplacementIDAfterRecovery(t *testing.T) {
	dir, err := ioutil.TempDir("", "agg-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	store := NewStore(dir + "/checkpoint.json")
	agg := NewAggregator(store)
	recv := NewReceiver(&receiverStore{path: dir + "/receiver.json"})

	for _, v := range testEvents[:4] {
		agg.Ingest(v)
	}
	agg.Persist()
	r1 := agg.Produce()
	agg.Persist()
	recv.Apply(r1) // 已发布、已应用，但 ack 前崩溃

	agg2 := NewAggregator(store) // 恢复
	pending := agg2.Pending()
	if len(pending) != 1 || pending[0].ID != r1.ID {
		t.Fatalf("恢复后应重发同一 ID %s，实际 %+v", r1.ID, pending)
	}
	recv.Apply(pending[0]) // 重发：幂等去重
	if recv.Total != r1.Delta {
		t.Fatalf("重发导致重复计数 total=%d 期望 %d", recv.Total, r1.Delta)
	}
	if agg2.State().NextResultSeq != r1.Seq+1 {
		t.Fatalf("恢复后序号被重置，可能生成替代 ID: seq=%d", agg2.State().NextResultSeq)
	}
}

// 检查点写入中途崩溃：原子重命名保证旧检查点完好，不留半截文件。
func TestTornCheckpointWrite(t *testing.T) {
	dir, err := ioutil.TempDir("", "agg-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	store := NewStore(dir + "/checkpoint.json")
	agg := NewAggregator(store)
	agg.Ingest(10)
	if err := agg.Persist(); err != nil {
		t.Fatal(err)
	}
	agg.Ingest(20)
	store.CrashAfterTempWrite = true
	if err := agg.Persist(); err != errSimulatedCrash {
		t.Fatalf("期望模拟崩溃错误，实际 %v", err)
	}
	cp, ok, err := store.Load()
	if err != nil || !ok {
		t.Fatalf("崩溃后检查点不可读: ok=%v err=%v", ok, err)
	}
	if cp.Sum != 10 {
		t.Fatalf("崩溃后应保留旧检查点 sum=10，实际 %d", cp.Sum)
	}
}
