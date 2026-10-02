# Delta-CRDT 增量压缩同步协议（TypeScript）

将全量 State-based CRDT 优化为**增量 Delta 包**传输：每个节点记录每个远端
节点的"已确认水位"（ack watermark，即版本向量），仅生成并发送自上次确认
之后变动的 Delta 差异切片。零运行时依赖，直接使用 Node.js 内置类型擦除
运行 TypeScript、内置 `node:test` 运行测试。

## 运行要求

- Node.js **>= 22.6**（本仓库在 v23.9.0 上验证；22.x 需 `--experimental-strip-types`，脚本已内置该flag）

```bash
npm test    # 运行全部测试（node:test）
npm run demo # 运行 10000 节点文档的同步演示
```

## 架构

| 文件 | 职责 |
| --- | --- |
| `src/versionVector.ts` | 版本向量：join / covers / 相等性比较 |
| `src/document.ts` | Delta-state 序列 CRDT（RGA）：op 日志即状态，`deltaSince(vv)` 产出增量切片 |
| `src/protocol.ts` | 线协议消息：`sync-request` / `delta` / `ack`，JSON 编码与字节测量 |
| `src/node.ts` | `SyncNode`：维护每个 peer 的确认水位，只发送水位以上的 Delta |
| `src/transport.ts` | 可靠有序内存传输 + 流量统计（生产环境可替换为任意传输层） |

## 核心机制

1. **状态 = 每副本连续 op 日志**。每个副本的每次变更携带连续序号 `s`
   （insert/delete 均为 op，delete 为墓碑）。状态的版本向量即各副本日志长度。
2. **Delta 区间**。`deltaSince(vv)` 返回所有 `s > vv[replica]` 的 op，
   按副本分组、按序号排序，保证每副本因果连续。全量传输只是
   `deltaSince({})` 的特例——Delta 与全量共用同一消息格式。
3. **确认水位**。接收方应用 Delta 后回 `ack`（携带自身版本向量）；发送方
   按分量取 max 更新该 peer 的水位，之后只发送水位以上的切片。无变更时
   零流量。
4. **幂等 + 因果缓冲**。重复投递按序号去重；乱序 op 按副本缓冲至前驱到达；
   跨副本孤儿 insert / 先到墓碑也会挂起并在依赖到达后解析，保证最终一致。

## 验收边界实测

在 10000 节点文档中修改 1 个字符（`npm run demo` 实际输出）：

```
document size: 10000 chars (CRDT nodes)
full state transfer: 602.3 KiB

[initial sync] wire traffic: 6 msgs, 602.5 KiB
[initial sync] B in sync: true
[initial sync] A's ack watermark for B: {"A":10000}

[1-char edit] delta packet: 140 B (2 msgs, 187 B total incl. ack)
[1-char edit] delta ops: 1
[1-char edit] delta payload: {"type":"delta","from":"A","base":{"A":10000},"ops":[{"t":"i","id":"A:10001","p":"A:5000","c":"X","r":"A","s":10001}],"version":{"A":10001}}
[1-char edit] B in sync: true
[1-char edit] texts equal: true

[no changes] extra traffic after ack: 0 msgs
[equivalence] delta-synced B == full-state C: true
```

- **常数级 Delta 包**：1 字符变更 → 140 字节（含 1 个 op），与文档大小无关；
  测试在 1k / 10k / 100k 节点文档上验证 Delta 均 < 256 字节且尺寸差仅来自
  序号十进制位数，而全量传输随文档线性增长（测试断言 5 倍以上放大）。
- **状态完全等价**：增量同步的 B 与全量传输的 C 指纹（规范化状态序列化）
  逐字节相同，文本相同，版本向量相同。

## 测试结果

`node --experimental-strip-types --test test/*.test.ts`（Node v23.9.0）：

```
ℹ pass 22
ℹ fail 0
ℹ duration_ms ~2600
```

覆盖：版本向量运算；本地增删；并发插入收敛；幂等合并；乱序/孤儿/先到
墓碑的因果缓冲；Delta 切片精确性；1 字符插入与删除的常数级包大小；
Delta 与全量传输等价性；ack 水位推进与零变更零流量；三节点全网状并发
收敛；协议编解码往返。

## 说明与边界

- 内存传输层假设可靠有序；CRDT 合并幂等，叠加超时重传即可适配不可靠
  网络（重复 Delta 无害）。
- 未做 op 日志垃圾回收：水位仅用于限制 Delta 大小。生产实现可在
  "所有 peer 均已确认"的最小水位以下截断日志（需保留快照以引导新节点）。
