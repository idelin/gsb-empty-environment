# iframe 沙箱标志推导

攻击者页可以同时拿到 `allow-scripts` 与 `allow-same-origin`，在沙箱框架内删除自己的
`sandbox` 属性；也可以借 `allow-popups-to-escape-sandbox` 让弹窗绕开沙箱。这个包把
嵌套 iframe 的令牌集合、继承关系和来源推导成可检查的数据，供 TypeScript 宿主在渲染或
审计 iframe 前做判定。

## 语义

- `sandbox` 是默认拒绝列表。有效能力是祖先链与当前帧的交集；子帧不能重新打开祖先已经
  关闭的能力。
- `sandbox` 属性缺失表示继承父帧；`sandbox=""` 表示显式启用全部限制。二者不能混为一谈。
- `srcdoc` 与 `about:blank` 继承父文档的有效源，而不是另一个 URL 的源。若有效标志中没有
  `allow-same-origin`，浏览器会把源强制为不透明源，推导结果记为 `"opaque"`。
- 不安全原因之一：帧处于沙箱中、与父帧同源、有效标志同时包含 `allow-scripts` 与
  `allow-same-origin`。此时脚本可以删除自身 `sandbox` 并访问父文档。
- 弹窗默认继承打开者的有效标志。只有打开者自己的 `sandbox` 属性显式包含
  `allow-popups-to-escape-sandbox`，弹窗才不再继承沙箱；该令牌不会从祖先继承。
- 不安全原因之二：打开者仍可运行脚本，同时 `allow-popups` 与显式逃逸令牌都有效，弹窗会
  脱离沙箱。`unsafeReasons` 会记录对应的原因。
- 未知令牌会被忽略并记录在 `unknownTokens` 中，与浏览器行为一致。

## 导入

```ts
import {
  deriveFrameTree,
  derivePopup,
  parseSandboxAttribute,
} from "./index.ts";
```

包通过 `index.ts` 导出全部 API，`package.json` 的 `exports`/`types` 指向同一入口。

## 主要 API

### `deriveFrameTree(root)`

递归推导宿主页面及其嵌套 iframe：

```ts
const tree = deriveFrameTree({
  origin: "https://host.example",
  children: [
    {
      src: "https://host.example/attack",
      sandbox: "allow-scripts allow-same-origin",
    },
  ],
});

const frame = tree.children[0];
frame.sameOriginAsParent; // true
frame.flags.scripts;      // true
frame.flags.sameOrigin;   // true
frame.unsafe;             // true
frame.unsafeReasons;      // ["same-origin-scripts-can-remove-sandbox"]
```

每个节点包含：

- `flags`：继承后的有效布尔标志。
- `flagResolutions`：每个标志是否有效，以及来源
  （`root` / `inherited` / `declared` / `omitted` / `blocked-ancestor`）。
- `origin`：`source`（`root` / `url` / `srcdoc` / `about-blank`）、沙箱前的 `base`、
  沙箱后的 `effective`，以及是否被强制为不透明源。
- `effectiveTokens`：继承后仍然有效的能力令牌。
- `popup`：该帧打开弹窗时，弹窗默认继承的标志、来源、是否逃逸。

### `deriveSandbox(frame, parent?)`

已知父帧源与有效令牌时，直接推导单个帧：

```ts
const child = deriveSandbox(
  { src: "https://host.example/child", sandbox: "allow-scripts allow-same-origin" },
  { origin: "https://host.example", effectiveTokens: ["allow-scripts"], sandboxActive: true },
);

child.flags.sameOrigin;                    // false
child.flagResolutions.sameOrigin.source;   // "blocked-ancestor"
```

### `derivePopup(opener, targetUrl?)`

根据打开者推导弹窗的初始沙箱与源。未给 `targetUrl` 时，弹窗按 `about:blank` 处理并继承
打开者源；给定时按目标 URL 重新解析源。

```ts
opener.popup.escaped; // 仅当显式逃逸令牌出现且 allow-popups 有效时为 true
```

### `parseSandboxAttribute(attribute)`

只做规范化解析，不处理继承：大小写不敏感、按 HTML ASCII 空白切分、去重、未知令牌单独返回。

## 测试

测试用三层嵌套 iframe 覆盖：

- 祖先与当前帧的令牌交集、不能恢复祖先限制；
- 缺失属性与空 `sandbox` 的差异；
- 同源双子令牌的不安全判定与跨源反例；
- `srcdoc` 继承父帧有效源，包括父帧已为不透明源的情况；
- 弹窗默认继承、逃逸令牌显式出现才生效、祖先令牌不继承；
- `allow-popups` 缺失时逃逸令牌无效。

```sh
npm test
```
