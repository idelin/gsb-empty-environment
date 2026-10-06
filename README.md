# logsim — 单机日志复制模拟器

Raft 风格的单节点日志副本（follower）模拟：接收不同任期的追加请求，
匹配前驱后截断冲突后缀，持久化后才确认。

## 规则（replica/replica.go）

- **旧任期拒绝**：`req.Term < currentTerm` → `stale-term`。
- **前驱匹配**：`PrevIndex` 不存在 → `missing-predecessor`；`PrevTerm`
  不匹配 → `prev-term-mismatch`（若前驱已提交则报损坏）。
- **冲突截断**：同索引不同任期的未提交后缀被截断后追加；已提交前缀
  （`index <= commitIndex`）不可截断，违反即 `ErrCorruption`。
- **损坏检测**：同索引同任期不同内容 → `ErrCorruption`。
- **先持久化后确认**：状态变更先写盘（tmp+fsync+rename，先日志后元
  数据），`Save` 失败或崩溃时不返回 ACK，内存状态不变。
- **提交推进**：`commit = min(LeaderCommit, lastIndex)`，只前进不后退。
- **重复消息幂等**：完全/部分重复的追加直接 ACK，不产生副作用。

## 崩溃切点（replica/store.go）

`Save` 暴露四个切点：`save.begin`、`log.persisted`、`meta.persisted`、
`save.done`。Hook 返回错误模拟持久化失败，panic 模拟断电（内存状态
丢弃，重启后从磁盘恢复）。日志先于元数据落盘，保证元数据永不引用
未持久化的条目。

## 事件重放（replica/trace.go）

`Recorder` 记录 append/crash 事件序列，可序列化为 JSON；`Replay` 在
全新副本上重放（crash 事件 = 丢弃内存副本、从磁盘恢复），`DiffSteps`
逐条比对重放结果与原始观测。

## 运行

```sh
go test ./...    # 崩溃切点扫描、重复消息、缺失前驱、提交推进、事件重放
go run ./cmd/sim # 脚本化演示场景
```
