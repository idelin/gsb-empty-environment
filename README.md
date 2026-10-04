# Multi-Paxos 共识日志 + 自适应阈值伸缩（Go，仅标准库）

无单点故障的多数派共识复制日志，带领导者租约快路径、空洞修补，以及根据
外部压力自调节内部参数的自适应控制器（防雪崩、负载感知）。

## 结构

- `paxos/types.go` — Ballot / Entry / 两阶段 RPC 消息
- `paxos/network.go` — 内存传输层，支持编程式网络分区与恢复
- `paxos/node.go` — Multi-Paxos 节点：
  - Phase 1 `Prepare/Promise`：Promise 携带未提交日志尾部用于重建
  - Phase 2 `Accept/Accepted`：多数派确认后提交并广播 Commit
  - 领导者租约：租约有效时跳过 Phase 1，连续槽位直接提交
  - 空洞修补：新 Leader 重建时对空缺槽位填 noop，对每槽取最高 ballot 值
  - 脏日志覆写：已提交条目不可变；少数派未提交脏数据在心跳同步时被安全覆写
- `paxos/adaptive.go` — 自适应控制器：
  - 在飞并发上限 AIMD 伸缩（饱和且低载加性增、过载/失败乘性减，双向钳位）
  - 熔断器：连续失败阈值触发，抖动冷却后半开探测，避免重试雪崩
  - 负载感知批大小：随负载线性收缩
  - 租约时长跟随 RTT 平滑值并在 [MinLease, MaxLease] 内钳位
- `cmd/demo` — 运行样例：5 节点分区恢复 + 负载尖峰下的参数自调节

## 运行

```sh
go test ./... -race -v   # 全部测试（含竞态检测）
go run ./cmd/demo        # 运行样例
```

## 验收场景（TestPartitionRecovery）

5 节点集群分区为 {0,1}|{2,3,4}：旧 Leader 0 在少数派分区中无法提交
（槽 4 留下脏数据），多数派选出新 Leader 2，通过 Phase 1 补全未提交槽位；
分区恢复后旧 Leader 退位，少数派脏日志被已提交值安全覆写，全集群日志一致且无空洞。
