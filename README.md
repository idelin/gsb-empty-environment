# wire-sim

零外部依赖的 TypeScript 工具集：**手工实现的 Protocol Buffers 二进制 Wire-format 编解码器 + 反射模式校验引擎 + 离散时间事件驱动模拟与 BUG 复现框架**。

- 零依赖：仅使用 Node.js 内置模块（`node:test` / `node:assert`）
- TypeScript 源码由 Node ≥ 22.6 原生类型擦除直接执行，无需编译步骤
- 全部整数路径使用 `BigInt` / `Math.imul`，跨平台位级确定

## 目录结构

```
src/
  varint.ts        Varint 7 位压缩编解码、ZigZag 有符号转换、64 位溢出防护
  reader.ts        边界检查的 WireReader：Fixed32/64、Length-delimited、嵌套子读取器
  writer.ts        增长缓冲的 WireWriter（小端 Fixed32/64、长度前缀、嵌套消息）
  schema.ts        反射模式引擎：schema 驱动的 decode / encode / validate
  errors.ts        DecodeError（带字段路径 + 字节偏移）、ValidationError
  sim/
    prng.ts        mulberry32 确定性伪随机源（可快照/恢复）
    scheduler.ts   (time, seq) 二叉堆事件调度器 —— 同时刻严格 FIFO
    simulation.ts  事件驱动内核：step/run/runAsync、逐步指纹、轨迹记录
    replay.ts      BUG 复现：轨迹录制 → protobuf 持久化 → 重放比对 → 定位首个分歧步
    workload.ts    标准确定性负载（单服务台排队模型）
    hash.ts        FNV-1a + 稳定序列化状态哈希
  demo.ts          端到端演示
test/              node:test 测试（60 项）
testkit/capture.ts 测试辅助
```

## 运行

```bash
# 需要 Node >= 22.6（类型擦除）；开发环境使用 v23.9.0
npm test     # 运行全部测试（含 100 万步确定性回归）
npm run demo # 端到端演示
```

## 运行样例（`npm run demo`）

```
=== 1. Deterministic simulation (seed = 42) ===
steps=100000 finalHash=0xb5922b4b
first entry: {"step":1,"time":0,"type":"arrival","hash":1764862728}
last  entry: {"step":100000,"time":120111,"type":"arrival","hash":3046255435}

=== 2. Persist trace as hand-rolled protobuf wire bytes ===
encoded 100000 trace entries -> 2957291 bytes
decoded entries=100000 checksums(packed)=100000
meta: seed=42 steps=100000 magic=0xdec0de51a17
roundtrip identical: true

=== 3. Bug reproduction: replay and compare per-step fingerprints ===
replay ok=true stepsCompared=100000

=== 4. Fault injection: tamper one trace entry, replay pinpoints it ===
replay ok=false divergence={"step":501,"expectedHash":1610127845,"actualHash":1610127642,...}

=== 5. Safe out-of-bounds protection on truncated fields ===
cut=1: DecodeError: truncated varint: need 1 byte(s), only 0 remaining (byte offset: 1)
cut=3: DecodeError: truncated nested message: declared 40 byte(s), only 1 remaining (byte offset: 2)
cut=2957290: DecodeError: truncated nested message: declared 493763 byte(s), only 493762 remaining ...

=== 6. Reflection validation engine ===
violation: TraceFile.meta.seed: value -5 out of range [0, 18446744073709551615] for uint64
violation: TraceFile.meta.name: expected string, got number
violation: TraceFile.entries[0].hash: value 1099511627776 out of range [0, 4294967295] for uint32
```

## 测试结果（`npm test`）

```
✔ varint encoding (7-bit compression)          — 边界字节序列 / 10 字节 int64 / 溢出与截断拒绝
✔ zigzag signed conversion                     — 规范映射表 / 全 int64 范围往返
✔ two's-complement reinterpretation
✔ fixed32 / fixed64                            — 小端字节序 / 极值往返 / 有符号读取
✔ length-delimited and nesting                 — 子读取器边界 / 超限长度拒绝 / 嵌套深度上限
✔ tags, skipping and malformed input           — 未知字段跳过 / 非法 tag / 组类型拒绝
✔ reflection decode: multi-level nesting + packed repeated
✔ safe out-of-bounds protection on truncation  — 逐字节截断前缀全部安全报错
✔ reflection validation engine                 — 范围 / 类型 / repeated 形状 / 嵌套索引路径
✔ scheduler ordering                           — 同时刻 FIFO / 随机负载堆序
✔ deterministic regression                     — 100 万步黄金哈希 (seed42=1112399500, seed43=3762455364)
✔ bug reproduction via record & replay         — 重放验证 / 故障注入精确定位分歧步
✔ concurrency safety                           — 异步交错运行位级一致 / 重入防护
✔ strict no-leak assertions                    — dispose 清零 / 监听器移除 / 堆内存有界

ℹ tests 60
ℹ pass 60
ℹ fail 0
```

## 设计要点

**Wire-format 解析器**
- Varint：每字节 7 位有效载荷，最高 10 字节；第 10 字节 > 1 即报 64 位溢出；负数按 protobuf int64 语义编码为 10 字节补码
- ZigZag：`(n << 1) ^ (n >> 63)`，小绝对值负数保持短编码
- 每次读取前校验剩余长度；截断抛 `DecodeError`，携带点分字段路径与字节偏移，绝不越界
- 嵌套消息通过受限子读取器解析，父游标预先整体跳过，畸形嵌套负载无法破坏父帧结构；嵌套深度上限 100 层
- Packed repeated：数值数组编码为单个 length-delimited 块；解码端同时兼容 packed 与 unpacked 两种线上形态

**确定性模拟与 BUG 复现**
- 离散时间 + (time, seq) 堆序事件队列，同时刻事件严格按调度先后触发
- 全部随机性来自可快照的 mulberry32；无墙钟、无 `Math.random`
- 每步记录 `(step, time, type, fingerprint)` 轨迹；指纹混合时间、步数、PRNG 状态、队列长度与用户状态哈希
- 轨迹本身用上述 protobuf 编解码器持久化（嵌套 Meta/Entry 消息 + packed checksums），形成端到端闭环
- 重放器逐步比对指纹，报告首个分歧步的期望/实际哈希，精确定位 BUG 引入点
- 100 万步回归约 1.6 s，黄金哈希锁定行为；异步交错并发运行与顺序运行结果位级一致
