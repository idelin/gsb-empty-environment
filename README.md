# Multi-Paxos 多数派共识日志 + 零拷贝持久化编解码 (TypeScript)

无单点故障的分布式共识日志引擎：Multi-Paxos 两阶段协议（Prepare/Promise +
Accept/Accepted）、领导者租约（lease）下绕过 Phase 1 连续提交、空洞修补与
少数派脏日志安全覆写；配套版本化二进制紧凑存储、内存映射式解析与零拷贝还原。
仅依赖 Node.js 标准库（>= 23.6，原生 type-stripping 直接运行 `.ts`）。

## 运行

```bash
npm test     # Node.js 内置测试运行器，52 个测试
npm run demo # 5 节点网络分区验收场景演示
```

## 架构

```
src/
  codec/                 持久化编解码层
    varint.ts            LEB128 变长整数（10 字节上限、安全整数溢出检测）
    crc32.ts             IEEE CRC32（帧完整性）
    writer.ts/reader.ts  有界写入器 / 边界检查读取器（每次读取防溢出）
    frame.ts             帧格式 magic|version|length|payload|crc32
    fields.ts            TLV 字段头与未知字段按 wire-type 跳过（前向兼容）
    logEntry.ts          LogEntry 版本化 schema（v1/v2），零拷贝/拷贝双解码
  storage/
    mappedSegment.ts     内存映射式段解析：单一共享缓冲 + 惰性零拷贝帧视图，
                         容忍崩溃撕裂尾部（torn tail）并报告截断偏移
    logStore.ts          追加式段 + 原子状态文件（tmp+rename），写队列串行化，
                         fsync 持久化 promised ballot / accepted entries
    resources.ts         资源台账：测试断言 store/segment 零泄漏
  paxos/
    types.ts             Ballot 与 Prepare/Accept/Commit 消息
    network.ts           确定性模拟网络：分区/单向断链/消息过滤钩子
    acceptor.ts          promise 持久化 + 领导者租约强制（租约期内拒绝一切
                         Prepare，杜绝竞选活锁）+ prevBallot 日志匹配
    proposer.ts          Phase 1 补全未提交槽位（最高选票值重提、空洞填
                         no-op）；租约内 Phase 2 直连提交；跟随者发散时按
                         needSyncFrom 回填；NotLeader 退避重试
    node.ts / cluster.ts 节点组装与 5 节点集群模拟（共享逻辑时钟）
demo/partition-demo.ts   验收场景：分区→旧 Leader 被取代→脏日志覆写→收敛
```

## 协议要点

- **Phase 1 (Prepare/Promise)**：新 Leader 收集多数派 promise 及其已接受
  条目；对每个未提交槽位取最高 ballot 的值重提（保证多数派已接受的值
  永不丢失），无值槽位填 no-op（保证提交日志无空洞）。
- **Phase 2 (Accept/Accepted)**：多数派接受即提交；提交下标只跨越连续
  已提交槽位。
- **领导者租约**：Phase 1 成功后 Leader 在 `leaseMs` 内跳过 Phase 1 直接
  连续提交；Acceptor 在租约期内拒绝所有 Prepare，故障转移等待租约超时，
  避免双 Proposer 活锁。
- **日志匹配与回填**：Accept 携带 `prevBallot`；跟随者前缀发散时回复
  `needSyncFrom`，Leader 从分叉点逐槽回填，安全覆写少数派脏日志（脏值
  从未被多数派提交，覆写不违反安全性）。

## 兼容性契约

- **向后兼容**：v2 解码器读 v1 帧，缺失字段取默认值（flags=0, ts=0）。
- **向前兼容**：未知 TLV 字段按 wire-type 跳过（模拟 v3 字段测试验证）。
- **防溢出**：varint 超 10 字节 / 超安全整数拒绝；长度前缀超剩余字节或
  超 64MiB 帧上限拒绝；每次读取均做边界检查；CRC 校验帧完整性。
- **零拷贝还原**：`decodeLogEntryView` / `MappedSegment.scan` 返回的
  `value`/`payload` 是底层映射缓冲的 `subarray` 视图（测试通过同一
  ArrayBuffer 身份与源变异可见性双重验证）；需要分离时用
  `decodeLogEntryCopy`。

## 测试（52 个，全部通过）

- 编解码边界：varint 边界值/溢出/截断、帧 CRC 损坏/错误 magic/版本越界/
  长度溢出/流式截断、恶意长度前缀、空值与 1MiB 大值、2^40 边界下标。
- 兼容与零拷贝：v1↔v2 互读、未知字段跳过、视图身份与源变异可见性、
  拷贝分离免疫。
- 共识：选举与提交、租约跳过 Phase 1（Prepare 计数断言）、租约过期重选、
  高 ballot 接管、no-op 空洞修补、重启持久性。
- 验收分区：5 节点 {1,2}|{3,4,5} 分区，旧 Leader 脏槽位 3 无法提交，
  新 Leader 经 Phase 1 接管并覆写，愈合后 5 节点日志逐字节一致。
- 并发与泄漏：64 并发提案唯一连续下标、双 Proposer 竞争收敛、500 帧
  共享映射并发零拷贝解码、写队列/在途消息/资源台账全部归零断言。
