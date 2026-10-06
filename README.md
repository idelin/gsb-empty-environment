# IME 组合感知的编辑控件状态机

编辑控件接收 composition / input / selection 三类事件，核心状态机保证：

- **组合期间不截断、不提交校验**：输入法组合（IME composition）过程中文本可
  自由超出长度上限，校验结果保持组合前的已提交值，且不产生撤销记录。
- **组合结束统一应用长度上限**：`compositionend` 时一次性截断，按
  **Unicode 码点**计数（`😀` 计为 1，不拆散代理对），并提交校验。
- **撤销恢复组合前状态**：整段组合只压入一条撤销记录，`undo()` 直接回到
  组合前的文本与选区。

## 结构

- `src/unicode.ts` — 码点计数 / 截断 / 切片 / 偏移夹取。
- `src/editor-machine.ts` — 纯状态机 `EditorMachine`，不依赖 DOM。
- `test/synthetic-control.ts` — 合成控件，模拟浏览器事件派发与框架回写。
- `test/editor-machine.test.ts` — 合成事件重放测试（`bun test`）。

## 事件契约

- 所有位置（选区、偏移）以 **Unicode 码点**为单位；接入真实 DOM 时由适配层
  负责 UTF-16 code unit 与码点的换算。
- input 事件携带控件当前 `value`（如同在 DOM 处理器里读取
  `event.target.value`），状态机维护“已提交视图”，因此**重复末次 input
  天然幂等**：Chrome 在 `compositionend` 后再发一次相同内容的 input、
  Firefox 先发末次 input 再发 `compositionend`，两种顺序结果一致。
- `compositionend` 的 `data` 用于替换实时文本中的组合段（Chrome 在
  `compositionend` 前不会派发携带最终串的 input）；组合段范围由组合前快照
  推算（组合开始时浏览器删除选区，组合串之后的后缀在组合期间不变）。
- **取消组合**（Escape）：浏览器把控件值还原为组合前内容后以空 `data` 触发
  `compositionend`，状态机据此恢复组合前的文本与选区，不记撤销、不提交校验。

## 测试

```sh
bun test
```

重放场景覆盖：组合期间不截断/不校验（Chrome 与 Firefox 两种事件顺序）、
码点计数与截断、重复末次 input 的幂等、取消组合恢复、选区替换（含超限
截断与 emoji）、撤销链与组合期间 undo 忽略等。

> 仓库未锁定 TypeScript 依赖；源码即类型化 TypeScript，Bun 直接运行。
> 如需 `tsc --noemit` 类型检查，请自行安装 `typescript` 与 `bun-types`。
