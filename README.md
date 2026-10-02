# compact-proto-codec

零外部依赖的 Protocol Buffers 二进制 Wire-format 编解码器 + 反射模式校验引擎 + 海量稀疏数据内存紧凑优化器（TypeScript / Node.js 原生运行）。

## 组件

| 模块 | 职责 |
| --- | --- |
| `src/varint.ts` | Varint 7 位压缩编解码（64 位 BigInt 安全）、ZigZag 有符号转换 |
| `src/wire.ts` | 越界安全的 `Reader` / 自增长 `Writer`，Fixed32/64、Length-delimited、嵌套子消息 |
| `src/schema.ts` | 反射字段描述（标量 / message / repeated / packed / required） |
| `src/codec-core.ts` | 反射引擎：编码、解码、校验；packed/unpacked 双向兼容；未知字段透传 |
| `src/sparse.ts` | 稀疏优化器：`SparseVector` 稀疏 Map ↔ 稠密数组自适应转换、RLE 压缩、delta+zigzag+varint 打包 |
| `src/codec.ts` | 门面 API `ProtoCodec` |

## 安全边界

- 所有读取先校验剩余长度，截断输入抛出带字段路径的 `TruncationError`，绝不越界访问。
- 超过 10 字节 / 第 10 字节溢出的 varint 抛出 `MalformedError`。
- 解码产物的 `bytes` 字段是源缓冲区的拷贝，无共享引用（无泄漏/串改）。

## 运行

需要 Node.js ≥ 22.18（原生 TypeScript 类型擦除 + `node:test`）。

```bash
npm test     # node --test test/
npm run demo # node examples/demo.ts
```
