# Multi-Paxos 共识日志 + 稀疏数据内存紧凑优化器 (Go)

纯标准库实现，包含两个组件：

- `paxos/` — Multi-Paxos 多数派共识与无空洞日志提交引擎
- `sparse/` — 海量稀疏数据的自适应容器与内存紧凑优化器
- `cmd/demo/` — 运行样例

## 环境

Go 1.11+（开发验证使用 go1.11.4），仅标准库。构建缓存不可写时可设
`GOCACHE=/private/tmp/gocache`。

## 运行

```sh
GO111MODULE=on go test ./...          # 全部测试
GO111MODULE=on go test ./... -race    # 并发竞态检查
GO111MODULE=on go run ./cmd/demo      # 运行样例
```

## paxos：协议要点

- **两阶段**：Phase 1 `Prepare/Promise` 选主并学习多数派日志；Phase 2
  `Accept/Accepted` 复制日志条目。
- **领导者租约**：选主成功后租约有效期内 `Propose` 直接走 Phase 2，
  测试断言租约期内零 `Prepare` 消息。
- **无空洞提交**：日志始终连续；follower 拒绝越界 `Accept` 并上报日志
  长度，领导者按缺口回填（hole repair）；新 leader 当选时按槽位取最高
  任期条目合并日志，并追加本任期 anchor no-op 使修复前缀可提交。
- **脏日志覆写**：少数派未提交条目在分区恢复后由新 leader 以更高任期
  的 `Accept` 安全覆写（`Sync`）。
- **提交规则**：仅本任期条目达到多数派才推进提交点，旧任期条目随之
  传递提交；`knownCommit` 保证乱序 `Commit` 消息不丢失进度。

### 验收场景（`TestPartitionReelectionAndDirtyOverwrite`）

5 节点集群：node 0 为旧 leader → 分区 `{0,1}|{2,3,4}` → 未提交槽位 C
留在多数派一侧、脏条目 DIRTY 留在少数派 → node 2 经 Phase 1 当选并
补全 C → 分区恢复后 `Sync` 覆写少数派脏日志 → 5 节点日志完全一致、
无空洞、全部提交。

## sparse：优化器要点

- 键空间按 16 bit 分片，每片在 **有序数组 / 位图 / 游程（RLE）** 三种
  容器间自适应转换：数组超 4096 键转位图，位图降回 2048 键以下转数组
  （迟滞避免抖动），`Optimize()` 按精确内存记账为每片挑选最紧凑编码。
- **压缩**：快照采用 varint + 差分编码，可再经 gzip 二次压缩；
  `LoadSnapshot` 严格校验截断、越界、非法标签与尾部垃圾。
- **无泄漏**：`MemBytes()` 精确记账，`Clear()` 后回到空集基线；
  测试断言 goroutine 数量与内存基线严格复位。
- 实测：200 万稀疏键（1000 段游程）优化后约 0.02 字节/键。
