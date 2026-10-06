# stackwise

TypeScript 堆叠库：按时间对齐不同系列，区分缺失与零；正负值分别从零向上/向下累计；
百分比模式下正负两侧各按各自的绝对和归一。另含按系列分批修订的数据流，带列级屏障
与撤回/隐藏恢复。

## 构建与测试

```sh
npm test        # 编译 (tools/tsc.js 定位 tsc) 并运行全部测试
npm run build   # 仅编译到 dist/
```

环境需要 TypeScript 编译器（`npm install`，或 `tools/tsc.js` 已知的本机缓存路径）。

## 核心堆叠（src/stack.ts）

- `stackSeries(inputs, mode)`：在所有系列时间戳的排序并集上对齐；某系列在某时刻无数据
  为 `missing`（`lower/upper` 为 `null`），显式 `0` 为 `zero`（零高度切片），二者严格区分。
- 正值从 0 向上累计，负值从 0 向下累计，两侧互不影响。
- `percent` 模式：正值高度按该列正和归一（正侧跨 `[0,1]`），负值按该列负绝对和归一
  （负侧跨 `[0,-1]`）。
- 隐藏系列（`hidden: true`）仍占时间轴与槽位，但不参与累计，所有点输出空界。
- 每个输出点：`{ t, lower, upper, missing, zero, value }`。

## 守恒检查（src/checks.ts）

- `checkAllZeroColumn`：全零列每个可见点为零高度切片且列范围为 `[0,0]`。
- `checkOrderConservation`：切换系列顺序后，各系列切片高度与列范围不变。
- `checkHiddenConservation`：absolute 模式下隐藏不改变可见系列高度；两种模式下隐藏
  等价于移除，取消隐藏精确恢复原界。

## 修订数据流（src/revisions.ts）

- 每个系列按代（generation）分批提交点更新；同系列状态为 1..g 各批次的累计合并
  （按点覆盖）。批次可跨系列、同系列乱序到达，缺口自动缓冲。
- **屏障规则**：屏障 G = 所有所需系列连续交付代数的最小值。时间列仅在屏障达到 g 后
  才以代 g 发布，且该列每个所需系列都取 ≤ g 的数据——百分比基线绝不混入半列新数据。
- **撤回**：`retractSeries(id)` 将系列移出所需集并丢弃其数据，重算所有受影响列的层
  范围，以 `alternative: true`、版本递增的形式输出替代版本；屏障可能因此释放。
- **隐藏/恢复**：`setHidden(id, …)` 保留其屏障义务但不参与堆叠；恢复后重算并输出
  替代版本。

## 完整矩阵模型（src/matrix.ts）

`buildMatrix` + `stackMatrix` 从当前事件日志物化完整 系列×时间 矩阵并从头堆叠，作为
增量引擎的基准真值。`test/revisions.test.ts` 中的随机化测试在乱序到达、撤回、
隐藏/恢复交错下，逐步比对引擎发布状态与矩阵模型。
