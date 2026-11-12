# MessagePack 严格解码包

供 TypeScript 网关的 Node 处理器调用的无依赖 MessagePack 解码器。它只接受一条规范编码的消息；默认拒绝所有扩展类型，调用方必须显式登记可信的 ext 类型号。

## 使用

```ts
import { decodeStrict } from "./index.ts";

const value = decodeStrict(requestBody, {
  allowedExtTypes: [2], // 有符号 ext 类型号；-1 时间戳也要显式登记
  maxDepth: 32,
  maxElements: 100_000,
});
```

输入可以是 `Uint8Array`、Node `Buffer` 或 `ArrayBuffer`。只允许一个根值，根值之后的多余字节会以 `TRAILING_BYTES` 拒绝。

## 严格规则

- 整数使用能表示该值的最短编码。例如 `uint8(0)`、`uint16(255)`、正数 `int8`、可放入低宽度的 64 位整数均被拒绝。
- 字符串、数组、map、binary 和 ext 的长度前缀必须最短；例如长度小于 32 的 `str8`、少于 16 项的 `array16` 均被拒绝。
- map 的键必须是字符串，且按解码后的字符串判重；非字符串键和重复键均被拒绝。
- ext 类型号必须在 `allowedExtTypes` 中，支持数组或 `Set`。类型 `-1` 的载荷长度只能是 4、8 或 12 字节。
- 未知 marker、截断输入、非法 UTF-8 和尾随字节均被拒绝。
- `maxDepth` 限制数组/map 嵌套深度，标量根值深度为 0，默认 32。
- `maxElements` 统计所有解码值（含容器、map 键和 map 值），默认 100,000。
- 不超出安全整数范围的整数返回 `number`，超出时返回 `bigint`，避免静默精度丢失。
- ext 值以 `{ type, data }` 返回，`data` 是原始 `Uint8Array`。

非法输入抛出 `StrictDecodeError`，其 `code` 可用于网关审计，`offset` 指向首个问题字节。常见代码包括 `NON_CANONICAL_INTEGER`、`NON_CANONICAL_STRING`、`NON_CANONICAL_ARRAY`、`NON_CANONICAL_MAP`、`DUPLICATE_MAP_KEY`、`EXTENSION_TYPE_NOT_ALLOWED`、`INVALID_TIMESTAMP_LENGTH`、`MAX_DEPTH_EXCEEDED` 和 `MAX_ELEMENTS_EXCEEDED`。

## 测试

```sh
npm test
```
