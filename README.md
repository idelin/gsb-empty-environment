# semantic-token-index

版本化语义 token 索引：为语义高亮提供增量更新。每次源码变更返回相邻版本之间的
**最小区间替换列表**，远端缓存只需应用补丁即可得到与全量分析完全一致的结果。

## 特性

- **版本化索引**：`SemanticTokenIndex.update(source)` 快照新版本并返回增量补丁；
  `diff(from, to)` 可计算任意两个已记录版本间的替换列表。
- **最小替换区间**：基于 Myers O(ND) 最小编辑脚本，相邻编辑合并为最大变更区间，
  得到 `{ start, deleteCount, tokens }` 形式的替换列表；`applyPatches` 从右往左应用。
- **UTF-16 位置**：token 的 `start`/`length` 使用 UTF-16 码元单位（与 LSP 一致），
  保证有序且不重叠（构建时校验）。
- **Unicode 标量存储**：`SourceText` 底层以标量值（码点）数组存储源码，提供
  `utf16OffsetToScalarIndex` / `scalarIndexToUtf16Offset` 双向映射；落在代理对
  中间的偏移会抛出 `RangeError`。
- **词法作用域**：`let`/`const` 声明进入当前作用域，`{}` 开闭作用域；引用解析到
  最近内层绑定（遮蔽），未解析引用标记为 `unresolved`。声明被修改/重命名时，
  远处同名引用的绑定键随之改变，体现在增量补丁中。

## 结构

- `src/sourceText.ts` — 标量存储与 UTF-16 ↔ 标量双向映射
- `src/lexer.ts` — Unicode 标识符感知的词法分析（输出 UTF-16 偏移）
- `src/analyzer.ts` — 作用域解析，产出有序不重叠的语义 token
- `src/diff.ts` — Myers 最小 diff、替换列表合并、补丁应用
- `src/semanticIndex.ts` — 版本化索引门面

## 用法

```ts
import { SemanticTokenIndex, applyPatches } from './src';

const index = new SemanticTokenIndex('let x = 1;\nx;');
const v0 = index.getTokens(0);

const patches = index.update('let x = 1;\nlet y = x;\ny;');
// patches: [{ start, deleteCount, tokens }] —— 最小区间替换列表
const v1Incremental = applyPatches(v0, patches);
// v1Incremental 与 index.getTokens(1) 完全一致
```

## 测试

```sh
npm test   # tsc 编译后运行 test/ 下的全部测试
```

覆盖：UTF-16 ↔ 标量双向映射（含代理对）、作用域遮蔽、声明修改影响远处同名引用、
新增声明、空 diff、多版本链上"增量补丁应用 == 全量结果"，以及 200 步随机编辑的
确定性模糊一致性测试。
