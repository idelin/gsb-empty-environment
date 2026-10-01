# pktscale — 有理倍率记录流缩放与离散事件链路仿真

纯 Go 标准库实现。将记录流按**有理数时间倍率**与**字节倍率**变换，
在整数 tick 的离散事件模型中运行所有包、队列与链路，不产生真实网络流量。

## 设计要点

- **精确有理累积**：时间轴映射 `out(t) = baseOut + (t-baseIn)*rate` 以
  `math/big.Rat` 精确维护；每个包对**精确累积位置**取整（而非对增量取整），
  舍入残差天然累计补偿，单包误差 ≤ 1/2 tick，长序列总时长误差 ≤ 1 tick，
  不会长期漂移，小间隔事件沿缩放后时间轴铺开而不会全挤在同一 tick。
- **流内因果**：每流输出 tick 单调不减；超 MTU 报文分片后，分片间隔
  `fragGap` 与"下一包不得早于上一包末分片"共同约束输出时刻。
- **跨流合并**：`Merge` 按 `(Tick, Seq, FragID)` 排序，同 tick 回退到声明
  顺序，余数补偿不会使后声明的记录越过先声明的记录。
- **可恢复状态**：`Save`/`Load` 将有理状态以精确 `num/den` 字符串存入
  JSON；输入分块、暂停（存档/恢复）与一次性处理结果逐片一致。
- **独立参考检查**：`ExactTimeline`/`CheckDrift` 用独立的 big.Rat 实现
  复核漂移界与倍率变化点；`CheckSpacing` 报告声明最小时距违例与
  "链路传输时间 > 包间隔"的不可满足时距。
- **倍率约束**：倍率 1 保持原轨迹；0（及负）倍率直接拒绝。

## 文件

| 文件 | 内容 |
|---|---|
| `scaler.go` | 缩放器：有理时间/字节倍率、残差补偿、分片、因果钳制 |
| `merge.go` | 跨流有序合并 |
| `sim.go` | 离散事件仿真：链路带宽/时延、FIFO 串行化 |
| `checkpoint.go` | JSON 存档/恢复（有理数无损） |
| `reference.go` | 独立精确时间轴参考与漂移/时距检查 |
| `cmd/pktscale-demo` | 运行样例 |

## 运行

```sh
go test ./...          # 全部测试
go run ./cmd/pktscale-demo
```

## 测试结果（go test -v）

```
--- PASS: TestCheckpointResumeEquivalence   分块+暂停+恢复 == 一次性处理
--- PASS: TestCheckpointIsRationalAndExact  存档为精确有理数，恢复后不发散
--- PASS: TestCheckSpacingDetectsUnsatisfiable  检出不可满足时距
--- PASS: TestCheckDriftDetectsViolation    检出漂移越界
--- PASS: TestRateOneIdentity               倍率 1 保持原轨迹
--- PASS: TestZeroRateRejected              0/负倍率拒绝
--- PASS: TestDriftBoundManySmallIntervals  2 万个小间隔：误差≤1 tick 且不全挤同 tick
--- PASS: TestFragmentationAndCausality     分片与流内因果联合约束
--- PASS: TestByteResidualCompensation      字节残差累计补偿无漂移
--- PASS: TestRateChangePoint               倍率变化点连续
--- PASS: TestMergeKeepsDeclaredOrder       合并不违反声明顺序
--- PASS: TestSimLinkSerialization          链路串行化与时延
--- PASS: TestEndToEndScaledStreamsOverLink 缩放→合并→仿真端到端
PASS
ok  	pktscale
```

## 运行样例（节选）

双流、时间倍率 2/3、字节倍率 5/4、MTU 600、链路 150 B/tick：

```
tick   0  stream 0  seq  0  frag 1/2  size  600   <- 800*5/4=1000 > MTU，分片
tick   1  stream 0  seq  0  frag 2/2  size  400
...
drift bound check (<=1 tick): 0 violation(s)
last packet: emitted tick 10, exact position 10
spacing check: 10 violation(s)
  ! stream 0 seq 0->2: packet needs 7 ticks on link but gap is 2 (spacing unsatisfiable)
```

链路 150 B/tick 下 1000 字节需 7 tick，而缩放后包间隔仅 2 tick ——
参考检查器如实报告该时距限制在此链路上不可满足。
