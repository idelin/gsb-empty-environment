# SVG 脚本净化

头像上传后的 SVG 会内联到页面，攻击者可放 script、foreignObject、事件属性、混入空白或实体的 javascript: 链接，以及指向 data: 的 use 引用。为 TypeScript 上传处理器写 SVG 净化包：只保留允许的元素与属性，解码实体后再判断 URL 协议，样式里的 url() 和 @import 按外部引用拒绝；净化结果再次净化应不变。用常见 XSS 向量、大小写和命名空间变形及正常图标验证输出。

## 使用

```ts
import { sanitizeSvg, SvgSanitizationError } from "./index.ts";

try {
  const safeSvg = sanitizeSvg(untrustedUpload);
} catch (error) {
  if (error instanceof SvgSanitizationError) {
    // 输入不是合法的单一 <svg> 文档，拒绝上传。
  }
}
```

`sanitizeSvg` 返回可安全内联的 SVG 字符串，并且满足
`sanitizeSvg(sanitizeSvg(input)) === sanitizeSvg(input)`。

## 净化规则

- 仅保留图标所需的 SVG 元素和属性；未知元素连同子树删除。
- 元素和属性名按不区分大小写匹配，输出统一为规范的 SVG 名称。命名空间前缀不会绕过白名单，根元素始终输出 SVG 命名空间。
- 删除 `on*` 事件属性；`href` / `xlink:href` 只接受本地 `#fragment`，因此 `javascript:`、`data:`、空白混入和实体编码的协议都会失效。
- `style` 属性只保留安全声明；包含 `url()` 或 `@import`（包括 CSS 注释和反斜杠转义变形）的声明会被拒绝。
- 演示属性中的 `url(...)` 仅允许本地 `#fragment`，例如 `fill="url(#gradient)"`。
- `<style>`、`<script>`、`<foreignObject>`、`<image>`、`<feImage>`、动画元素等不会进入输出。
- 输出会转义文本和属性值、移除非法 XML 字符，并拒绝畸形文档、过大文档和多个根元素。

## 测试

```sh
npm test
```

测试包含正常图标、大小写与命名空间变形、实体/空白注入、事件属性、
`data:`/`javascript:` 引用、CSS `url()`/`@import` 变形，以及重复净化不变性。
