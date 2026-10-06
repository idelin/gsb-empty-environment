package main

import "path/filepath"

// FaultPoint 枚举每个可能崩溃的故障点。
type FaultPoint int

const (
	NoFault            FaultPoint = iota // 基线：无故障
	FaultAfterIngest                     // F1 摄入后、快照检查点前
	FaultAfterSnapshot                   // F2 快照后、结果生成前
	FaultAfterProduce                    // F3 结果生成并落盘后、发布前
	FaultAfterPublish                    // F4 接收方已应用、确认未处理（ack 丢失）
	FaultAfterAck                        // F5 确认后、检查点落盘前
	FaultDuringPersist                   // F6 检查点写入中途（临时文件与原子重命名之间）
)

var faultPointNames = map[FaultPoint]string{
	NoFault:            "基线(无故障)",
	FaultAfterIngest:   "F1 摄入后/检查点前",
	FaultAfterSnapshot: "F2 快照后/生成结果前",
	FaultAfterProduce:  "F3 结果落盘后/发布前",
	FaultAfterPublish:  "F4 发布后/确认前",
	FaultAfterAck:      "F5 确认后/落盘前",
	FaultDuringPersist: "F6 检查点写入中途",
}

var allFaultPoints = []FaultPoint{
	NoFault,
	FaultAfterIngest,
	FaultAfterSnapshot,
	FaultAfterProduce,
	FaultAfterPublish,
	FaultAfterAck,
	FaultDuringPersist,
}

var testEvents = []int64{5, 3, 8, 2, 7, 1, 4, 6, 9, 10, 11, 12}

const eventsPerCycle = 4

// Report 记录一次演练结束后的两项进度。
type Report struct {
	FP          FaultPoint
	ExpectedSum int64
	// 计算状态恢复
	FinalOffset int64
	FinalSum    int64
	ComputeOK   bool
	// 效果确认
	PendingLeft   int
	ReceiverTotal int64
	Deliveries    int64
	Duplicates    int64
	GapErrors     int64
	EffectOK      bool
}

func expectedSum() int64 {
	var s int64
	for _, v := range testEvents {
		s += v
	}
	return s
}

// runScenario 在第二个处理周期的指定故障点注入崩溃，
// 然后从检查点恢复并跑到静默，最后核验两项进度。
func runScenario(dir string, fp FaultPoint) Report {
	store := NewStore(filepath.Join(dir, "checkpoint.json"))
	agg := NewAggregator(store)
	recv := NewReceiver(&receiverStore{path: filepath.Join(dir, "receiver.json")})

	cycles := len(testEvents) / eventsPerCycle
	crashCycle := 1
	crashed := false

	crashHere := func(cycle int, at FaultPoint) bool {
		return !crashed && fp == at && cycle == crashCycle
	}

	for cycle := 0; cycle < cycles; cycle++ {
		// 1. 摄入（仅内存，未落盘）
		for i := 0; i < eventsPerCycle; i++ {
			agg.Ingest(testEvents[agg.Consumed()])
		}
		if crashHere(cycle, FaultAfterIngest) {
			crashed = true
			break
		}
		// 2. 快照检查点（计算状态推进）
		agg.Persist()
		if crashHere(cycle, FaultAfterSnapshot) {
			crashed = true
			break
		}
		// 3. 生成结果并随检查点落盘：ID 序号在发布前持久化
		if fp == FaultDuringPersist && cycle == crashCycle && !crashed {
			store.CrashAfterTempWrite = true
		}
		agg.Produce()
		if err := agg.Persist(); err == errSimulatedCrash {
			crashed = true // F6：临时文件已写、未重命名，旧检查点完好
			break
		}
		store.CrashAfterTempWrite = false
		if crashHere(cycle, FaultAfterProduce) {
			crashed = true
			break
		}
		// 4. 发布：接收方幂等应用
		for _, p := range agg.Pending() {
			recv.Apply(p)
		}
		if crashHere(cycle, FaultAfterPublish) {
			crashed = true
			break
		}
		// 5. 确认（内存）+ 落盘（效果确认推进）
		ids := make([]string, 0, len(agg.Pending()))
		for _, p := range agg.Pending() {
			ids = append(ids, p.ID)
		}
		for _, id := range ids {
			agg.Ack(id)
		}
		if crashHere(cycle, FaultAfterAck) {
			crashed = true
			break
		}
		agg.Persist()
	}

	if crashed {
		// 进程重启：丢弃全部内存状态，仅从检查点恢复
		store.CrashAfterTempWrite = false
		agg = NewAggregator(store)
	}
	drain(agg, recv)
	return verify(fp, agg, recv)
}

// drain 是恢复流程，明确分两步推进：
//  1. 计算状态恢复：从检查点位移重放剩余输入并逐条落盘；
//  2. 效果确认：为未覆盖的增量补发结果，重发全部待发结果直到确认清空。
func drain(agg *Aggregator, recv *Receiver) {
	for agg.Consumed() < int64(len(testEvents)) {
		agg.Ingest(testEvents[agg.Consumed()])
		agg.Persist()
	}
	if agg.HasUnemittedDelta() {
		agg.Produce()
		agg.Persist()
	}
	for len(agg.Pending()) > 0 {
		res := agg.Pending()[0]
		recv.Apply(res) // 重发同一 ID，接收方幂等去重
		agg.Ack(res.ID)
		agg.Persist()
	}
}

func verify(fp FaultPoint, agg *Aggregator, recv *Receiver) Report {
	expected := expectedSum()
	cp := agg.State()
	computeOK := cp.Offset == int64(len(testEvents)) && cp.Sum == expected
	effectOK := len(cp.Pending) == 0 && recv.Total == expected && recv.GapErrors == 0
	return Report{
		FP:            fp,
		ExpectedSum:   expected,
		FinalOffset:   cp.Offset,
		FinalSum:      cp.Sum,
		ComputeOK:     computeOK,
		PendingLeft:   len(cp.Pending),
		ReceiverTotal: recv.Total,
		Deliveries:    recv.Deliveries,
		Duplicates:    recv.Duplicates,
		GapErrors:     recv.GapErrors,
		EffectOK:      effectOK,
	}
}
