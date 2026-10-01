# tsxform — 有理倍率记录流变换（离散事件模型）

纯 Go 标准库实现。把记录流按**有理时间倍率**和**有字节倍率**缩放，在整数 tick 的
离散事件模型中运行所有包、队列与链路，不发真实网络流量。

## 设计要点

- **舍入残差累计补偿**：每条流用 `math/big.Rat` 保存精确有理位置，输出 tick 取
  精确位置的 floor。残差始终留在累加器里，长序列漂移永久受界于 1 tick 以内，
  不会累积漂移；小间隔不会被各自舍入成 0 后全挤在同一个 tick（绝对位置 floor
  会自然摊开）。字节方向同理：`floor(累计字节 × 倍率)` 的增量为缩放尺寸，总字节
  误差永久小于 1 字节（缩放后为 0 的包钳到 1 字节并计入 `Clamped`）。
- **MTU 分片与流内因果**：缩放后超 MTU 的包按 MTU 分片，同包分片按序在链路上
  串行发送（占用 `ceil(尺寸/带宽)` tick），同流后续包必须等前包分片发完。
- **跨流合并不违反声明顺序**：到达堆按 `(理想tick, 声明序号, 分片号)` 排序，
  理想 tick 相同的包严格按输入声明顺序出队；余数补偿只影响本流 tick，不改变
  合并次序。
- **可恢复状态**：`Pipeline.Save()/LoadPipeline()` 以 JSON 保存全部状态，包括
  有理累加器（`big.Rat` 字符串）、到达堆、链路在途传输。输入分片喂入、中途
  暂停、断点续跑与一次处理结果逐字节一致（有测试保证）。
- **独立精确时间轴参考检查**：`Check()` 从零重算精确缩放位置（与缩放器不共享
  状态），验证：每包漂移 ∈ [0,1) tick、流内因果、分片结构、链路串行、同 tick
  声明顺序、倍率变化点连续性；并把声明的最小包时距在缩放后无法满足的位置列
  入 `Unsatisfiable`。
- **倍率语义**：倍率 1 保持原轨迹；倍率 0（及负值、零分母）在构造时拒绝
  （`ErrZeroRate`）。倍率变化点处精确位置连续。

## 布局

- `ratio.go` — 正有理数倍率（拒绝 0），`p/q` 解析
- `scaler.go` — 时间/字节缩放、残差补偿、MTU 分片、倍率变化点
- `sim.go` — 离散事件模型：到达堆、FIFO 队列、串行链路（整数 tick）
- `pipeline.go` — 流式管线、一次性 `Transform`、检查点保存/恢复
- `reference.go` — 独立精确时间轴参考检查器
- `cmd/tsxform` — 命令行样例
- `*_test.go` — 全部测试（`go test`）

## 运行

```sh
go test ./...
go run ./cmd/tsxform -demo -rate 1/3 -bytes 2/1 -mtu 1500 -bw 100 -min-gap 1
# 或从 stdin 读 "流 tick 字节" 行：
printf '0 0 100\n0 10 200\n1 10 300\n' | go run ./cmd/tsxform -rate 1/1 -bw 150
```

## 运行样例（截取）

```
$ go run ./cmd/tsxform -demo -rate 1/3 -bytes 2/1 -mtu 1500 -bw 100 -min-gap 1
SEQ  STREAM  IN  IDEAL  DEPART  DONE  BYTES  FRAG
0    0       0   0      0       2     120    1/1
1    0       1   0      2       4     120    1/1
2    0       2   0      4       6     120    1/1
12   1       0   0      6       14    800    1/1
3    0       3   1      22      24    120    1/1
...
18   1       12  4      132     147   1500   1/6
18   1       12  4      147     162   1500   2/6
18   1       12  4      162     177   1500   3/6
18   1       12  4      177     192   1500   4/6
18   1       12  4      192     207   1500   5/6
18   1       12  4      207     212   500    6/6
check: violations=0 unsatisfiable-spacing=10 max-drift=2/3 tick
UNSATISFIABLE: stream 0 packets ending at input tick 1: ideal spacing 0 < required 1
...
```

可见：理想 tick 0 处三个流按声明顺序（seq 0,1,2,12,…）合并；4000 字节包经
2/1 字节倍率缩放为 8000 字节后按 MTU=1500 分成 6 片串行发出；1/3 时间倍率下
声明的 1 tick 最小包时距无法满足，被逐一列出；最大漂移 2/3 tick < 1。

倍率 0 被拒绝：

```
$ go run ./cmd/tsxform -demo -rate 0/1
tsxform: rate must be positive: zero rate rejected
```

## 测试结果

```
$ go test -v ./...
=== RUN   TestCheckpointResumeEquivalence      --- PASS  # 断点续跑 == 一次处理
=== RUN   TestChunkedFeedWithPausesEquivalence --- PASS  # 输入分片+暂停 == 一次处理
=== RUN   TestPauseIsTransparent               --- PASS  # 空暂停不改变输出
=== RUN   TestCheckpointWhileLinkBusy          --- PASS  # 链路在途时也可检查点
=== RUN   TestReferenceDriftBoundLongSequence  --- PASS  # 5000 包最大漂移 2/3 tick < 1
=== RUN   TestRateChangePoint                  --- PASS  # 倍率变化点连续且通过参考检查
=== RUN   TestUnsatisfiableSpacingReported     --- PASS  # 无法满足的包时距被报告
=== RUN   TestCheckerDetectsCorruption         --- PASS  # 参考检查能发现篡改
=== RUN   TestIdentityRatePreservesTrace       --- PASS  # 倍率 1 保持原轨迹
=== RUN   TestZeroRateRejected                 --- PASS  # 倍率 0 拒绝
=== RUN   TestSmallIntervalsBoundedDrift       --- PASS  # 3000 个小间隔: 999 tick 受界, 每 tick ≤ 3 包
=== RUN   TestByteResidualCompensation         --- PASS  # 字节残差补偿, 总量精确
=== RUN   TestFragmentationAndCausality        --- PASS  # 超 MTU 分片+流内因果
=== RUN   TestByteScaleUpFragments             --- PASS  # 字节放大后分片
=== RUN   TestEmitRejectsBadInput              --- PASS  # 非法输入拒绝
=== RUN   TestParseRatio                       --- PASS
=== RUN   TestDeclaredOrderMergeAndSerialization --- PASS  # 同 tick 声明顺序+链路串行
=== RUN   TestCausalityRandomTrace             --- PASS  # 随机轨迹流内因果
PASS
ok  	tsxform
```
