# swimdsim — 离散事件网络模拟器 + SWIM 协议确定性测试驱动

单进程、纯 Go 标准库实现的离散事件网络模拟器，为 SWIM 故障检测协议状态机
提供**全确定性**测试环境：固定随机种子即可 100% 复现包括竞态在内的任意运行轨迹。

## 架构

```
sim/    离散事件模拟器
        - Simulator: 虚拟时钟，Step() 每次步进 1 tick；
          事件堆按 (time, seq) 全序排列，同时刻事件顺序确定
        - Switch:    虚拟交换机，支持链路分区 (Partition/Heal)、
          均匀丢包率、均匀延迟抖动；全部随机性来自单个带种子 RNG
swim/   SWIM 协议状态机
        - Ping / Ack / PingReq 直接+间接探测
        - Alive / Suspect / Dead 三态，incarnation 号仲裁
        - 感染式 gossip 传播（按节点去重、最新优先，防止反驳消息饿死）
        - 仲裁规则：(incarnation, state) 全序，同 incarnation 下
          Dead > Suspect > Alive；关于自身的谣言一律通过
          提升自身 incarnation 反驳（refute）
cmd/swimdemo/  可运行的竞态复现样例
```

确定性的三个支柱：

1. 事件堆以 `(time, seq)` 全序排序 —— 同一 tick 内的事件顺序固定；
2. 所有随机性（丢包、抖动）集中在交换机内单个 `rand.NewSource(seed)`；
3. 协议层不迭代 map（成员按排序后的切片轮询），无隐式顺序依赖。

## 竞态场景：两节点同时宣布对方崩溃

`swim/swim_test.go` 中 `TestMutualDeathRaceDeterministic`：

- tick 100：虚拟交换机切断 `A<->B` 链路；
- 双方探测对称超时，**同一 tick 117 互相标记 Suspect，同一 tick 221
  互相宣布对方 Dead** —— 精确复现极端竞争场景；
- tick 400：链路恢复，双方收到关于自己的 Dead 谣言，各自将 incarnation
  提升为 1 并广播 Alive 反驳；
- 仲裁代码确定性收敛：双方视图一致为 `ALIVE(inc=1)`；
- 全程运行 **整整 10000 个离散时钟步长**，并以相同种子完整运行两遍，
  逐字节比对日志序列，验证零随机性、百分百可复现。

## 运行样例

```
$ go run ./cmd/swimdemo
tick=0100 **** virtual switch: link A<->B PARTITIONED ****
tick=0117 node=A suspect B inc=0 (local probe failed)
tick=0117 node=B suspect A inc=0 (local probe failed)
tick=0221 node=A declare-dead B inc=0
tick=0221 node=B declare-dead A inc=0
tick=0400 **** virtual switch: link A<->B HEALED ****
tick=0402 node=A refute DEAD-rumor inc=0 -> bump incarnation to 1
tick=0403 node=B refute DEAD-rumor inc=0 -> bump incarnation to 1

final membership after 10000 deterministic clock steps:
  node A sees: [A=ALIVE(inc=1) B=ALIVE(inc=1)]
  node B sees: [A=ALIVE(inc=1) B=ALIVE(inc=1)]

switch stats: sent=1970 dropped=30 delivered=1940
```

## 测试结果

```
$ go test ./... -v
=== RUN   TestArbitrationOrdering
--- PASS: TestArbitrationOrdering (0.00s)
=== RUN   TestSelfRefutation
--- PASS: TestSelfRefutation (0.00s)
=== RUN   TestMutualDeathRaceDeterministic
    swim_test.go:99: race reproduced: A and B declared each other dead at tick 221
    swim_test.go:136: switch stats: sent=1970 dropped=30 delivered=1940
--- PASS: TestMutualDeathRaceDeterministic (0.00s)
=== RUN   TestConvergenceUnderLossAndJitter
    swim_test.go:172: no false positives over 10000 steps; switch: sent=7138 dropped=1037 delivered=6099
--- PASS: TestConvergenceUnderLossAndJitter (0.01s)
PASS
ok      swimdsim/swim
```

| 测试 | 验证内容 |
| --- | --- |
| `TestArbitrationOrdering` | 仲裁全序：同 incarnation 下 Dead>Suspect>Alive，旧 incarnation 一律忽略，只有更新的 incarnation 能复活 |
| `TestSelfRefutation` | 关于自身的谣言必然被 incarnation+1 反驳，过期谣言被忽略，反驳进入 gossip 队列 |
| `TestMutualDeathRaceDeterministic` | 10000 时钟步内复现双节点同 tick 互判崩溃，仲裁确定性收敛，双跑日志逐字节一致 |
| `TestConvergenceUnderLossAndJitter` | 4 节点、15% 丢包、1-5 tick 抖动下跑 10000 步零误判，且结果可复现 |
