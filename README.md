# protobuf-wire-lockfree

零外部依赖的 TypeScript 实现：

1. **Protocol Buffers 二进制 wire-format 编解码器 + 反射模式校验引擎**
   - Varint（7 位压缩）、ZigZag 有符号整数、Fixed32/64（小端）、Length-delimited 嵌套消息
   - Packed / 非 packed repeated 数组（解码端两种编码都接受，符合规范）
   - schema 描述符驱动的反射校验：字段类型、整数范围、repeated/packed 约束、未知字段安全跳过
   - 全程边界检查：字段截断抛出 `ProtobufTruncationError`，绝不越界读取

2. **高性能无锁并发队列与 CAS 加速**
   - `AtomicCounter`：基于 `SharedArrayBuffer` + `Atomics.compareExchange` 的硬件级 CAS，可跨 worker 线程共享
   - `LockFreeQueue`：Michael–Scott 无界 MPMC 无锁队列，仅靠 CAS 同步，无任何互斥锁
   - `MPMCRing`：Vyukov 有界 MPMC 环形队列，序列号 + CAS 光标，直接驻留共享内存供多线程使用

## 目录结构

```
src/protobuf/   varint / zigzag / wire / Reader / Writer / schema 校验引擎
src/queue/      atomic(CAS) / lockfree-queue / mpmc-ring
test/           node:test 测试（含 worker_threads 并发安全测试）
examples/       运行样例
```

## 运行要求

Node.js >= 22.6（`--experimental-strip-types`）或 >= 23.6（原生类型擦除，推荐）。无需安装任何依赖。

```bash
# 运行全部测试（30 个用例）
node --test test/*.test.ts

# 运行样例
node examples/demo.ts
```

## 测试覆盖

- varint 边界值（0/127/128/300/2^32-1/2^64-1）、截断 varint、超过 64 位的非法 varint
- zigzag32/64 全边界（含 ±2^31、±2^63）
- fixed32/64、float/double 往返与截断防护
- 三层嵌套消息 + packed repeated（sint32/sint64）往返
- 反射校验：错误类型、越界整数、非数组 repeated、wire-type 不匹配
- 截断防护：对嵌套消息逐字节截断，字段内部截断必须安全抛错
- 无锁队列：空队列、FIFO、交错操作、20 万次压力、排空后哨兵节点归零（无泄漏）
- MPMC 环：容量校验、满/空边界、环绕 50 周期 FIFO、10 万次churn
- 并发安全：4 生产者 × 4 消费者跨线程精确一次投递（10 万条）、8 线程 × 10 万次 CAS 自旋计数无丢失更新
