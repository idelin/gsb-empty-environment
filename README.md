# Clipboard消毒往返稳定

攻击者构造的剪贴板HTML清理后再解析，会变成可执行结构。为TypeScript编辑器实现可导入的粘贴消毒组件，输入已解析节点树：只留白名单元素与属性，URL仅允许http、https、mailto；遇svg、math命名空间切换、foreignObject或style一律移除，序列化再解析须得同一棵树，否则整段丢弃。用命名空间混淆和往返不稳定样例测试。

## 设计

核心消毒逻辑只依赖一个最小 `DomHost` 接口（`src/types.ts`），不绑定任何
具体 DOM 库：

- `src/sanitizer.ts`：白名单过滤 + 序列化/再解析往返校验。
- `src/whitelist.ts`：元素、属性白名单与 URL scheme 校验。
- `src/browser.ts`：浏览器原生 DOM 适配器（用 `<template>` 解析）。
- `test/dom.ts`：离线内存 DOM，实现 HTML5 命名空间切换、foreignObject
  HTML 集成点、breakout 标签、表格 foster parenting、实体解码与序列化。

## 安全策略

- 元素白名单：仅保留承载内容语义的 HTML 元素，`script`、`iframe`、
  `object`、`embed`、`form`、`template`、`link`、`base` 等不在其中。
- 命名空间：只接受 HTML 命名空间；`svg`、`math` 及其整棵子树（含
  `foreignObject` 内 breakout 回 HTML 的元素）一律移除。
- 样式：`style` 元素与 `style` 属性都不在白名单，全部移除。
- 属性：全局仅 `title`、`lang`、`dir` 与 `data-*`，其余按元素白名单放行；
  事件处理属性（`on*`）、`class` 等默认拒绝。
- URL：`href`、`src`、`cite`、`poster` 仅允许绝对 `http:`、`https:`、
  `mailto:`；先剥离 tab/LF/CR/FF 再用 `URL` 解析，拒绝 `javascript:`、
  `data:`、协议相对与相对 URL，以及控制字符/实体混淆的 scheme。
- 往返稳定：消毒树序列化后用同一个宿主解析器再解析，做结构比较
  （命名空间、localName、属性集合、文本、子节点顺序）。任何不一致都按
  mXSS 风险处理，返回空片段并标记 `dropped`。
- 相邻文本节点在消毒时合并，避免删除注释/恶意元素后出现的 DOM 分裂
  与再解析差异。

## 用法

```ts
import { sanitizePaste, createBrowserDomHost } from './index.ts';

const host = createBrowserDomHost();
const result = sanitizePaste(clipboardFragment, host);
if (result.dropped) {
  // result.reason === 'round-trip-mismatch'：整段不可信，放弃粘贴
} else {
  editor.insert(result.fragment);
}
```

## 测试

```sh
npm test
```

覆盖 URL scheme 混淆、`svg`/`math`/`foreignObject` 命名空间切换、前缀
伪造标签、注释剥离、表格 foster parenting、实体与非 ASCII 往返，以及多组
历史 mXSS 载荷。运行时使用 Node 内置 test runner（`--experimental-strip-types`），
无需第三方依赖。
