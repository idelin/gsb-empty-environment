# Multi-Paxos 多数派共识与无空洞日志提交引擎（TypeScript）

零依赖实现：Node.js 内置类型擦除（>= 22.6）直接运行 TypeScript，内置 `node:test` 测试。

## 运行

```bash
npm test        # node --test 'test/**/*.test.ts'（36 个测试）
npm run demo    # 5 节点分区故障转移演示
```

## 组件

| 文件 | 职责 |
| --- | --- |
| `src/codec.ts` | 二进制帧编解码：紧凑布局、CRC32 完整性、边界防溢出、前向/后向兼容、零拷贝视图、增量流解析 |
| `src/logstore.ts` | 追加式持久化日志段：打开时整体读入（mmap 风格）一次扫描建索引，读取返回零拷贝视图；写操作经 promise 链串行化；支持后缀截断与 fsync 配置 |
| `src/paxos.ts` | Multi-Paxos：Prepare/Promise + Accept/Accepted 两阶段、领导者租约跳过 Phase 1、Phase 1 修复空洞（no-op 填充）并采纳已提交前缀、prevTerm 日志一致性检查、脏日志截断覆写、落后副本回填 |
| `src/transport.ts` | 内存传输：确定性微任务投递、链路阻断/网络分区注入、消息统计 |
| `src/cluster.ts` | 测试/演示集群装配 |

## 二进制帧格式（小端）

```
offset 0   u32   magic = 0x4D50584C
offset 4   u8    格式版本
offset 5   u8    条目类型（0 = noop, 1 = data）
offset 6   u16   headerLen（>= 32，超出部分为前向兼容扩展区）
offset 8   u64   term（Paxos ballot 编码）
offset 16  u64   日志槽位 index
offset 24  u32   payloadLen
offset 28  u32   crc32（header 的 crc 字段清零后 + payload）
offset 32  ..    扩展区（headerLen - 32 字节）
offset headerLen ..  payload
```

- **零拷贝还原**：`decodeEntryView` / `scanFrames` 返回的 `payload`、`extensions` 是源 Buffer 的 `subarray` 视图，不发生任何拷贝。
- **边界防溢出**：所有长度字段先与缓冲区实际大小校验；`payloadLen` 超过硬上限（64 MiB，可配置）直接以 `OVERFLOW` 拒绝，不触发任何分配。
- **兼容性**：后向——新读者接受旧版本帧；前向——旧读者通过 `headerLen` 跳过未知扩展区，并可用 `maxVersion` 策略拒绝过新版本。

## 共识协议要点

- **Phase 1（Prepare/Promise）**：候选人携带自身 `commitIndex`；接受者回复其后的全部已接受条目及自身 `commitIndex`。新 Leader 先**采纳多数派已提交前缀**（防止覆写已提交值），再对未提交区间逐槽选择最高 term 的值重新提案，无值槽位填充 **no-op**（无空洞提交）。
- **Phase 2（Accept/Accepted）**：多数派确认后按序推进 `commitIndex` 并广播提交。
- **领导者租约**：租约有效期内跳过的 Phase 1，连续 LogEntry 直接 Phase 2 提交；租约过期自动重新 Phase 1。
- **日志一致性**：Accept 携带 `prevIndex/prevTerm`；跟随者发现空洞或脏后缀即拒绝并请求**回填（backfill）**，Leader 从其已提交前缀开始重发并覆写少数派脏日志（截断 + 重写）。
- **提交安全**：提交广播携带 `commitTerm`，跟随者仅在本地日志与 Leader 已提交日志逐点一致时才推进 `commitIndex`。

## 验收场景（test/paxos.test.ts）

5 节点集群 → 分区 `{1,2}|{3,4,5}` → 旧 Leader（node1）在少数派侧产生未提交脏日志 → 多数派选出新 Leader（node4）→ Phase 1 修复槽位 → 分区恢复 → 少数派脏日志被安全覆写 → 全集群收敛、已提交值零丢失、日志无空洞。

## 测试覆盖（36 个用例）

- 编解码：往返一致性、u64 边界、零拷贝内存共享、逐字节截断、CRC 损坏、长度溢出、版本前向/后向兼容、流式重组、段扫描。
- 存储：持久化重放、截断缩文件、300 并发追加全序、并发追加+截断不损坏、关闭后拒绝写入（无 fd 泄漏）。
- 共识：选主与顺序提交、租约跳过/过期重选、分区故障转移验收、已选值保留、no-op 空洞填充、脏日志覆写、50 并发提案恰好一次且连续、并发竞选安全性。
- 泄漏：节点计时器清零、传输队列排空、进程级 Timeout/Immediate 资源不增长。
