# anchor-migrate

批量编辑文档时，批注（锚点）不能只保存绝对偏移。本库用「左右上下文 + 版本补丁」做锚点迁移：
位置映射保证连续性，上下文保证身份，二者结合输出确定性的迁移结果。

## 核心概念

- **Anchor**：文档中一个位置，由左上下文 `left`、右上下文 `right` 标识（`createAnchor` 按半径捕获）。
- **Patch**：一组**顺序应用**的编辑 `{ start, end, text }`。每个编辑的坐标基于它之前所有编辑应用后的文档状态，因此同一补丁内部、以及连续补丁之间都允许**重叠替换**。
- **AnchorState**：迁移结果，只有四种状态，**绝不猜选**：
  - `resolved`：唯一确定位置；
  - `ambiguous`：多个候选位置，`candidates` 全部列出；
  - `tombstone`：锚点被删除覆盖且无唯一归宿，墓碑（删除前的位置与被删文本）永久保留在 `tombstones` 中；
  - `invalidated`：上下文在文档中已不存在。

## 迁移规则

单个编辑 `[start, end) → text` 下的位置映射：

| 位置 | 结果 |
| --- | --- |
| `position < start` | 不变 |
| `position > end` | 平移 `text.length - (end - start)` |
| `position === start`（纯插入 `start === end`） | 保持在插入文本之前（左亲和，确定性规则） |
| `position === start`（删除/替换左边界） | 幸存，停留在区间起点 |
| `position === end`（`end > start`） | 落在替换文本之后 |
| `start < position < end` | 被删除覆盖 → **墓碑** |

删除覆盖锚点时：先记录墓碑，再用上下文在结果文档中查找归宿——唯一匹配则跟随（文本搬移），
多个匹配则输出候选（重复段落），零匹配则进入 `tombstone` 状态。墓碑记录永不清除，
撤销删除后锚点可按唯一上下文恢复，墓碑仍留档。

连续性未断的锚点信任位置映射，并在新位置**重新捕获上下文**，因此邻近编辑
（如同位置插入）会被吸收进上下文，而不会误判失效。

## 组合映射 ≡ 逐步映射

`composePatches(p1, p2)` 把连续补丁合并为一个（编辑序列的连接）。迁移是对编辑序列的折叠，因此

```
migrate(doc, compose(p1, p2), anchor) ≡ migrate(p2) ∘ migrate(p1)
```

对文档、位置与锚点状态都严格成立。测试用**重复段落、撤销、文本搬移、同位置插入**
四个场景及 300 组随机编辑序列验证了该性质。

## API

```ts
import {
  createAnchor, migrateAnchor, migrateThroughPatches,
  composePatches, applyPatch, mapPosition, resolveByContext,
} from 'anchor-migrate';

const anchor = createAnchor(doc, 42, 32);          // 在偏移 42 处创建锚点
const { doc: doc2, state } = migrateAnchor(doc, patch, anchor, { version: 'v2' });
const end = migrateThroughPatches(doc, [p1, p2], anchor); // 逐步迁移
const same = migrateAnchor(doc, composePatches(p1, p2), anchor); // 组合迁移，结果相同
const again = resolveByContext(otherDoc, state);   // 版本失同步时按纯上下文重定位
```

## 构建与测试

```sh
npm run build   # tsc -p tsconfig.json
npm test        # 编译并运行全部场景测试
```
