# redsim — RED 早丢弃离散事件模拟

纯 Go 标准库实现的 RED（Random Early Detection）模拟器。所有包、队列和
链路都运行在整数 tick 的离散事件模型中，不产生任何真实网络流量。

## 设计要点（与需求对应）

- **参数明确的 RED**：`red.Params`（minTh/maxTh/maxP/wq/容量/参考包长），
  构造时校验。阈值以下绝不概率丢包；avg ≥ maxTh 一律强制丢。
- **随机源可注入**：`red.Source` 接口；默认实现 `red.KeyedSource` 用
  splitmix64 混合 (seed, eventID)。
- **PRNG 按事件身份分流**：抽样只取决于 (seed, 事件身份)，与消费顺序无关，
  因此重放与分块处理不改变抽样（`TestReplayDeterminism`、
  `TestChunkingInvariance` 用轨迹摘要验证）。
- **平均队长更新与空闲衰减分开**：`UpdateAverage`（EWMA）与
  `DecayIdle`（`(1-wq)^t` 几何衰减）是两个独立方法。
- **空闲衰减与包长归一化严格分离**：`NormalizeForPacketSize` 只把基础概率
  按 `size/maxSize` 缩放，不接触平均队长；空闲衰减不接触概率。
- **每次丢弃有据可查**：每个判决定（`red.Decision`）记录 avg、基础概率、
  归一化概率、随机抽取值和文字理由；模拟器把每次丢弃追加到 `Network.Drops`。
- **多队列共享链路 + 公平调度**：每条流一个 RED 队列，共享一条链路，
  DRR（deficit round robin）按字节公平调度，quantum 可配置。
- **有限随机位枚举验证**：测试用 `exhaustiveSource` 枚举全部 2^12 个随机
  位取值，验证经验丢弃概率精确等于 `ceil(p·N)/N`，并验证字节守恒
  （到达字节 = 接受字节 + 丢弃字节）。
- **不以平均延迟掩盖饥饿**：统计输出每流最大延迟、min/max 吞吐比和 Jain
  公平指数；测试直接断言混合包长下无流饥饿（`TestMixedSizesNoStarvation`）。
- **seed 相同轨迹相同**：所有丢弃/完成事件写入 FNV 摘要，同 seed 摘要一致。

## 布局

```
red/red.go        RED 核心：参数、可注入随机源、判定与丢弃依据
red/red_test.go   阈值行为、有限随机位枚举、衰减/归一化分离、PRNG 分流
sim/sim.go        离散事件引擎：事件堆、FIFO 队列、共享链路、DRR 调度
sim/sim_test.go   重放/分块一致性、字节守恒、公平调度与反饥饿
cmd/redsim        可运行样例（性能对照 harness）
```

## 运行

```sh
go test ./...                 # 全部测试
go run ./cmd/redsim           # 默认样例（seed=42, 3 流, 30000 tick）
go run ./cmd/redsim -seed 7 -flows 4 -ticks 50000 -rate 120 -quantum 1500
```

## 运行样例（`go run ./cmd/redsim`）

```
RED discrete-event simulation (integer ticks, in-memory only)
seed=42 flows=3 horizon=30000 link=100B/tick quantum=1500B
red: minTh=4000B maxTh=12000B maxP=0.50 wq=0.002 capacity=30000B maxPkt=1500B

flow  arrived  completed  dropped  drop%   bytesIn   bytesOut  avgDelay  maxDelay
   0     1318       1248       70    5.3   1008527    939009     300.2       591
   1     1328       1208      120    9.0   1071951    948133     361.4       962
   2     1343       1232      111    8.3   1062223    947037     356.2       805

totals: arrived=3989 dropped=301 (7.5%)
fairness: Jain index=1.0000  min/max flow throughput=0.9904
worst-case delay across flows: 962 ticks (reported so averages cannot hide starvation)

drop verdicts: probabilistic=294 forced=0 overflow=7 (total=301)
sample drop bases:
  tick=6526   flow=0 pkt=281   size=212 B verdict=drop-probabilistic avg=5013.5 p=0.0090 draw=0.0031
    basis: early drop: draw 0.003109 < p 0.008953 (base 0.063343 x size 212/1500)
  tick=6704   flow=0 pkt=290   size=795 B verdict=drop-probabilistic avg=5152.4 p=0.0382 draw=0.0323
    basis: early drop: draw 0.032350 < p 0.038174 (base 0.072026 x size 795/1500)
  tick=6968   flow=2 pkt=2946  size=911 B verdict=drop-probabilistic avg=5118.8 p=0.0425 draw=0.0387
    basis: early drop: draw 0.038681 < p 0.042468 (base 0.069926 x size 911/1500)
  ... (298 more drops omitted)

reproducibility: run1 digest=c98ced96f41a5e17 run2 digest=c98ced96f41a5e17 identical=true
```

## 测试结果（`go test ./... -v`，15 个测试全部通过）

```
ok  redsim/red
    TestParamsValidate                      参数校验
    TestBelowThresholdNeverDrops            阈值以下枚举 2^10 个抽取值：零丢弃
    TestAboveThresholdAlwaysForced          阈值以上枚举 2^10 个抽取值：全部强制丢
    TestDropProbabilityByEnumeration        枚举 2^12 随机位：丢弃数 == ceil(p·N)，字节守恒
    TestAverageUpdateAndIdleDecayAreSeparate EWMA 与空闲衰减互不干涉
    TestIdleDecayAppliedOnNextDecide        空闲衰减在下次判定时按空闲 tick 精确生效
    TestPacketSizeNormalization             概率随包长线性缩放并截断到 [0,1]
    TestKeyedSourceDeterministic            (seed, identity) 纯函数
ok  redsim/sim
    TestReplayDeterminism                   同 seed 摘要一致；异 seed 摘要不同
    TestChunkingInvariance                  分块推进与一次跑完轨迹逐位一致
    TestByteConservation                    到达字节 == 完成字节 + 丢弃字节，队列排空
    TestFairSchedulingNoStarvation          DRR 下 min/max 吞吐比 >= 0.7，无流饥饿
    TestMixedSizesNoStarvation              1500B 流与 64B 流均获公平字节份额
    TestNoDropsWhenUncongested              无拥塞零丢弃
    TestDropRecordsCarryBasis               每条丢弃记录带理由且判定与阈值规则一致
```
