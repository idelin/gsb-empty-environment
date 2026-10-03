# protobuf-wire-saga

零外部依赖的 TypeScript 实现，包含两个组件：

1. **二进制紧凑变长编解码器 + 反射模式校验引擎**（`src/wire.ts`、`src/schema.ts`）
   - 纯手工解析 Protobuf 二进制 wire format，无任何生成代码 / 第三方依赖
   - Varint 7 位压缩编码（最长 10 字节 = 64 位，BigInt 精确表示）
   - ZigZag 有符号整数转换（`sint32` / `sint64`）
   - Fixed32 / Fixed64（含 `sfixed*`、`float`、`double`，小端序）
   - 嵌套 Length-delimited 消息（任意层级）
   - Packed / Unpacked Repeated 数组（解码双向兼容，proto3 互操作语义）
   - 未知字段按 wire type 安全跳过
   - 字段截断时抛出 `TruncationError`（携带 offset / needed / available / 字段路径），结构非法抛出 `MalformedWireError` —— 严格的安全越界防护

2. **反向补偿与事务回滚状态编排**（`src/saga.ts`）
   - Saga 模式：正向步骤失败时按 LIFO 逆序执行补偿
   - 补偿失败自动重试（可配置次数与退避），重试耗尽后挂起，
     通过 `resumeCompensation()` 恢复，保证最终一致性
   - 幂等：并发 `run()` 共享同一在执行途中的事务；成功后重复调用返回缓存结果；
     每个步骤每次尝试至多执行一次、补偿至多一次
   - 并发安全：单实例状态机（idle → running → succeeded / compensating → rolledback / failed）
   - 支持 `AbortSignal` 在步骤间中止并触发回滚
   - 无泄漏：终态清空内部缓冲，`dispose()` 释放全部监听器

## 运行

需要 Node.js ≥ 22.6（推荐 ≥ 23.6，原生类型擦除直接运行 `.ts`）。

```bash
npm test          # Node 内置测试运行器，40 个用例
npm run example   # 编码/解码/截断防护/回滚演示
```

## 快速示例

```ts
import { encodeMessage, decodeMessage } from './src/schema.ts';

const schema = {
  name: 'Order',
  fields: {
    1: { name: 'id', type: 'uint64' },
    3: { name: 'balance', type: 'sint64' },              // ZigZag
    4: { name: 'scores', type: 'int32', repeated: true, packed: true },
    5: { name: 'address', type: 'message', message: addressSchema }, // 嵌套
  },
} as const;

const bytes = encodeMessage(schema, order);   // Uint8Array wire 字节
const back = decodeMessage(schema, bytes);    // 还原结构体字段
```

```ts
import { Saga } from './src/saga.ts';

const saga = new Saga<Ctx>();
saga
  .addStep({ name: 'debit', execute: debit, compensate: refund })
  .addStep({ name: 'credit', execute: credit, compensate: clawBack });
const status = await saga.run(ctx); // 失败时自动逆序补偿 → 'rolledback'
```
