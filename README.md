# multi-header-grid

多层表头引擎：叶列可隐藏、重排，分组可折叠；从同一份状态一次性派生
rowspan/colspan、键盘导航树与叶列→数据列映射，并以事务形式原子发布。
核心计算不依赖浏览器布局引擎——节点树与字形度量全部由夹具
（`fixtures/header.fixture.ts`）给出，列宽是标签字符串的纯函数。

## 运行环境

- Node.js ≥ 22.6（使用内置 type stripping 直接运行 TypeScript，无需编译、无第三方依赖）
- 测试使用 Node 内置测试运行器 `node:test`

```sh
npm test        # node --experimental-strip-types --test test/
npm run sample  # node --experimental-strip-types samples/demo.ts
```

## 设计要点

- **模型**（`src/model.ts`）：表头是树，分组含子节点，叶节点按列 id 引用
  列注册表——同名不同 id 的列互不影响。操作（hide / show / collapse /
  expand / moveColumn）是纯函数，非法操作整体拒绝。
- **派生**（`src/derive.ts`）：先剪枝（隐藏叶、空组整体消失，不留无效
  跨度），再一次遍历同时产出单元格跨度、占位网格、键盘树和数据列映射；
  折叠组渲染为单列、rowspan 到底，代表列为组内第一个可见叶。
- **事务**（`src/store.ts`）：`commit(ops)` 把一批操作应用为一个版本，
  只发布一次完整快照；undo/redo 以版本为单位回放，任意组合可逆。
- **选择区映射**（`src/selection.ts`）：按列 id 把旧版本选区映射到新
  表头，返回候选单元格（exact / partial）或失效（invalidated）。
- **参考检查**（`src/reference.ts`）：独立的扁平实现——不遍历树，而是
  给每个叶子分配位置键并排序重建可见叶序；对任意编辑序列（含
  undo/redo）与主实现逐步比对。

## 不变量（每个发布的快照都满足，`test/helpers.ts`）

- 所有单元格 rowSpan/colSpan ≥ 1，空组不留下无效跨度；
- 占位网格每个格子恰好属于一个单元格；
- 底行单元格从左到右 == 数据列顺序，`dataIndex` 为 0..n-1；
- 键盘树只引用真实存在的单元格。

## 运行样例（`npm run sample` 输出节选）

```
== v0 initial ==
[ 基础                                  ] [ 指标                                                            ] [ Solo ]
[ Alpha ] [ Beta                      ] [ Score ] [ Score                     ] [ 子组                      ] [      ]
[       ] [                           ] [       ] [                           ] [ N-1 ] [ N-2               ] [      ]
data[0] <- alpha (owner: basic)
...
data[6] <- solo (owner: root)

== v1 transaction: hide n1+n2, collapse "metrics", move "solo" into "basic" ==
[ 基础                                                            ] [ 指标              ]
[ Solo  ] [ Alpha ] [ Beta                                      ] [                   ]
data[0] <- solo (owner: basic)
data[1] <- alpha (owner: basic)
data[2] <- beta (owner: basic)
data[3] <- m1 (owner: metrics, collapsed: metrics)

selection mapped v0 -> v1: exact
  candidate c:alpha at row 1, col 1
  candidate c:beta at row 1, col 2
reference check: MATCH
```

## 测试结果（`npm test`，Node v23.9.0）

```
✔ initial fixture: spans, depth, keyboard tree, data mapping
✔ keyboard path: walk from top-left group to a deep leaf
✔ hiding every leaf of a group removes the group, leaving no invalid span
✔ hiding a whole tree leaves an empty, consistent header
✔ collapsed group renders as one column with full rowspan and keeps coverage
✔ collapsed group whose leaves are all hidden disappears entirely
✔ cross-group move keeps explicit ownership and consistent indices
✔ same-label columns with different ids are independent
✔ random edit sequences match the independent reference   (25 种子 × 120 步)
✔ reference agrees on a scripted edge-case sequence
✔ selection maps exactly across a reorder
✔ selection over a group that later collapses maps to the collapsed cell
✔ partial mapping reports lost columns when some are hidden
✔ selection invalidates when nothing it covered remains visible
✔ out-of-grid selection invalidates immediately
✔ a commit publishes spans, keyboard tree and data mapping atomically
✔ undo/redo compose arbitrary operation sequences reversibly
✔ a new commit after undo drops the redo tail
✔ invalid ops reject the whole transaction and publish nothing
✔ empty commit is a no-op
ℹ tests 20  ℹ pass 20  ℹ fail 0
```
