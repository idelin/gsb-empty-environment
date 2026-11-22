# SVG 清洗组件

面向资源处理管线的无依赖 SVG 清洗器。它不执行 SVG、不展开自定义实体，也不加载任何远程资源，而是严格解析后按白名单重建文档。

## 使用

```ts
import { sanitizeSvg, sanitizeSvgWithReport } from "./src/index.ts";

const safeSvg = sanitizeSvg(untrustedSvg, {
  maxBytes: 2 * 1024 * 1024,
  maxDepth: 64,
  maxNodes: 10_000,
});

const { svg, issues } = sanitizeSvgWithReport(untrustedSvg);
```

也可以复用配置实例：

```ts
import { SvgSanitizer } from "./src/index.ts";

const sanitizer = new SvgSanitizer({ maxDepth: 32, maxNodes: 2_000 });
const safeSvg = sanitizer.sanitize(untrustedSvg);
```

## 清洗策略

- 拒绝 `DOCTYPE`、DTD、外部实体和未声明的命名实体；只解码 XML 预定义实体与合法数字字符引用。
- 仅保留 SVG 绘图、渐变、裁剪、滤镜、标记、文本和样式等白名单元素。`script`、`foreignObject`、`image`、动画元素及其他可执行/可加载容器不会进入输出。
- 移除 `on*` 事件属性、未知属性、危险命名空间属性。
- 通过输入大小、最大嵌套深度和最大节点数限制资源消耗；超限时抛出 `SvgLimitError`。
- 元素 ID 必须唯一且合法。`href`/`xlink:href` 只接受当前文档内、清洗后仍存在的 `#fragment`；断链会移除该属性，循环引用链会被切断。
- `fill`、`stroke`、`filter`、`clip-path`、`mask`、`marker-*` 等属性中的 `url()` 只允许指向当前文档内已清洗的节点。
- 内联样式和 `<style>` 只保留安全 CSS 子集。`@import`、`@font-face`、外部/相对 URL、`data:`、`javascript:`、CSS 表达式等声明会被移除；局部 `url(#id)` 继续有效。
- 输出始终包含 SVG 命名空间，并在使用 `xlink:href` 时补充 `xmlns:xlink`，可直接交给 SVG 渲染器。

默认限制为 2 MiB、64 层嵌套和 10,000 个输入元素。`maxNodes` 统计解析阶段的所有元素，包括随后被移除的恶意元素，以免依赖“先解析、后删除”来绕过限制。

## 错误

`SvgSanitizationError` 的 `code` 可用于管线决策：

- `EXTERNAL_ENTITY`：`DOCTYPE`、DTD、外部或未知实体。
- `INVALID_XML` / `INVALID_ROOT` / `INVALID_INPUT`：输入无法作为 SVG 文档处理。
- `MAX_BYTES_EXCEEDED`、`MAX_DEPTH_EXCEEDED`、`MAX_NODES_EXCEEDED`：触发限制。

可恢复的问题（被移除的元素、属性、引用或样式）通过 `sanitizeSvgWithReport().issues` 返回，默认清洗仍产出可用 SVG。

## 验证

测试夹具覆盖三类恶意输入：活动脚本/事件与危险容器、XXE/外部实体、断链/循环引用与样式资源注入；另有一张包含渐变、裁剪、标记、滤镜、`use` 与 CSS 的正常矢量图。

```sh
npm test
```
