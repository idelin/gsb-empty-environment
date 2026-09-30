# pointer-gesture-arbiter

从指针事件识别 **click / drag / pinch（双指缩放）** 的手势仲裁器，纯 TypeScript，
不依赖浏览器布局引擎，使用 Node.js 内置测试运行器验证（Node ≥ 23，原生类型擦除）。

## 设计

- **交互（interaction）**：首个 `pointerdown` 到最后一个指针移除。
- **代际（generation）**：活动指针集合每次变化（第二指加入、指针抬起、捕获丢失）代际 +1。
  手势按代际仲裁：drag 需要恰好 1 个指针，pinch 需要 ≥ 2 个。
- **互斥提交**：每次交互中 drag/pinch 的最终 `commit` 至多一个；click 仅在从未发出任何
  preview、未超 slop、未取消、全程单指针时提交。点击转拖动后不再发 click。
- **预览与补偿**：越过阈值先发出 `preview`；被取代（第二指加入、坐标系无法转换、
  捕获丢失、超时）时发出 `compensate` 撤销已发预览；只有 `pointerup` 才会 `commit`。
- **取消与丢失**：`pointercancel` / `lostpointercapture` / `tick()` 超时看门狗都会移除
  指针并补偿预览，状态必然回到 `idle`，不会永久保持激活。
- **延迟事件**：指向已移除指针的事件被丢弃（`dropped: true`）。
- **坐标系**：事件携带 `space`，经 `SpaceConverter` 仿射变换到规范空间；无法转换的
  空间导致硬重锚定（补偿预览、不提交）。
- **布局**：节点树与字形度量由 `fixtures/scene.json` 给出，文本宽度由字形 advance
  计算（`src/layout.ts`），命中测试取最深包含节点。

## 验证

- `src/traceGen.ts`：有限轨迹生成器，在阈值附近（T-ε / T / T+ε）枚举事件序列，
  交错第二指加入、捕获丢失、延迟事件、坐标系变化与超时，全部由逻辑序号/逻辑时间驱动。
- `src/invariants.ts`：独立检查器，只看输入轨迹与（状态, 输出）日志，验证合法状态
  转移、提交计数（互斥提交 ≤ 1）、preview 与 commit/compensate 配对、终止于 idle。

## 运行

```sh
npm test          # node --test（15 个测试）
npm run verify    # 有限轨迹套件 + 不变量检查（29 条轨迹）
npm run sample    # 四个脚本化场景的事件流输出
```
