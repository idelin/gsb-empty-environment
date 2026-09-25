# snaprepl — 分块快照安装 + 日志追赶的单进程事件模拟

纯 Go 标准库实现。落后副本先以分块快照（chunked snapshot）追平，再接续快照
索引之后的日志；通信、故障与时间全部由单进程离散事件模拟器控制，运行完全
确定（种子化随机数）。

## 结构

- `sim.go` — 离散事件模拟器：虚拟时钟 + 事件堆，时间只在事件处理时推进。
- `net.go` — 故障网络：随机时延（乱序）、丢包、重复、快照块比特翻转、节点隔离。
- `node.go` — 副本协议：固定 leader、多数派提交、日志压缩、快照流式发送与重传、
  跟随者暂存/发布/回滚、按序恰好一次应用。
- `snapshot.go` — 快照与暂存区：按偏移去重缓冲，收齐字节才允许发布。
- `monitor.go` — 安全性监视器：记录每次 apply/publish/rollback 并检查不变量。
- `cluster.go` — 集群装配、心跳、客户端写入、收敛判定（活性证据）。
- `cmd/demo` — 两个演示场景；`replica_test.go` — 全部测试。

## 关键机制

- **先快照后日志**：`nextIndex <= baseIndex` 的跟随者收到快照流；安装完成后
  leader 从 `snapIndex+1` 继续 AppendEntries。
- **安装期间不混读**：暂存字节对读者不可见，状态机只在摘要校验通过时原子
  换入（`publishSnapshot` 中 `n.SM = kv` 一处切换）。
- **摘要错误回滚**：组装后 SHA-256 不匹配则丢弃全部暂存块并 nack，旧状态
  不受影响；leader 收到 nack 立即重传整流。
- **缺块不发布**：`staging.complete()` 要求字节数凑齐，重复块幂等忽略。
- **跳过已包含日志**：`Index <= baseIndex` 的日志条目不追加、不应用；
  `lastApplied` 已覆盖的快照（迟到的旧快照流）直接 ack 跳过，防止状态机回退。

## 安全性证据（不变量，由 Monitor 在运行中检查）

1. 每个副本按严格递增的索引应用日志（交错/重复到达不重复应用）；
2. 任何副本不会应用已被其安装快照覆盖的日志条目；
3. 状态机安全：两个副本应用同一索引时命令必然相同；
4. 读隔离：安装期间读只见旧状态，发布后只见新状态（`TestNoMixedReadsDuringInstall`）；
5. 摘要错误必回滚且旧状态无损（`TestDigestMismatchRollback`）。

## 活性证据

故障（丢包/重复/乱序/损坏/隔离）在虚拟时刻 T 停止后，集群在有界虚拟时间内
收敛：`TestLivenessBoundedConvergence` 断言收敛时刻 ≤ T+300；
`TestSeedSweepSafetyAndLiveness` 在 20 个随机种子上同时验证安全性与收敛。

## 运行

```sh
go test -v ./...   # 测试（含安全性/活性证据输出）
go run ./cmd/demo  # 运行样例：场景A 隔离追赶；场景B 损坏回滚恢复
```
