# doc-symbols

TypeScript 文档符号库：解析标题、脚注引用与脚注定义，支持增量更新并返回受影响的引用集合。

## 语法

- 标题：`# …` 至 `###### …`
- 引用：`[^name]`（正文与定义体内均可出现）
- 定义：`[^name]: body`，可出现在引用之后（前向引用合法）

## 诊断

| code | severity | 含义 |
| --- | --- | --- |
| `undefined-reference` | error | 引用没有对应定义 |
| `duplicate-definition` | error | 同名重复定义；保留首定义，后续定义进入 `duplicates` |
| `circular-footnote` | warning | 定义体相互引用成环（含自引用），基于 Tarjan SCC |

## API

```ts
import { parseDocument } from './src/parser.ts';
import { IncrementalDocument } from './src/incremental.ts';

const doc = new IncrementalDocument('See [^a].\n[^a]: def');
const result = doc.update('See [^a].\n\n[^a]: def');
result.affectedReferences; // ReferenceChange[]：added / removed / changed / moved
result.diagnostics;        // 更新后的完整诊断
result.changedLines;       // 变化的行区间
```

- 引用身份：`name`（首次出现）与 `name#N`（第 N 次同名出现），位置平移后仍稳定。
- `changed`：诊断或解析目标（定义名 + 定义体）变化；`moved`：仅位置变化。
- 删除定义后，其所有引用（含其他定义体内的引用）被重新诊断并计入受影响集合。

## 增量策略

按行缓存解析结果；`update` 通过公共前后缀定位变化区间，仅重解析变化行，
未变化行复用缓存（行号在装配阶段重排，列偏移天然免疫行平移）。
符号表与诊断全局重建以保证正确性。

## 测试

```sh
npm test          # node --test（Node >= 23.6，原生类型擦除，无需构建）
npm run typecheck # 需要安装 devDependency typescript
```

测试包含场景用例（前向引用、循环脚注、重复定义、删除定义、位置平移）
以及 300 轮随机编辑的模糊测试：每轮将增量结果与独立实现的全量解析
oracle 逐一核对符号表、诊断与受影响引用集合。
