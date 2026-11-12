# XInclude 展开范围限制

供 TypeScript 文档转换服务调用的 XInclude 预处理组件。它在合并多个 XML
片段之前先完成 XInclude 展开，并把“能读什么、能嵌多深、能展开多大”限制在
调用方登记的同一资源包内，零第三方依赖。

## 威胁模型与对策

- **按命名空间 URI 识别，而非前缀**：只认
  `http://www.w3.org/2001/XInclude` 下的 `include`/`fallback`。攻击者把
  `xi:` 改成任意前缀、或用默认命名空间声明都无法绕过；反过来，前缀叫
  `xi:` 但 URI 错误的元素不会被当作指令。
- **href 仅限同包登记资源**：只接受相对引用，拒绝绝对路径
  （`/etc/passwd`、`//host/share`）、任何 URI scheme（`file:`、`http:`、
  `jar:` 等）、反斜杠、百分号编码、片段标识符；点段解析后越出包根即拒绝。
  归一化得到的“包根相对资源键”才会交给调用方解析器，解析器对未登记资源
  返回 `null`，形成第二道防线。
- **`xml:base` 不参与解析基准**：相对 href 永远以当前文档自身在包内的目录
  为基准，`xml:base` 无法把解析范围牵引到包外；输出时 `xml:base` 被剥离。
- **`parse="text"` 安全内联**：文本资源整体作为文本节点，序列化时统一
  转义，无法借文本内容注入新的 XML 标记。
- **深度与字节双上限**：`maxDepth` 限制嵌套包含层数，`maxExpandedBytes`
  按 UTF-8 字节累计展开量，超限立即中止（不静默截断），防止嵌套炸弹与
  超大资源耗尽内存。
- **循环检测**：记录祖先资源链，重复进入同一资源即抛
  `XIncludeCycleError`，不依赖深度限制兜底。
- **无 XXE 攻击面**：内置解析器直接拒绝 DOCTYPE/DTD，仅支持五个内置
  实体与数字字符引用。
- **错误分级**：安全策略错误（`XIncludeSecurityError`）与全局字节上限
  （`XIncludeLimitError`）不会被 `xi:fallback` 吞掉；资源缺失、循环、过深
  等资源级错误才允许 fallback 兜底。

## 用法

```ts
import { MapResourceResolver, XIncludeProcessor } from "./index.ts";

const resolver = new MapResourceResolver({
  "parts/header.xml": "<header>...</header>",
});
const processor = new XIncludeProcessor({
  resolver,
  rootResource: "main.xml",
  maxDepth: 16,
  maxExpandedBytes: 1024 * 1024,
});
const result = await processor.process(
  `<doc xmlns:xi="http://www.w3.org/2001/XInclude">` +
    `<xi:include href="parts/header.xml"/></doc>`,
);
result.xml;           // 展开并序列化后的 XML
result.resolverCalls; // 解析器回调次数
result.expandedBytes; // 累计 UTF-8 字节
```

自定义解析器只需实现 `ResourceResolver`，返回资源字符串或 `null`：

```ts
const processor = new XIncludeProcessor({
  resolver: {
    resolve({ key }) {
      return packageStore.has(key) ? packageStore.get(key) : null;
    },
  },
});
```

## 模块结构

- `src/xml-parser.ts`：命名空间感知的最小 XML 解析器（拒绝 DOCTYPE）。
- `src/xml-serializer.ts`：序列化，保留命名空间声明、剥离 `xml:base`、
  统一转义。
- `src/href.ts`：href 安全归一化（包根相对资源键）。
- `src/resource-resolver.ts`：解析器接口与带计数的 Map 实现。
- `src/xinclude-processor.ts`：展开编排、深度/字节/循环控制、fallback 分级。
- `src/errors.ts`：分级错误类型。

## 测试

```sh
npm test
```

测试覆盖前缀改名、默认命名空间、绝对路径/`file:`/scheme 攻击、点段越界、
`parse="text"` 注入、嵌套循环、深度与字节上限、`xml:base` 越权尝试，以及
合法同包片段下解析器回调的精确调用次数。
