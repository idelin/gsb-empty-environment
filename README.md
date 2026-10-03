# proto-wire-codec

零外部依赖的 Protocol Buffers 二进制 Wire-format 编解码器（TypeScript 版）。
纯手工解析，Node.js 内置 `node:test` 验证，无需任何 npm 依赖与构建步骤
（Node ≥ 22.6 原生类型擦除直接运行 `.ts`）。

## 能力

- **Varint**：LEB128 7 位压缩编码，64 位 / 10 字节上限，超长与截断防护
- **ZigZag**：有符号整数 `sint32` / `sint64` 映射（bigint 精确到 64 位极值）
- **Fixed32/64**：小端定长编码，含 `sfixed*`、`float`、`double`
- **Length-delimited**：`string` / `bytes` / 嵌套消息，长度前缀先验边界后读
- **Packed Repeated**：数值数组紧凑打包；解码同时兼容 packed 与 unpacked
- **反射模式校验引擎**：按 Schema 递归校验类型、整数范围、嵌套路径，编码前拦截
- **向前/向后兼容**：未知字段按 wire type 安全跳过；缺失字段回填 Schema 默认值
- **零拷贝还原**：`bytes` 与嵌套消息均为源缓冲区 `subarray` 视图，可架在
  内存映射（mmap 风格）窗口上解析，全程不复制、不改写输入
- **边界防溢出**：截断 → `TruncatedError`；varint 超 64 位 → `OverflowError`；
  非法 wire type / 字段号 → `InvalidWireTypeError` / `OverflowError`，均携带字节偏移

## 运行

```sh
node example/demo.ts              # 运行样例：嵌套消息 + packed 数组 + 截断防护演示
node --test 'test/*.test.ts'      # 41 个边界/兼容性/并发/零拷贝测试
```

## 用法

```ts
import { encode, decode } from './src/index.ts';
import type { Schema } from './src/index.ts';

const Point: Schema = [
  { no: 1, name: 'x', type: 'sint32' },
  { no: 2, name: 'y', type: 'sint32' },
];
const bytes = encode(Point, { x: -1, y: 2 });   // 反射校验 + 紧凑编码
const point = decode(Point, bytes);             // 零拷贝还原
```

## 结构

- `src/writer.ts` — 紧凑二进制编码器（精确长度输出，无容量泄漏）
- `src/reader.ts` — 边界安全的零拷贝读取游标（单实例无共享状态，并发安全）
- `src/schema.ts` — 反射 Schema 校验 / 编码 / 解码引擎
- `src/errors.ts` — 分级错误体系（统一基类 `ProtoCodecError`）
- `test/` — varint 边界、ZigZag 极值、嵌套/兼容、截断防护、零拷贝与并发
