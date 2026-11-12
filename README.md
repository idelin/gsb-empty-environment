# Thrift 严格解码包（TypeScript）

RPC 网关在转发前需要按 Thrift Binary 协议解析请求、读取租户字段做鉴权。攻击者可以用
错误的线上类型、重复字段、夸大的容器声明长度或深层嵌套，让“宽松解析”的网关与后端对
同一串字节产生不同理解，从而绕过租户校验。本包提供**全有或全无（all-or-nothing）**的
严格解码：任何违规都整体拒绝，绝不跳过字段或猜测类型；校验通过后输出一份确定的规范
重编码字节，网关只转发这份字节。

零运行时依赖，直接用 Node 内置的 TypeScript 类型擦除运行。

## 安全保证

- **线上类型一致**：调用方给出字段表（`Map<fieldId, FieldSpec>`），已知字段的线上类型
  与表中声明不一致时抛 `FIELD_TYPE_MISMATCH`，整体拒绝而不是跳过；容器元素、map 键值、
  嵌套结构体字段递归套用同样的规则。
- **重复字段拒绝**：同一结构体内同一 field id 出现两次即抛 `DUPLICATE_FIELD`，避免
  “网关取第一个、后端取最后一个”之类的解析分叉。
- **容器长度预校验**：`list`/`set`/`map` 的声明长度按“剩余字节 ÷ 最小元素尺寸”在分配
  任何数组前校验；夸大长度抛 `CONTAINER_LENGTH_TOO_LARGE`，负长度抛 `INVALID_LENGTH`。
  字符串/二进制的负长度同样拒绝。
- **嵌套深度上限**：结构体与容器统一计入深度（根结构体为深度 1，默认上限 16），
  超限抛 `NESTED_TOO_DEEP`，未知字段内部的嵌套同样受限。
- **未知字段字节上限**：不在字段表中的字段按其整段原始字节计费（默认 64 KiB），
  超限抛 `UNKNOWN_BYTES_EXCEEDED`；未超限的未知字段会被保留并随规范字节转发。
- **只接受 strict binary envelope**：消息必须以版本前缀 `0x80010000` 开头，旧版
  非 strict 格式直接拒绝；STOP 之后存在尾随字节也拒绝。
- **规范重编码**：字段按 id 升序输出，bool 归一化为 `0/1`，未知字段原样保留，
  对同一份合法输入重复解码/编码得到字节完全一致的结果（幂等）。

## 快速使用

```ts
import { ThriftType, decodeMessage, getRequiredField } from './index.ts';

const fieldTable = new Map([
  [1, { type: ThriftType.I32 }],                       // tenantId
  [2, { type: ThriftType.STRING }],
  [3, { type: ThriftType.LIST, element: { type: ThriftType.I64 } }],
  [4, {
    type: ThriftType.STRUCT,
    fields: new Map([[1, { type: ThriftType.STRING }]]),
  }],
]);

const decoded = decodeMessage(rawBytes, fieldTable, {
  maxDepth: 16,
  maxUnknownBytes: 64 * 1024,
  maxMessageBytes: 16 * 1024 * 1024,
});

const tenant = getRequiredField(decoded.root, 1);      // 缺失或未知会抛错
backendSocket.write(decoded.canonical);                // 只转发规范字节
```

网关已自行拆包、只有结构体载荷时用 `decodeStructBytes(bytes, table, options)`，
返回的 `canonical` 不含消息 envelope。

## 错误码

所有失败都抛 `ThriftDecodeError`，带 `code` 与出错 `offset`：

| code | 触发场景 |
| --- | --- |
| `MESSAGE_TOO_LARGE` | 整条消息超过 `maxMessageBytes` |
| `INVALID_MESSAGE` | 非 strict envelope、消息类型非法、解码参数非法 |
| `TRUNCATED_INPUT` | 字节在声明的数据结束前被截断 |
| `TRAILING_BYTES` | 根结构体 STOP 后仍有字节 |
| `INVALID_TYPE` | 出现未定义的线上类型，或 bool 值不是 0/1 |
| `INVALID_FIELD_ID` | 字段 id 非正整数 |
| `FIELD_TYPE_MISMATCH` | 已知字段/元素的线上类型与字段表不符 |
| `DUPLICATE_FIELD` | 同一结构体里 field id 重复 |
| `INVALID_LENGTH` | 字符串/容器长度为负 |
| `CONTAINER_LENGTH_TOO_LARGE` | 声明长度超过剩余字节可容纳的最小元素数 |
| `NESTED_TOO_DEEP` | 嵌套深度超过 `maxDepth` |
| `UNKNOWN_BYTES_EXCEEDED` | 未知字段原始字节总和超过 `maxUnknownBytes` |
| `INVALID_ENCODING` | 方法名或字符串不是合法 UTF-8（仅文本解码场景） |

## 目录

- `src/protocol.ts`：线上类型常量、带边界检查的字节读写器
- `src/types.ts`：规范值树、字段表与解码选项类型
- `src/decoder.ts`：严格解码器（所有安全检查集中在此）
- `src/encoder.ts`：规范重编码器
- `src/index.ts`：公共出口与取字段辅助函数
- `examples/gateway-usage.ts`：网关鉴权 + 转发接入示例
- `test/strict-decoder.test.ts`：攻击与边界测试

## 测试

```sh
npm test
```

覆盖四类攻击的手工构造字节：tenant 被写成字符串、重复 tenant、负/夸大容器长度、
嵌套炸弹，另含截断输入、尾随字节、非 strict 消息、非法 bool、未知字节上限等用例。
