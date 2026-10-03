# protobuf-wire-codec

零外部依赖的 Protocol Buffers 二进制 Wire-format 编解码器 + 反射模式校验引擎(TypeScript)。
纯手工解析,不依赖任何第三方包;测试使用 Node.js 内置 `node:test` 运行器。

## 特性

- **Varint 7 位压缩编码**:完整 64 位支持(BigInt),10 字节上限,第 10 字节溢出检测
- **ZigZag 有符号整数转换**:32/64 位编解码
- **Fixed32 / Fixed64**:小端定长编码,含 float/double
- **Length-delimited**:string(UTF-8 严格校验)、bytes、嵌套消息、Packed Repeated 数组
- **零拷贝解析**:`bytes` 字段与嵌套子消息直接返回源缓冲的 `subarray` 视图,可直映文件缓冲(内存映射风格)
- **向前/向后兼容**:未知字段按 wire type 安全跳过(含废弃的 group),缺失字段填充默认值
- **边界防溢出**:截断、越界长度前缀、varint 溢出、非法 tag、递归深度限制,全部抛出带错误码的 `ProtobufDecodeError`
- **反射模式校验**:`defineSchema` 在定义期校验字段号合法性/重复、packed 适用性、嵌套 schema 完整性

## 运行

需要 Node.js ≥ 22.6(使用内置 type-stripping 直接运行 TypeScript,无需编译、无需安装依赖)。

```bash
npm test     # 运行全部 53 个测试(node:test)
npm run demo # 运行编解码 + 持久化 + 零拷贝还原示例
```

## 用法

```ts
import { defineSchema, encodeMessage, decodeMessage } from './src/index.ts';

const phoneSchema = defineSchema('PhoneNumber', [
  { name: 'number', number: 1, kind: 'string', required: true },
  { name: 'type', number: 2, kind: 'int32', defaultValue: 0 },
]);

const personSchema = defineSchema('Person', [
  { name: 'name', number: 1, kind: 'string', required: true },
  { name: 'id', number: 2, kind: 'int32' },
  { name: 'phones', number: 4, kind: 'message', repeated: true, message: phoneSchema },
  { name: 'scores', number: 5, kind: 'sint32', repeated: true, packed: true },
]);

const bytes = encodeMessage(personSchema, {
  name: 'Alice', id: 123,
  phones: [{ number: '555-1234', type: 1 }],
  scores: [0, -1, 300],
});

const decoded = decodeMessage(personSchema, bytes); // 零拷贝还原
```

## 错误码

| 代码 | 含义 |
| --- | --- |
| `TRUNCATED` | 缓冲区在 varint / 定长字段 / group 中途被截断 |
| `OUT_OF_BOUNDS` | length-delimited 长度前缀声明超出剩余字节 |
| `VARINT_OVERFLOW` | varint 超过 10 字节或第 10 字节 > 1 |
| `INVALID_TAG` / `INVALID_WIRE_TYPE` | 字段号 0、保留 wire type 6/7、group 不匹配 |
| `RECURSION_LIMIT` | 嵌套消息/group 超过深度上限(默认 64,可配置) |
| `INVALID_UTF8` | string 字段含非法 UTF-8 |
| `SCHEMA_MISMATCH` | 线上 wire type 与 schema 期望不符 / schema 定义非法 |
| `MISSING_REQUIRED` | required 字段缺失 |

## 测试覆盖(53 项,全部通过)

- `test/varint.test.ts`:golden 向量、5000+ 随机 64 位往返、10 万 ZigZag 往返、截断/溢出
- `test/reader.test.ts`:定长小端向量、零拷贝视图、越界长度前缀、UTF-8、tag 校验、group 跳过
- `test/message.test.ts`:三层嵌套 + packed repeated 精确往返、golden bytes、递归 schema、深度限制、schema 校验
- `test/compat.test.ts`:旧读新(跳过未知字段)/新读旧(默认值)、group 兼容、乱序字段、重复字段 last-wins
- `test/truncation.test.ts`:逐字节截断全部前缀 + 4 万次变异/垃圾 fuzz,断言只抛 `ProtobufDecodeError`
- `test/persistence.test.ts`:落盘 → 文件缓冲零拷贝还原、长度前缀帧日志追加读取
- `test/concurrency.test.ts`:512 路并发异步解码共享缓冲、256 路交错编解码、4 个 worker_threads 并行解码校验和一致
- `test/leak.test.ts`:5 万次编解码后堆增长 < 8MB(`--expose-gc`)、输入缓冲完整性、零拷贝视图边界
