# anchor-migrate

批量编辑文档时，批注锚点不能只保存绝对偏移。本库提供基于**左右上下文**与**版本补丁**的锚点迁移：迁移结果只有三种——**唯一位置**、**多个候选**、**失效（保留墓碑）**，绝不替用户猜选。

## 核心概念

### 版本补丁（`src/patch.ts`）

补丁是 OT 风格的操作序列（`retain` / `insert` / `delete`），对整篇文档生效：

- `fromEdits(docLength, edits)`：由 `{start, end, text}` 编辑列表构造补丁；同一条补丁内编辑不允许重叠。
- `compose(a, b)`：组合两个**连续**补丁。连续补丁之间的替换**允许重叠**（后一个补丁可以改、删前一个补丁刚插入的文本），组合结果等价于依次应用。
- `invert(doc, patch)`：求逆补丁，用于撤销。
- `transformPosition(pos, patch, assoc)`：位置映射。`assoc` 决定 caret 依附的相邻字符：
  - `'left'` 依附左侧字符，落在插入点时停在插入文本之前；
  - `'right'` 依附右侧字符，落在插入点时跟到插入文本之后。
  - 被依附的字符被删除时返回 `null`（这保证组合律成立，见下）。

**组合律**（随机化测试持续验证）：

```
apply(doc, compose(a, b))                === apply(apply(doc, a), b)
transformPosition(p, compose(a, b))      === transformPosition(transformPosition(p, a), b)
apply(apply(doc, p), invert(doc, p))     === doc
```

### 锚点迁移（`src/anchor.ts`）

锚点保存 `pos` 及其左右各 `contextLength` 个字符的上下文快照。`migrateAnchor(anchor, patch, newDoc)` 的迁移规则：

1. 补丁精确保留的位置 → 直接映射，输出 `unique`，并用新文档刷新上下文；
2. 位置被删除/替换覆盖（`transformPosition` 返回 `null`）→ 用 `left + right` 在新文档中搜索：
   - 0 个匹配 → `invalid`，锚点留下**墓碑**（保存上下文快照），后续补丁中墓碑不丢失；
   - 1 个匹配 → `unique`，重定位（文本搬移后锚点跟随唯一位移）；
   - 多个匹配 → `candidates`，输出全部候选位置，**不猜选**；
3. 墓碑锚点每轮用快照上下文重新搜索：唯一匹配时复活（撤销、搬移归位），仍无匹配则保持失效。

`migrateAnchors(anchors, patch, newDoc)` 批量迁移，失效锚点以墓碑形式保留在数组中。

## 用法

```ts
import { apply, compose, fromEdits, invert } from './src/patch';
import { createAnchor, migrateAnchor } from './src/anchor';

const doc = 'alpha beta gamma';
const anchor = createAnchor(doc, 8, { contextLength: 4, bias: 'left' });

const p1 = fromEdits(doc.length, [{ start: 6, end: 11, text: '' }]); // 删除 'beta '
const doc1 = apply(doc, p1);
const m1 = migrateAnchor(anchor, p1, doc1);
// m1.result.kind === 'invalid'，m1.anchor.tombstone 保留墓碑

const p2 = invert(doc, p1); // 撤销
const doc2 = apply(doc1, p2);
const m2 = migrateAnchor(m1.anchor, p2, doc2);
// m2.result === { kind: 'unique', pos: 8 }，墓碑复活

// 连续补丁可以组合后再迁移，结果与逐步迁移一致
const pc = compose(p1, p2);
const mc = migrateAnchor(anchor, pc, doc2);
// mc.result 与 m2.result 相同
```

## 验证的场景（`test/scenarios.test.ts`）

- **重复段落**：删除锚点所在段落，剩余相同段落产生多个候选，不猜选；继续删除至唯一匹配后复活。
- **撤销**：删除覆盖锚点 → 墓碑；应用逆补丁 → 复活到原位置；组合补丁（恒等）直接迁移结果一致。
- **文本搬移**：剪切 → 墓碑；粘贴到他处 → 唯一复活；组合补丁直接重定位结果一致。
- **同位置插入**：连续补丁在同一位置插入，两种 bias 下组合映射与逐步映射一致。
- **连续补丁重叠替换**：后一个补丁修改/横跨前一个补丁的替换区，组合为单个编辑，所有位置、两种亲和性下组合映射 == 逐步映射。
- **随机化不变量**：400 轮随机文档/补丁验证 compose/apply/invert/位置映射组合律；200 轮验证锚点层面组合与逐步一致。

## 构建与测试

```sh
npm test   # 等价于 tsc -p tsconfig.json && node dist/test/index.js
```

零运行时依赖；测试使用自带的迷你框架（`test/framework.ts`），无需联网安装任何包。
