# stream-escape-codec

流式转义编解码库（TypeScript）。协议文本由可打印字符、反斜线转义、
十六进制转义和长度前缀原始块组成；解码器支持逐字节到达的输入，
并为每个输出字节保留其对应的输入范围 `[start, end)`。

## 协议格式

```
protocol := *( literal / escape )
literal  := 任何非反斜线字节                    ; 原样通过
escape   := "\\"                               ; 字面反斜线 (0x5C)
          | "\x" 2HEXDIG                       ; 任意字节（十六进制大小写均可）
          | "\r" 1*DIGIT ":" <len 个原始字节>   ; 长度前缀原始块
```

- **原始块**：`\r<len>:` 之后恰好 `len` 个字节逐字拷贝，块内反斜线
  **没有**特殊含义（`\r4:\x41` 解码为 4 个字节 `\x41`，而非 `A`）。
- **长度上限**：`len` 默认最大 65535（`maxRawLen` 可配）。超限、无数字、
  缺少冒号、数字中出现非数字字符，都视为**非法转义**。
- **非法转义不吞字符**：遇到非法转义时，解码器原样输出 `\`，并从下一个
  字节重新解析。例如 `\q\x41` → `\qA`，`\xZZ\x41` → `\xZZA`。
- **截断**：流结束（`end()`）时仍未完整的转义序列原样输出，不丢字节。
  例如 `\r5:ab` → `\r5:ab`。
- **UTF-8 只在解码后校验**：解码器按字节工作，多字节 UTF-8 序列可以
  由多个转义/原始块拼成；`decodeUtf8` 在解码完成后才对结果做严格校验。

## API

```ts
import {
  StreamDecoder, decode, decodeBytes, decodeUtf8,
  encode, encodeUtf8, DEFAULT_MAX_RAW_LEN,
} from './src/codec.ts';
```

- `new StreamDecoder({ maxRawLen? })` — 流式解码器
  - `push(data: Uint8Array): ByteSpan[]` — 喂入一块输入，返回已解码的 span
  - `end(): ByteSpan[]` — 结束输入；残留的不完整转义原样输出
- `decode(data, options?): ByteSpan[]` — 一次性解码（保留输入范围）
- `decodeBytes(data, options?): Uint8Array` — 一次性解码为字节
- `decodeUtf8(data, options?): string` — 解码后严格校验 UTF-8，非法则抛错
- `encode(data, { rawThreshold? }): Uint8Array` — 逆编码；
  保证 `decode(encode(x)) == x`。`rawThreshold`（默认 4）控制多长的
  不可打印字节串改用 `\r<len>:` 原始块；传 `Infinity` 则只用 `\xHH`
- `encodeUtf8(text, options?): Uint8Array` — 字符串 → UTF-8 协议文本

`ByteSpan = { byte, start, end }`：`byte` 为输出字节，`[start, end)` 为
产生该字节的输入区间（跨 `push` 累计的绝对偏移）。字面/原始字节为
`[i, i+1)`；`\\` 占 2 字节；`\xHH` 占 4 字节；`\r<len>:` 头部不产生输出。

## 流式示例

```ts
const decoder = new StreamDecoder();
for await (const chunk of socket) {
  for (const span of decoder.push(chunk)) {
    console.log(span.byte, '来自输入区间', span.start, span.end);
  }
}
decoder.end();
```

## 开发

本环境无 `tsc`，`src/` 使用 TypeScript 受控子集（仅可擦除类型语法），
`npm run build` 用 `scripts/strip-types.mjs` 擦除类型生成 `build/*.js`，
`npm test` 构建并运行 `test/run.mjs`（零依赖断言框架）。有 `tsc` 的环境
可直接用 `tsconfig.json`（strict）做类型检查。

测试覆盖：全部语料在**所有两切块/三切块位置**及逐字节喂入下与一次性
解码结果（含每个 span 的输入区间）完全一致；截断长度、超限长度、非法
转义后的合法字符保留、原始块内反斜线、多字节 UTF-8 跨块拼接与解码后
校验、随机缓冲区与全字节值的编码往返。
