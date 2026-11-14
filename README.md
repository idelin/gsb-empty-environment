# Markdown 链接净化组件

无外部依赖、可由 TypeScript 文档渲染服务直接导入。Markdown 先解析为 AST，链接/图片地址在写入 AST 前净化；原始 HTML 也走同一套标签与属性白名单。

```ts
import sanitizeMarkdown, {
  sanitizeMarkdown as render,
  sanitizeHtml,
  sanitizeUrl,
  parseMarkdown,
} from "./index.ts";

const html = sanitizeMarkdown(userMarkdown);
```

- `sanitizeMarkdown(markdown, options?)` / default export：解析、净化并输出 HTML。
- `parseMarkdown(markdown, options?)`：返回已净化链接目的地的 Markdown AST。
- `sanitizeHtml(html, options?)`：HTML 语法树白名单净化。
- `sanitizeUrl(value, options?)`：返回安全 URL，拒绝时返回 `null`。
- `ALLOWED_TAGS`、`ALLOWED_ATTRIBUTES`：当前声明允许的标签与属性。

URL 判定先按 HTML 实体、UTF-8/百分号与 WHATWG 浏览器空白规则反复规范化，再检查最终协议。仅允许 `http`、`https` 与相对路径；`javascript`、`data`、`vbscript`、其他显式协议以及控制字符/不可见字符变体一律拒绝。注释、声明、畸形标签、`script/style/svg/math/iframe` 等子树会被丢弃或转义，事件属性、`style`、`srcset` 等未声明属性不会输出。

运行测试：

```sh
npm test
```
