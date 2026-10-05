# lzarc — 自定义 LZ77 + 规范霍夫曼日志压缩

可独立解码的块格式压缩器，用于日志归档。格式与位序见 [FORMAT.md](FORMAT.md)，
**不与 DEFLATE 兼容**。

## 环境要求

- Node.js ≥ 23.6（或 ≥ 22.6 加 `--experimental-strip-types`）：直接运行 TypeScript，无需编译。
- 无第三方依赖。

## 使用

```sh
node src/cli.ts compress   <input> <output> [--window-bits N] [--block-size N]
node src/cli.ts decompress <input> <output>
node src/cli.ts verify     <input>     # 内存中往返校验 + 大小统计
```

CLI 输出输入/输出字节数、压缩比与块数。**体积下降不是正确性证明**；
正确性以 `verify` 的逐字节往返一致为准。

库 API（`src/api.ts`）：`compress(data, {windowBits, blockSize})`、
`decompress(data, {maxBlockSize})`。流式解码用 `src/decoder.ts` 的
`StreamDecoder`：`push(chunk)` 喂入任意字节边界的输入分块，
`read(maxBytes)` 按调用方额度取输出（暂停后不会重复输出），
`finish()` 校验流在块边界结束，块损坏抛 `BlockError` 后可 `recover()`
跳过坏块继续下一块。

## 测试

```sh
node --test test/*.test.ts
```

覆盖：小串穷举往返（三进制字母表长度 ≤ 6 全部 1093 串、二进制字母表长度 ≤ 8
全部 511 串，均同时走主解码器与不使用压缩器匹配逻辑的 token 参考展开器）、
距离 1 长重复（重叠复制）、空块、单符号、逐字节截断、声明长度溢出、
过度订阅码表、非法距离、展开超限、CRC 损坏、损坏块不污染下一独立块、
任意分块输入与输出额度限制。

## 实现说明

- 压缩器（`src/lz77.ts`）为贪心哈希链匹配，仅压缩侧使用；解码与参考展开器
  （`src/reference.ts`）不依赖其匹配逻辑。
- 霍夫曼码长限制 15 位（zlib 式溢出调整），规范码按 (码长, 符号) 排序赋值。
- 每块固定头 20 字节 + 码长表（主表 ≤ 265 字节、距离表 ≤ 21 字节），
  小块开销占比高；日志归档建议 1 MiB 块（默认）。
