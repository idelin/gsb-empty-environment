# recvq — 有界接收队列

纯 Go 标准库实现的有界接收队列：按数据优先级与租户公平调度，支持明确的
丢弃策略，输出接收 / 处理 / 丢弃守恒统计。遥测事件在本地合成为 JSON Lines，
可由夹具（fixture）回放校验；不接监控平台，不含展示看板。

## 设计

- **有界队列**：容量固定，满时按 `-policy` 处理：
  - `incoming`（默认）：拒绝新记录，计 `dropped{queue_full}`；
  - `oldest`：驱逐队首记录（普通优先 lane 优先），计 `dropped{evicted}`。
- **优先级调度**：高 / 普通两条 lane，加权 3:1。两条 lane 都饱和时普通流
  保证至少 25% 的处理份额——高优先持续流不能完全饿死普通流。
- **租户公平**：同一 lane 内按租户轮转（round-robin），单租户不能垄断。
- **守恒统计**：`arrived == processed + dropped + in_queue` 恒成立；
  被拒绝的记录只计 `dropped`，绝不计 `processed`。
- **关闭模式**：`drain` 排空队列后退出；`stopnow` 立即丢弃队列内剩余记录，
  计 `dropped{shutdown}`。两种模式都保持守恒。
- **遥测**：每次入队 / 处理 / 丢弃在本地合成一条事件（JSON Lines），
  `Replay` 可从夹具文件折叠回统计并校验守恒。夹具：
  `testdata/telemetry.jsonl`（由 demo 生成）。

## 运行样例

```console
$ go run ./cmd/demo -records 800 -capacity 32 -arrival-us 10 -work-us 8000 -fixture testdata/telemetry.jsonl
== receive queue demo ==
capacity=32 workers=4 policy=incoming mode=drain
offered=800 accepted=76 rejected=724
stats: arrived=800 enqueued=76 processed=76 dropped=724 by_reason=map[queue_full:724]
in_queue=0 conserved=true (arrived == processed + dropped + in_queue)
processed_by_tenant: map[tenant-d:19 tenant-a:17 tenant-b:21 tenant-c:19]
processed_by_priority: map[high:24 normal:52]
wrote 876 telemetry events to testdata/telemetry.jsonl
```

驱逐策略（`oldest`）下高优先记录挤占普通记录， noisy 租户的高优先流占优，
但普通流仍有份额：

```console
$ go run ./cmd/demo -records 800 -capacity 32 -arrival-us 10 -work-us 8000 -policy oldest
stats: arrived=800 enqueued=800 processed=69 dropped=731 by_reason=map[evicted:731]
processed_by_tenant: map[tenant-a:54 tenant-b:7 tenant-c:5 tenant-d:3]
processed_by_priority: map[high:59 normal:10]
```

立即停止（`stopnow`）：队列内剩余记录被丢弃并计 `dropped{shutdown}`，
与 `drain` 的"全部处理完"语义明确区分：

```console
$ go run ./cmd/demo -records 800 -capacity 32 -arrival-us 10 -work-us 8000 -mode stopnow
stats: arrived=800 enqueued=69 processed=37 dropped=763 by_reason=map[queue_full:731 shutdown:32]
in_queue=0 conserved=true (arrived == processed + dropped + in_queue)
```

## 测试结果

```console
$ go test ./... -v
=== RUN   TestConservationUnderLoad
--- PASS: TestConservationUnderLoad (0.00s)
=== RUN   TestRejectedRecordsAreDroppedNotProcessed
--- PASS: TestRejectedRecordsAreDroppedNotProcessed (0.13s)
=== RUN   TestHighPriorityStreamCannotStarveNormal
--- PASS: TestHighPriorityStreamCannotStarveNormal (0.00s)
=== RUN   TestTenantFairnessRoundRobin
--- PASS: TestTenantFairnessRoundRobin (0.00s)
=== RUN   TestDropOldestEvictsQueuedRecords
--- PASS: TestDropOldestEvictsQueuedRecords (0.00s)
=== RUN   TestDrainProcessesEverything
--- PASS: TestDrainProcessesEverything (0.00s)
=== RUN   TestStopNowDiscardsQueuedRecords
--- PASS: TestStopNowDiscardsQueuedRecords (0.05s)
=== RUN   TestEnqueueAfterCloseIsRejected
--- PASS: TestEnqueueAfterCloseIsRejected (0.00s)
=== RUN   TestSynthesizeDeterministic
--- PASS: TestSynthesizeDeterministic (0.00s)
=== RUN   TestReplayMatchesLiveRun
--- PASS: TestReplayMatchesLiveRun (0.00s)
=== RUN   TestReplayFixture
--- PASS: TestReplayFixture (0.01s)
    telemetry_test.go:70: fixture replayed: arrived=800 enqueued=76 processed=76 dropped=724 by_reason=map[queue_full:724]
=== RUN   TestReplayDetectsConservationViolation
--- PASS: TestReplayDetectsConservationViolation (0.00s)
=== RUN   TestReplayRejectsUnknownReason
--- PASS: TestReplayRejectsUnknownReason (0.00s)
PASS
ok  	recvq	0.635s
```

## 文件

- `receiver.go` — 有界队列、加权优先级调度、租户轮转、丢弃策略、关闭模式、守恒统计
- `telemetry.go` — 遥测事件合成、JSON Lines 写入、夹具回放与守恒校验、负载合成
- `cmd/demo/main.go` — 可运行样例（参数化容量 / 策略 / 关闭模式 / 夹具输出）
- `receiver_test.go` / `telemetry_test.go` — `go test` 测试
- `testdata/telemetry.jsonl` — 遥测回放夹具
