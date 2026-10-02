# CSP 求解器：非时序回溯（Conflict-directed Backjumping）

TypeScript 实现的智能回溯约束求解引擎。搜索遇到死胡同时，不回退到上一变量，
而是根据**冲突集（Conflict Set）**定位导致矛盾的最深根源变量，直接跳跃回溯。

## 算法

Conflict-directed Backjumping（Prosser, 1993），见 `src/csp.ts`：

- 每个变量层维护 conflict set：使其取值失败的历史变量集合；
- 某层域耗尽时，取 conflict set 中**最深**的变量作为回跳目标；
- 回跳时把 conflict set 的其余成员合并进目标层的 conflict set，保留冲突信息；
- 中间层（被跳过的变量）状态全部重置，其无用分支被整体剪除。

## 验收场景

`src/demo-problem.ts` 构建 10 变量 CSP：X8 域为 `{1}`，关键约束 `X8 ≠ X2`，
X3..X7 无任何约束。搜索按序赋值，第 8 步 X8 域耗尽；冲突分析得出
conflict set = `{X2}`，求解器直接跳过第 3~7 步回跳到第 2 步修改 X2。

## 运行

无需安装依赖（Node ≥ 22.6，利用原生 TypeScript 类型擦除）：

```bash
npm run demo   # 运行样例：CBJ vs 时序回溯对照
npm test       # Node.js 内置测试（node:test）
```

## 样例输出

```
=== Conflict-directed Backjumping (CBJ) ===
访问节点数: 18
回跳记录:
  第 8 步(X8) -> 第 2 步(X2)，跳过 5 层；conflict set = {X2}
解: [1,2,1,1,1,1,1,1,2,1]

=== Chronological Backtracking (对照) ===
访问节点数: 618
解: [1,2,1,1,1,1,1,1,2,1]

剪枝效果: 时序回溯访问 618 个节点，CBJ 仅访问 18 个节点，减少 97.1%
```

## 文件结构

- `src/csp.ts` — CBJ 求解器 + 时序回溯对照实现
- `src/demo-problem.ts` — 10 变量验收问题
- `src/demo.ts` — 运行样例
- `test/cbj.test.ts` — 8 个 node:test 测试（回跳层级、冲突集内容、剪枝比例、不可解判定等）
