# SSR流式脚本数据转义

服务端流式渲染把用户字符串写入内联JSON脚本，分块边界会拆开危险序列。为TypeScript SSR框架实现可导入的流式转义器：对</script、<!--、<script及U+2028、U+2029按跨块状态机转义，解析结果仍是原JSON值；块可在任意字节处切开，包括UTF-8多字节中间，不得提前释放待判定的尾部。用逐字节切分与一次性转义对照测试。

## 用法

```ts
import {
  StreamingScriptJsonEscaper,
  escapeInlineScriptJson,
} from "./index.ts";

// 一次性转义
const safe = escapeInlineScriptJson(JSON.stringify(value));

// 流式转义：块可在任意字节处切开（含 UTF-8 多字节中间）
const escaper = new StreamingScriptJsonEscaper();
for await (const chunk of byteStream) {
  res.write(escaper.push(chunk)); // chunk: Uint8Array | string
}
res.write(escaper.end());
```

转义规则：`</script`、`<script`、`<!--`（大小写不敏感）的 `<` 变为 `<`，U+2028/U+2029 变为 ` `/` `；输出经 `JSON.parse` 仍还原原值。状态机在危险前缀被判定前不会释放尾部字节。

## 测试

```sh
npm test
```
