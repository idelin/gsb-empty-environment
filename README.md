# 单机排程精确求解器

求解问题 `1 | r_j, s_ij(有向) | min Σ w_j·T_j`，次目标最小化最大完工时间 (makespan)。
任务数 **n ≤ 18**，结果保证全局最优。

## 模型

- 每个任务 `j`：释放时间 `release`、加工时长 `proc`、交货期 `due`、单位迟交罚金 `weight`。
- 换线时间 `setup[i][j]` 有方向性（`setup[i][j] ≠ setup[j][i]`，允许不满足三角不等式）。
- `initialSetup[j]`：机器初始状态到首个任务的换线时间（缺省全 0）。
- `anticipatorySetup`（缺省 `true`）：换线是否可在任务释放前进行。
  - `true`：机器一空闲即换线，换完若任务未释放则等待（等待发生在换线后）。
  - `false`：任务释放后才开始换线（等待发生在换线前）。
- 罚金：`weight × max(0, 完工时间 − due)`，只对迟交计费，提前完工无奖励/惩罚。

## 关于主动空闲

求解器不假设"可执行的任务必须立即开工"：机器可以在释放时间前空闲等待。
由于迟交罚金是完工时间的正则（非减）函数、换线只依赖顺序不依赖时刻，
超出"等待释放"之外的额外空闲不可能降低总罚金，因此 DP 按
`开工 = max(机器空闲 + 换线, 释放时间)` 取值即已覆盖最优空闲决策；
次目标（最早完工）进一步排除了无意义空闲。输出的每段均给出等待与换线时长。

## 算法

子集 DP（Held-Karp 风格）：状态 `(已排任务集合 mask, 末尾任务 last)`。
同一状态下，"罚金低但完工晚"与"罚金高但完工早"的部分解互不支配，
因此每个状态保存 `(完工时间, 累计罚金)` 的 **Pareto 前沿**而非单一最优值，
保证精确性。终态按 `(总罚金, makespan)` 字典序取最优。

- 状态数 ≤ n·2^(n−1)（n=18 时约 236 万），每个状态的前沿通常很小。
- 实测 n=18 随机实例约 40–100 s（Node 16），n ≤ 12 通常秒级。

## 用法

```bash
npm install        # 安装 typescript（仅重新编译需要）
npm run build      # tsc 编译 src/ -> dist/（dist/ 已随附，可跳过）
npm start                          # 运行内置示例
node dist/main.js examples/sample.json     # 求解指定输入
npm test                           # 自检：400 个随机实例与暴力枚举对拍 + n=18 性能
```

## 输入格式 (JSON)

```json
{
  "anticipatorySetup": true,
  "initialSetup": [1, 1, 1, 1],
  "jobs": [
    { "id": "A", "release": 0, "proc": 3, "due": 8, "weight": 2 }
  ],
  "setup": [[0, 2], [3, 0]]
}
```

`setup[i][j]` 为任务 `i` 紧接任务 `j` 时的换线时间，对角线忽略。

## 输出

每段一行：任务、等待（释放前空闲）、换线、加工、开始、完工、期限、迟交、罚金；
末尾给出顺序、总罚金、最大完工、总空闲。

## 文件

- `src/scheduler.ts` — 求解器（子集 DP + Pareto 前沿 + 回溯出段明细）
- `src/brute.ts` — 全排列暴力参照（仅对拍用）
- `src/main.ts` — CLI 与结果格式化
- `src/selfcheck.ts` — 随机对拍与性能自检
- `dist/*.js` — 编译产物（沙箱内无 tsc，为手工生成的等价 CommonJS，可用 `npm run build` 重新生成）
- `examples/sample.json` — 8 任务示例；`examples/idle-demo.json` — 展示释放时间导致的等待段
