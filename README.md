# redsim — RED 式早丢弃离散事件模拟

纯标准库 Go 实现。所有包、队列、链路都运行在整数 tick 的离散事件模型中，
不发任何真实网络流量。

## 设计要点

- **可注入随机源**：一切抽样走 `Source` 接口（`redsim/prng.go`）。
  `Deriver.For(kind, id)` 按**事件身份**从主 seed 分流，抽样结果只取决于
  “谁在抽”，与调用顺序无关——因此重放与分块运行不改变抽样
  （`TestDeterministicReplay` / `TestChunkedRunMatchesSingleRun`）。
- **平均队长更新与空闲衰减分离**（`redsim/red.go`）：
  `UpdateAvg` 只做忙时指数平均；`ApplyIdleDecay` 只做空闲衰减
  `avg *= (1-wq)^m`，两者互不调用。
- **包长归一化严格分离**：`NormalizeProb` 仅在丢包判定阶段按
  `Len/MTU` 缩放概率（ByteMode），与时间维度的衰减无关。
- **阈值规则**：`avg < MinTh` 不概率丢包（甚至不取样，测试用
  panic 源证明）；`avg >= MaxTh` 强制丢包；区间内
  `pa = pb/(1-count*pb)`。
- **每次丢弃输出依据**：`Decision` 记录 tick、队列、事件 ID、包长、
  判定时刻 avg、空闲衰减长度、pb/pa、抽取值与原因（forced/probabilistic）。
- **多队列共享链路 + DRR 公平调度**（`redsim/sim.go`）：每队列独立
  RED 配置；DRR 按字节配额调度，长期字节份额均衡、无饥饿。
- **字节守恒**：`CheckByteConservation` 复核 到达 == 丢弃 + 离链 + 在队。
- **有限随机位枚举**：`TestEnumerationDropProbabilityAndByteConservation`
  枚举全部 2^12 个抽取值，精确验证丢包概率（768/4096 = 0.1875）与字节守恒。
- **实验只作性能对照**：分窗口离链字节（`WindowedBytes`）直接暴露长期
  饥饿，不以平均延迟掩盖。

## 运行

```sh
go run ./cmd/redsim -seed 42 -ticks 10000
go test ./...
```

## 运行样例（节选）

```
== 包模式 RED + DRR (seed=42) ==
-- 每次丢弃依据 --
tick=410    q=0 evt=246  len=1500  avg=   3.713 idleTicks=0   pb=0.0079 pa=0.0226 draw=0.0100 -> probabilistic(draw<pa)
tick=666    q=1 evt=400  len=800   avg=   3.676 idleTicks=0   pb=0.0075 pa=1.0000 draw=0.6072 -> probabilistic(draw<pa)
tick=770    q=0 evt=462  len=1500  avg=  12.089 idleTicks=0   pb=0.0000 pa=0.0000 draw=0.0000 -> forced(avg>=maxTh)
...
q0: arrived=2000 dropped=1257 departed=732  dropRate=0.6285 bytesIn=3000000 bytesOut=1098000 bytesDropped=1885500  (字节模式)
q1: arrived=2000 dropped=471  departed=1518 dropRate=0.2355 ...
q2: arrived=2000 dropped=0    departed=1999 dropRate=0.0000 ...
byteConservation=true
-- 分窗口离链字节（窗口=2000 tick，前 5 窗）--
q0: 219000 115500 264000 249000 250500
q1: 202400 275200 239200 248800 248800
q2: 119700 120000 119700 120300 120000
```

字节模式下长包队列 q0 丢包率显著上升（包长归一化生效），q2 队列短不丢；
三个队列每个窗口都有吞吐，无长期饥饿。

## 测试结果

```
ok  	redsim/redsim	0.234s
```

10 个测试全部通过：阈值下不丢（且不取样）、阈值上强制丢、空闲衰减与
平均更新分离、包长归一化分离、2^12 枚举验证丢包概率与字节守恒、
PRNG 按事件身份分流、同 seed 轨迹一致、分块运行一致、全链路字节守恒、
DRR 公平性与无饥饿。
