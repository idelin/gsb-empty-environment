# tree-core

无头（headless）树视图核心，TypeScript 实现，零依赖。覆盖：展开/折叠、上下移动、
Home/End、类型搜索（type-ahead）、多选、异步子节点装载、树重排、筛选，以及不借
浏览器布局引擎的字形度量。节点树与字形度量均由夹具（`fixtures/`）给出。

要求 Node.js ≥ 22.6（利用原生类型剥离直接运行 `.ts`，内置 `node:test` 验证）。

```sh
npm test        # node --test：34 个测试
npm run demo    # 运行样例，打印每一步的可见行/焦点/选择/版本/内容宽度
```

## 结构

| 文件 | 职责 |
| --- | --- |
| `src/tree.ts` | 结构模型：节点、展开集、版本计数、异步装载令牌、细粒度事件 |
| `src/visible.ts` | 增量可见序列：结构变更只做局部 splice，不遍历整树 |
| `src/focus.ts` | 焦点游标，绑定树版本 |
| `src/selection.ts` | 多选 + 范围锚点，绑定树版本，与焦点完全分离 |
| `src/typeahead.ts` | 类型搜索缓冲，绑定树版本并带超时 |
| `src/measure.ts` | 由夹具字形度量计算行宽/内容宽度 |
| `src/controller.ts` | 命令层：键盘映射、确定性策略（见下） |
| `fixtures/` | 夹具节点树与字形度量 |
| `examples/demo.ts` | 运行样例 |
| `test/` | Node.js 内置测试（含有限事件探索） |

## 确定性规则

- **焦点与选择分离**：移动焦点不改变选择；Space/Ctrl 切换单节点，Shift 扩展范围。
- **禁用节点**：不可聚焦（导航与类型搜索跳过）、不可选中、范围选择自动排除；
  禁用被聚焦/已选节点时，焦点移到最近可用可见行、选择被清除。
- **版本绑定**：树每次变更 `version` 恰好 +1。焦点、范围锚点、类型搜索缓冲各自
  记录绑定版本；树变更后，过期锚点在下一次范围操作时重锚到当前焦点，过期类型
  搜索缓冲立即失效。
- **异步装载**：按节点单调递增的装载令牌使旧装载失效；`completeLoad` 只插入子
  节点、绝不触碰展开状态与选择 —— 旧装载不会让已关闭分支重新展开，异步新增
  子项也不会重置已有选择。`cancelLoad` 使迟到结果被丢弃。
- **折叠含焦点的子树**：焦点回到被折叠节点；若该节点禁用则上溯到最近可用祖先。
- **删除焦点节点**：确定后备 —— 被删区段之后第一个可用可见行，否则之前最后一
  个可用可见行，否则 null。
- **Shift 范围**：按可见顺序取锚点到焦点之间的可用节点。
- **筛选**：非空筛选显示匹配节点及其祖先（忽略展开状态）；筛选是模式切换，结
  构变更在筛选模式下整体重算，非筛选模式下全部增量 splice。
- **有限事件探索**：`test/exploration.test.ts` 用确定性 PRNG 生成有界事件序列
  （导航/选择/类型搜索/装载/取消/重排/筛选/禁用），每步断言不变量（增量可见序
  列 == 全树参考遍历、焦点/选择/锚点合法、缓冲不过期），并把事件日志从头回放，
  要求状态哈希完全一致。

## 运行样例（`npm run demo` 节选）

```
== remote children loaded (selection preserved, expansion of others kept) ==
  v [ ] alpha
      [ ] alpha one
      [ ] alpha two (disabled)
  >   [ ] alpha three
  > [ ] beta (disabled)
> v [x] gamma
      [x] gamma one
      [ ] gamma two
  v [ ] remote
      [ ] remote one
      [ ] remote two (disabled)
      [ ] remote three
    [ ] delta
focus=gamma selection=[gamma, gamma-one] version=4 width=78px

== remove focused alpha -> fallback to next enabled visible row ==
  > [ ] beta (disabled)
> v [ ] gamma
      [ ] gamma one
      [ ] gamma two
  v [ ] remote
      [ ] remote one
      [ ] remote two (disabled)
      [ ] remote three
    [ ] delta
focus=gamma selection=[] version=6 width=78px
```

## 测试结果（`npm test`）

```
✔ a stale load must not re-expand a branch the user closed
✔ only the latest load may complete; earlier results are discarded
✔ cancelling a load discards its late result
✔ a load resolving after its node was removed is discarded
✔ failed loads leave the branch loadable and do not corrupt state
✔ async children never reset focus or selection
✔ expanding a lazy branch auto-starts the configured loader
✔ bounded exploration: invariants hold and replay reproduces state
✔ exploration covers cancel and disabled interactions deterministically
✔ measureText sums fixture advances with default fallback
✔ rowGeometry applies indentation per depth
✔ contentWidth tracks the widest visible row without any DOM
✔ expand/collapse changes the visible rows
✔ Down/Up/Home/End skip disabled nodes
✔ collapsing the subtree containing focus returns focus to the collapsed node
✔ collapsing a disabled ancestor returns focus to the nearest enabled ancestor
✔ ArrowRight expands, ArrowLeft collapses or moves focus to parent
✔ clicking or focusing a disabled node is ignored
✔ focus and selection are independent
✔ Space toggles the focused node; disabled nodes cannot be selected
✔ Shift range follows visible order and excludes disabled nodes
✔ stale anchor (tree version changed) re-binds to the new focus
✔ removing nodes prunes selection and anchor
✔ inserting children (including async completion) does not reset selection
✔ prefix match jumps focus
✔ a disabled node is never matched, even by its full label
✔ buffer accumulates within timeout and restarts after timeout
✔ search wraps around from after the current focus
✔ buffer is bound to the tree version: any mutation clears it
✔ Escape cancels the type-ahead buffer
✔ structural updates are incremental splices, never full traversals
✔ filter shows matches plus ancestors and ignores expansion
✔ filter mode recomputes on structural change and stays consistent
✔ move between parents keeps the visible list consistent
ℹ tests 34
ℹ pass 34
ℹ fail 0
```
