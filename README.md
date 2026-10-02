# wire-sim

零外部依赖的 TypeScript 工具集（Node.js ≥ 23.6，原生 TypeScript 类型擦除运行，无需编译）：

1. **二进制紧凑变长编解码器** — 纯手工实现的 Protocol Buffers Wire-format 解析器/编码器
2. **反射模式校验引擎** — 基于消息描述符（schema descriptor）的驱动式解码与校验
3. **离散时间事件驱动模拟框架** — 确定性调度、百万步回归、故障复现（trace diff 定位）

## 运行

```bash
npm test          # node --test（Node 内置测试运行器，67 个测试）
npm run demo      # 端到端样例：编解码 → 截断防护 → 百万步模拟 → 故障复现
```

## 结构

```
src/
  wire/    varint.ts(7位压缩/ZigZag) reader.ts(越界防护读取器) writer.ts errors.ts
  proto/   schema.ts(反射描述符) decoder.ts(校验引擎) encoder.ts
  sim/     rng.ts(SplitMix64) heap.ts(二叉堆) simulator.ts trace.ts(录制/复现) workload.ts
test/      67 个测试：边界、截断模糊测试、确定性回归、并发与泄漏
fixtures/  共享 schema 与 worker_threads 工作脚本
examples/  demo.ts 端到端样例
```

## Wire-format 支持

- Varint（7 位压缩，64 位 BigInt，溢出/未终止检测）
- ZigZag 有符号整数（sint32/sint64）
- Fixed32/Fixed64、sfixed、float/double（小端）
- Length-delimited：string（UTF-8 严格校验）、bytes、嵌套消息
- Packed / 非 Packed repeated 标量（两种线上编码均可解码）
- 未知字段安全跳过（含废弃 group），可配置 `unknownFields: 'error'`
- 安全防护：每次读取均做边界检查；截断抛 `WireError{code:'TRUNCATED', path, offset}`；
  递归深度限制（默认 100）抵御恶意嵌套；tag/wire-type 合法性校验

## 确定性模拟

- 事件排序键 `(time, priority, seq)`，二叉堆调度，同种子严格同序
- `TraceRecorder` 逐事件 SHA-256 校验和；`diffTraces` 定位首个分叉事件（故障复现）
- 百万步回归黄金校验和：`31a44fad…ae7ab01`（seed=42, 1,000,000 arrivals）

## 测试结果（本机）

```
ℹ tests 67
ℹ pass 67
ℹ fail 0
ℹ duration_ms ~8000
```

覆盖：varint/zigzag 全部边界值（含 2^64-1、int64 最小值）、每个截断点的安全性模糊测试、
多层嵌套 + packed repeated 精确还原、UTF-8/递归/线型校验、百万步确定性回归、
worker_threads 并行解码一致性、句柄/监听器/堆内存无泄漏断言。
