# amount-input-core

金额输入核心库（TypeScript，与框架无关）。

- **raw 保留原始小数语义**：值始终是合法中间态 `-?\d*(\.\d*)?`，
   lone 负号 `-`、尾点 `123.`、空前导 `.5` 都允许存在，提交时再 `normalizeRaw`。
- **display 千位分隔**：`formatDisplay` 只在整数部分插入 `,`，负号与小数部分原样保留。
- **受控光标映射**：任意选区输入 / 删除 / 粘贴后，通过 display↔raw 双向位置映射
  计算新光标，**不依赖浏览器默认修正**——调用方必须拦截输入并回写 `value` 与
  `selectionStart/End`（见下方接入方式）。
- **约束**：负号仅首位一个，小数点仅一个，多余字符在整体清洗时丢弃并同步修正光标。

## API

| 函数 | 说明 |
| --- | --- |
| `applyEdit(prevRaw, selStart, selEnd, inserted, options?)` | 核心：应用一次编辑，返回 `{ raw, display, cursor }`（display 坐标） |
| `formatDisplay(raw)` | raw → 千位分隔显示串 |
| `sanitizeRaw(text)` | 任意文本 → 合法 raw（粘贴清洗） |
| `normalizeRaw(raw)` | 提交态规范化：去尾点、补 `0.`、去前导零、空值归一 |
| `buildPositionMap(display)` | 构建 `rawToDisplay` / `displayToRaw` 双向光标映射 |
| `isValidRaw(raw)` | 校验中间态合法性 |

`applyEdit` 的 `options.deleteDirection`：删除内容仅为千位分隔符时，
`'backward'`（Backspace）连同前一位数字删除，`'forward'`（Delete）连同后一位，
默认 `'none'` 不扩展。

## 接入方式（受控组件）

```ts
import { applyEdit, formatDisplay, normalizeRaw } from 'amount-input-core';

let raw = '';
// 在 beforeinput / onChange 中：阻止默认行为，自行重算
function onInput(selStart: number, selEnd: number, inserted: string, dir: 'none'|'backward'|'forward') {
  const next = applyEdit(raw, selStart, selEnd, inserted, { deleteDirection: dir });
  raw = next.raw;
  input.value = next.display;                 // 回写显示值
  input.setSelectionRange(next.cursor, next.cursor); // 回写光标
}
// blur 时：raw = normalizeRaw(raw); input.value = formatDisplay(raw);
```

## 验证

- `test/amount.test.mjs`：单元测试（清洗、分组、符号/小数点约束、删除扩展、粘贴、规范化、映射往返）。
- `test/simulation.test.mjs`：**连续编辑核对**——给每个原始字符打唯一标记 id，
  用种子随机数模拟数千次「选区 + 输入/删除/粘贴」，每步断言：
  库输出的 raw 与标记模型一致、光标映射回 raw 偏移一致、raw 始终合法、
  存活标记保持相对顺序且新生标记连续。

```sh
npm test   # 构建（类型擦除）+ 全部测试
```

> 离线环境无 tsc：`src/amount.ts` 仅使用可擦除类型语法，
> `scripts/build.mjs` 做受限类型擦除生成 `dist/amount.mjs` 并以 `node --check` 校验。
