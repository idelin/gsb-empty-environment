# stream-escape-codec

流式转义编解码库（TypeScript）。协议文本由三类元素组成：

| 元素 | 语法 | 说明 |
| --- | --- | --- |
| 简单转义 | `\\` `\n` `\r` `\t` `\0` | 单字节 |
| 十六进制转义 | `\xHH` | 任意字节，大小写均可 |
| 长度前缀原始块 | `\R<len>:<len 字节>` | 块内反斜线**无**特殊含义；`len` 为十进制，上限 `maxRawLength`（默认 65535） |

## 特性

- **流式解码**：输入可按任意字节边界切块喂入（`push`），`end()` 收尾。
- **输入范围保留**：每个输出字节都携带其对应的绝对输入区间 `[start, end)`。
- **非法转义不吞字符**：非法序列按字面量输出，后续合法字符照常解码；
  流末尾的截断序列（含截断的原始块）也由 `end()` 按字面量冲刷。
- **UTF-8 只在解码后校验**：`Utf8StringDecoder` 对解码后的字节流做
  fatal UTF-8 校验，多字节序列可跨块、可由 `\xHH` 拼出。
- **逆编码**：`encode` 支持转义模式（ASCII 安全，默认）与原始块模式。

## API

```ts
import { EscapeDecoder, Utf8StringDecoder, decode, decodeToBytes, encode } from "./src/codec";

const dec = new EscapeDecoder({ maxRawLength: 1024 });
for (const chunk of chunks) {
  for (const { byte, start, end } of dec.push(chunk)) { /* ... */ }
}
dec.end(); // 冲刷截断序列（字面量）

encode(bytes);                          // 转义模式
encode(bytes, { useRawBlocks: true });  // 原始块模式
decodeToBytes(encoded);                 // 一次性解码
```

## 构建与测试

```sh
npm run build   # tsc -p tsconfig.json
npm test        # 编译并运行 test/codec.test.ts
```
