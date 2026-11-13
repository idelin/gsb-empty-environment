# rawtext 二次解析裁决库

过滤结果再入解析器时，`noscript` 里已转义的起始标签不能重新变成元素。
本库是一个零依赖、可直接导入的 TypeScript HTML 片段/文档解析器，专门用于
对这类二次解析结果做元素树裁决。

## 解析规则

- `noscript`、`script`、`style`、`textarea`、`title` 的内容按 rawtext 处理：
  `<`、`>`、`&lt;` 等字符原样保留，不进行实体解码，也不产生子元素。
- 只有大小写不敏感的匹配结束标签（如 `</noscript>`、`</script >`）可以结束
  rawtext 区域。
- 普通元素（例如 `p`）中的文本仍按 HTML 规则解码字符引用。因此
  `<p>&lt;img src=x onerror=alert(1)&gt;</p>` 得到文本节点
  `<img src=x onerror=alert(1)>`，不会得到 `img` 元素。
- 序列化时 rawtext 文本按原字符输出，普通文本重新转义；再次解析后节点类型、
  元素结构和文本值保持稳定。

## 导入

```ts
import {
  adjudicateReentry,
  analyzeRawtextPayload,
  comparePayloadParents,
  parseFragment,
  parseDocument,
  serialize,
  toElementTree,
  compareElementTrees,
} from "./index.ts";
```

`package.json` 已声明 `exports`、`main` 和 `types`，因此也可以按包名导入。

## 核心 API

### `parseFragment(input, options?)`

返回 `DocumentFragmentNode`。元素带有 `tagName`、`attrs`/`attributes`、
`childNodes`/`children`；文本节点带有 `value`/`data`。节点还提供只读的
`nodeType`、`textContent` 以及 `getElementsByTagName`、`querySelectorAll`、
`getElementById` 等查询方法。

```ts
const fragment = parseFragment(
  "<noscript>&lt;img src=x onerror=alert(1)&gt;</noscript>",
);
const noscript = fragment.children[0];
const text = noscript.children[0];

// text.type === "text"
// text.value === "&lt;img src=x onerror=alert(1)&gt;"
```

### `reparse(source)` / `reparse(parent, payload)`

先解析、序列化，再解析一次，返回第二次解析的片段。结果片段上附带
非枚举的 `ok`、`stable`、`addedElements`、`result` 等裁决字段。完整裁决对象
请使用 `adjudicateReentry`。

### `adjudicateReentry(source)` / `adjudicateReentry(parent, payload, options?)`

返回 `ReparseResult`：

```ts
const result = adjudicateReentry(
  "noscript",
  "&lt;img src=x onerror=alert(1)&gt;",
);

result.ok;                // true：二次解析没有新增元素
result.stable;            // true：元素树和文本完全稳定
result.addedElementNames; // []
result.serialized;        // <noscript>&lt;img ...&gt;</noscript>
```

`assertNoNewElements(...)` 在新增元素时抛出 `RawtextReentryError`。

### `comparePayloadParents(payload, leftParent?, rightParent?)`

用两个只差父元素的包装载荷进行比较，默认是 `noscript` 与 `p`：

```ts
const comparison = comparePayloadParents(
  "&lt;img src=x onerror=alert(1)&gt;",
);

comparison.sameElementTree;      // false：根元素不同
comparison.sameChildElementTree; // true：子元素树都是空
comparison.left.ok;              // true
comparison.right.ok;             // true
```

### `analyzeRawtextPayload(payload, options?)`

对五个 rawtext 父元素逐个执行二次解析，返回每个父元素的
`{ parent, ok, stable, addedElementNames, result }`。

### `compareScriptingModes(source)` / `detectMutationXss(source)`

同一份载荷分别按脚本关闭（过滤/清洗侧）和脚本开启（浏览器侧）解析，
比较元素树。`compareScriptingModes` 返回完整裁决对象，
`detectMutationXss` 在浏览器侧新增元素时返回 `true`。

### `toElementTree(...)` / `compareElementTrees(...)`

`toElementTree` 把片段、文档、元素或载荷字符串转成
`{ tag, attributes, children }` 元素树。`compareElementTrees` 默认逐层比较
标签、属性和子元素；载荷只差父元素时可传 `{ ignoreRootTag: true }`，或直接
调用 `comparePayloadElementTrees`。

## 测试

```bash
npm test
```

测试覆盖五个 rawtext 父元素、`p` 文本节点控制组、真实标签不被二次分词、
结束标签边界、序列化往返稳定性、文档解析和父子元素树比较。
