# proto-wire-engine

零外部依赖的 Protocol Buffers 二进制 Wire-format 编解码器 + 反射模式校验引擎，
面向高频调用路径做了 GC 停顿消除（内存复用 + 对象池化）。纯 TypeScript，
由 Node.js (>= 22.6, 推荐 23+) 原生类型擦除直接运行，无需编译步骤、无需任何 npm 依赖。

## 能力

- **Varint**：7 位压缩编码/解码，number 快路径（安全至 2^53-1）+ bigint 全 64 位路径
- **ZigZag**：有符号 int32/int64 与无符号 varint 的互转
- **Fixed32/Fixed64**：小端定长编解码
- **Length-delimited**：字符串、字节、嵌套子消息（`enterSub/leaveSub` 零分配递归）
- **Packed Repeated**：packed varint / zigzag / fixed32 / fixed64 数组
- **反射校验引擎**：声明式 `MessageSchema` 驱动解码，逐字段校验 wire type，
  未知字段安全跳过
- **安全越界防护**：每次读取前做边界检查，字段截断抛出 `TruncatedFieldError`，
  绝不越界访问内存
- **对象池** `ObjectPool`：acquire/release 复用实例，精确 outstanding 计数、
  双重释放检测、严格无泄漏断言
- **零分配缓存** `ZeroAllocCache`：定容开放寻址哈希，构造后热路径零堆分配

## 运行

```bash
npm run demo   # 运行样例：多层嵌套 + packed 数组 编码→解码→10万次池化解码→截断防护
npm test       # Node.js 内置测试运行器，55 个用例
```

## 样例（examples/demo.ts）

```ts
import { WireEngine } from "../src/engine.ts";
import type { MessageSchema } from "../src/schema.ts";

const Outer: MessageSchema = {
  name: "Outer",
  fields: [
    { field: 1, name: "id", kind: "varint" },
    { field: 2, name: "mid", kind: "message", schema: Middle },
    { field: 3, name: "ratios", kind: "packed-fixed32" },
    { field: 4, name: "blob", kind: "bytes" },
  ],
};

const engine = new WireEngine();
const msg = engine.decode(bytes, Outer);        // 池化 reader，自动归还
engine.decode(bytes, Outer, msg);               // 复用同一对象图：稳态零分配
```

## 目录

- `src/varint.ts` — varint / zigzag 原语
- `src/reader.ts` — 边界安全读取游标（零拷贝 subarray 视图）
- `src/writer.ts` — 可复用增长缓冲写出器
- `src/schema.ts` — 反射 schema 校验/解码引擎
- `src/pool.ts` — 对象池（泄漏/双释放检测）
- `src/cache.ts` — 定容零分配缓存
- `src/engine.ts` — 池 + 缓存 + 解码器组合门面
- `test/` — Node.js 内置 `node:test` 测试（边界、截断、并发、无泄漏断言）
