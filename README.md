# provenance-text

文本拼接库：在拼接、复制、移动、合并片段时追踪每个输出字符的来源集合，
并按“来源读者集合的交集”计算可见性。查询某读者可见文本时，返回遮挡区间
及其原因来源。

## 核心语义

- **来源注册**：`SourceRegistry.register(id, readers)` 声明每个来源片段
  允许哪些读者阅读。
- **逐字符来源**：文档中每个字符携带 `sources`（来源 id 集合）。插入片段时
  每个字符的来源为 `{fragmentId}`；`insertLiteral` 插入无来源文本，
  对所有人可见。
- **权限交集**：字符对读者可见，当且仅当其所有来源的读者集合都包含该读者
  （即有效读者 = 各来源读者集合的交集）。
- **复制 / 移动**：`copyRange` / `moveRange` 保留每个字符的来源集合，
  重复引用同一来源不会丢失限制。
- **合并**：`insertDocument` 做拼接；`overlay` 做按位合并，重叠字符的来源
  取并集，因此可见读者变为交集（只会更严格）。
- **格式化不降权**：`mapChars` 让派生字符继承原字符来源；
  `collapseWhitespace` 合并空白时取各字符来源的并集，保证可见读者
  是原字符可见读者的子集——格式化永远不会放宽限制。
- **可见性查询**：`visibleFor(reader)` 返回 `{ text, redactions }`，
  不可见字符替换为 `█`，`redactions` 给出每个连续遮挡区间的
  `[start, end)` 与 `reasons`（阻挡该读者的来源 id 列表）。

位置均按 UTF-16 码元计（与 `String.prototype.slice` 一致）。

## 示例

```ts
import { Document, SourceRegistry } from './src/provenance.ts';

const registry = new SourceRegistry();
registry.register('public', ['alice', 'bob']);
registry.register('alice-only', ['alice']);

const doc = new Document(registry);
doc.appendFragment('public', 'hello ');
doc.appendFragment('alice-only', 'secret');

doc.visibleFor('bob');
// { text: 'hello ██████', redactions: [{ start: 6, end: 12, reasons: ['alice-only'] }] }
```

## 验证

`src/reference.ts` 是逐字符存储来源集合的朴素参考实现；`tests/run.ts`
用确定性随机操作序列（插入、复制、移动、删除、跨权限边界替换、合并、
格式化）对 `Document`（区间归并存储）与参考实现做差分比对，并包含针对
重复引用、删除、跨权限边界替换的定点测试。

```sh
npm test   # 需要 Node >= 23.6（原生运行 TypeScript）
```
