# foreignObject嵌HTML

svg的foreignObject里可以再嵌一套未过滤的HTML。foreignObject内部按HTML走同一套元素和URL限制，不能因为外层是svg而跳过。foreignObject里的普通段落保留。文档管线调用可导入的TypeScript库做这次裁决。在foreignObject内外各放一个事件属性，核对残留。

## 实现

零依赖的可导入 TypeScript 库（Node 原生 type stripping 运行，无需安装依赖）：

- `src/tokenizer.ts` / `src/tree-builder.ts`：自带 HTML/SVG 解析，构建带命名空间与 `inForeign` 标记的节点树。
- `src/policy.ts`：元素/属性白名单、事件属性判定、URL scheme 限制。
- `src/sanitizer.ts`：遍历节点树执行裁决，元素被移除时保留安全子节点，原始可执行文本（script 等）不泄漏，并产出移除报告。
- `src/serializer.ts`：转义输出，SVG 空元素自闭合、属性名恢复驼峰。
- `src/pipeline.ts`：文档管线入口，批量调用库做裁决。
- `index.ts`：对外导出的库入口。

### foreignObject 规则

- 进入 `foreignObject` 后，其内部元素按 **HTML** 的同一套元素/属性/URL 白名单裁决，不会因为外层是 SVG 而跳过。
- `foreignObject` 标签本身仍是 SVG 元素，保留 `width`/`height` 等几何属性。
- 内部普通段落（如 `<p>`）保留；`<script>` 等危险元素及其文本被移除。
- 内外所有 `on*` 事件属性一律剔除；`javascript:`/`vbscript:`/`data:` 等 URL（含控制字符混淆）被拦截。

## 用法

```ts
import { sanitizeOne, runSanitizationPipeline } from "./index.ts";

const result = sanitizeOne(
  '<div onclick="x()"><svg><foreignobject><p onmouseover="y()">keep</p></foreignobject></svg></div>',
);
result.html;   // 清洗后的 HTML，内外 on* 均无残留
result.report; // 每条移除项（element/attribute/url）

runSanitizationPipeline([{ id: "doc-1", html: "..." }]);
```

## 测试

```sh
npm test
```

用例在 foreignObject 内外各放置一个事件属性并断言无任何 `on*` 残留，同时校验内部段落保留、script 移除、危险 URL 拦截。
