# mpaxos — Multi-Paxos 多数派共识与无空洞日志提交引擎

纯 Go 标准库实现，无任何第三方依赖。

## 功能

- **两阶段共识**：Phase 1（Prepare/Promise）+ Phase 2（Accept/Accepted），多数派提交，无单点故障。
- **领导者租约**：租约有效期内跳过 Phase 1，直接以 Phase 2 连续提交 LogEntry；租约过期前多数派无法被新 Leader 抢占。
- **无空洞日志**：新 Leader 通过 Phase 1 收集多数派的已接受值，逐槽位取最高选票值补全未提交槽位，空洞以 no-op 填充；提交索引严格按序推进。
- **脏日志安全覆写**：Commit 消息携带全部已提交槽位的选票指纹，follower 比对本地槽位，不一致的少数派脏日志通过 Fetch 拉取已提交值安全覆写。
- **持久化编解码**：紧凑二进制格式（magic + version 头，CRC32 + 长度前缀记录），追加式存储，崩溃后撕裂写自动截断。
- **零拷贝还原**：`Store.View` 通过 mmap 只读映射直接解析，`Entry.Cmd` 别名映射页，不发生拷贝；`Load`/`LoadLog` 提供拥有所有权的拷贝。
- **兼容性**：版本头向后兼容（旧版本可读），payload 预留尾部前向兼容（新写入器的未知字段被忽略）。
- **边界防溢出**：记录长度上限、长度前缀与缓冲区交叉校验、cmdLen 与 payload 交叉校验，损坏输入只返回错误，绝不 panic。

## 文件格式

```
文件头 (16B):  magic "MPAXLOG1" | version u32 | flags u32
记录:          crc32 u32 | length u32 | payload
payload:       term u64 | node u32 | index u64 | cmdLen u32 | cmd ... | (预留扩展)
```

## 目录结构

- `paxos/codec.go` — 二进制编解码、扫描、零拷贝解码
- `paxos/store.go` — 持久化存储、mmap 映射、撕裂恢复
- `paxos/types.go` — Ballot / 消息类型
- `paxos/node.go` — Multi-Paxos 节点（选举、租约、修复、提交）
- `paxos/cluster.go` — 内存传输层（分区模拟、消息统计）
- `cmd/demo` — 验收场景运行样例

## 运行

```sh
go test ./paxos/ -count=1 -v   # 全部测试
go test ./paxos/ -race         # 并发安全（竞态检测）
go run ./cmd/demo              # 5 节点分区恢复演示
```

## 测试覆盖

- 编解码：往返一致性、零拷贝别名证明、逐字节截断、CRC 损坏、长度/cmdLen 溢出、撕裂尾扫描、前后向兼容、并发编解码。
- 存储：重开恢复、mmap 零拷贝视图、撕裂写截断、坏魔数拒绝、并发追加+并发映射读取、最高选票折叠。
- 共识：选举与提交、租约绕过 Phase 1（断言零 Prepare 流量）、5 节点分区验收场景（旧 Leader 被取代、未提交槽位补全、少数派脏日志覆写）、no-op 补洞、并发提案一致性、节点重启恢复、goroutine 无泄漏。
