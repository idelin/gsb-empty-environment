# wirelock

零外部依赖的 TypeScript 组件库：

1. **Protobuf 二进制 Wire-format 编解码器** — 纯手工解析 Varint（7 位压缩）、ZigZag 有符号整数、Fixed32/64、嵌套 Length-delimited 消息与 Packed Repeated 数组。
2. **反射模式校验引擎** — 以 `MessageSchema` 字段描述符驱动编码/解码/类型校验，未知字段安全跳过，错误携带字段路径。
3. **高性能无锁并发队列** — 基于 Vyukov 有界 MPMC 算法，全部协调通过 `Atomics.compareExchange`（CAS）与每槽位序列号完成，无全局互斥锁；底层为 `SharedArrayBuffer`，可跨 `worker_threads` 共享。

## 环境要求

- Node.js **>= 23.6**（内置 TypeScript type-stripping 与 `node:test`，零编译、零依赖）。
- 本仓库在 Node v23.9.0 上验证通过（`nvm use 23`）。

## 目录结构

- `src/encoding.ts` — Varint / ZigZag 编解码与 64 位溢出防护
- `src/reader.ts` — 带边界检查的 Wire-format 读取器（截断即抛 `ProtobufDecodeError`）
- `src/writer.ts` — 自动扩容的 Wire-format 写入器
- `src/schema.ts` — 反射式 Schema 校验引擎（`MessageSchema.encode/decode`）
- `src/queue.ts` — `AtomicMPMCQueue`（无锁 MPMC）、`LockFreeQueue<T>`（泛型无泄漏封装）、`MutexQueue`（全局锁基线）
- `test/` — Node.js 内置测试（39 个用例）
- `examples/demo.ts` — 运行样例
- `bench/throughput.ts` — 4 生产者 x 4 消费者跨 worker 吞吐对比

## 运行

```bash
node --test test/*.test.ts   # 全部测试
node examples/demo.ts        # 编解码 + 截断防护 + 队列样例
node bench/throughput.ts     # 无锁 vs 全局锁吞吐对比
```

## 设计要点

- **Varint**：最多 10 字节，第 10 字节大于 `0x01` 即判定 64 位溢出；遇到缓冲区结尾未出现终止字节即报截断。
- **安全越界防护**：所有读取先校验剩余字节，长度前缀超过剩余缓冲即拒绝，绝不越界访问；截断错误携带偏移量与字段路径。
- **无锁队列**：入队/出队位置与槽位序列号均为原子操作；满/空通过序列号差值判定，无需任何互斥锁。容量须为 >= 2 的 2 的幂（Vyukov 算法约束）。
- **严格无泄漏**：泛型队列出队后立即清空对象池槽位，`countLiveSlots()` 可在测试中断言零残留。
