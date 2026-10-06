# amount-input-core

金额输入核心库（TypeScript，零依赖，DOM 无关）。

- **保留原始小数语义**：raw 就是用户输入的语义本身，库从不把它解析成 number。
  中间态如 `-`（孤负号）、`12.`（尾点）、`.5`、`0.50`、`007` 都原样保留。
- **显示时插入千位分隔符**：`display = formatDisplay(raw)`，纯函数，如
  `-1234567.89` → `-1,234,567.89`；小数部分不分组。
- **任意编辑后精确映射光标**：输入、选区替换、退格、前删、粘贴统一走
  `display 选区 → raw 选区 → splice + 净化 → 重新格式化 → 映射回 display 光标`
  的管线，不依赖浏览器对 value 的默认修正（宿主需受控重渲染）。
- **语法约束**：负号仅能在首位；小数点仅一个（先到先留）；其余字符
  （含粘贴进来的逗号、空白、字母）在净化阶段丢弃。

## 坐标系约定

所有光标/选区位置都是**字符边界下标**（`0..length`）。宿主与库之间只用
display 坐标系通信；库内部维护 raw ↔ display 的双向映射表：

- `rawToDisplay[i]`：raw 第 i 边界 → display 边界（raw 边界可精确往返）；
- `displayToRaw[d]`：display 第 d 边界 → raw 边界；逗号两侧边界吸附到同一
  raw 边界（逗号不对应任何 raw 字符）。

## API

```ts
import {
  applyEdit, deleteBackward, deleteForward,   // 编辑管线
  formatDisplay,                              // raw -> 千位分隔显示
  sanitizeRaw, sanitizeWithCaret, isValidRaw, // 净化
  buildPositionMap,                           // 位置映射表
  rawIndexToDisplayIndex, displayIndexToRawIndex,
} from "amount-input-core";

applyEdit({ raw, selectionStart, selectionEnd, inserted })
// => { raw, display, caret }   选区为 display 坐标，inserted 可为任意粘贴文本

deleteBackward(raw, caretDisplay)  // 退格，自动跨过千位逗号
deleteForward(raw, caretDisplay)   // 前删，自动跨过千位逗号
```

## 宿主接入（受控组件）

```ts
input.addEventListener("beforeinput", (e) => {
  e.preventDefault(); // 关键：阻止浏览器默认修正，完全由库接管
  let result;
  if (e.inputType === "deleteContentBackward" && input.selectionStart === input.selectionEnd) {
    result = deleteBackward(raw, input.selectionStart!);
  } else if (e.inputType === "deleteContentForward" && input.selectionStart === input.selectionEnd) {
    result = deleteForward(raw, input.selectionStart!);
  } else {
    result = applyEdit({
      raw,
      selectionStart: input.selectionStart!,
      selectionEnd: input.selectionEnd!,
      inserted: e.data ?? "",
    });
  }
  raw = result.raw;
  input.value = result.display;
  input.setSelectionRange(result.caret, result.caret);
});
```

## 测试与核对方法

`npm test`（需 Node ≥ 22.6，直接以类型擦除方式运行 TS）。

- `test/edit.test.ts` 用标记语言描述编辑：raw 串中 `|` 为光标、`[...]` 为
  选区，例如 `runEdit("123|4", "5")` 断言得到 `{ raw: "1235|4", display: "12,35|4" }`。
- `test/fuzz.test.ts` 用**带原始位置标记的参考模型**（`test/model.ts`，每个
  字符携带唯一 id，全程只在 raw 坐标系中 splice + 净化）与库实现并行执行
  随机连续编辑（输入 / 选区替换 / 退格 / 前删 / 粘贴，每种子 500 步），逐步
  交叉核对：存活字符序列一致、光标落点一致，并验证不变量
  `display === formatDisplay(raw)`、`sanitizeRaw(display) === raw`、
  raw 边界映射可精确往返。

## 目录

```
src/sanitize.ts  净化规则（负号首位、单点小数、光标随净化映射）
src/format.ts    千位分隔显示
src/mapping.ts   raw ↔ display 位置映射表
src/edit.ts      编辑管线（applyEdit / deleteBackward / deleteForward）
test/            单元测试 + 标记模型 + 连续编辑模糊核对
```
