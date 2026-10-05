# 连接顺序优化器（Join Order Optimizer）

接收关系基数与连接选择率，用 **DP over subsets** 枚举全部二叉连接树（bushy，
不限于左深树），在最多 12 张表内求最低成本计划。成本模型包含中间结果代价与
可配置的物化代价，笛卡尔积可配置禁止。

## 成本模型

- 扫描叶子节点代价为 0。
- 每个连接节点的代价 = 中间结果基数 + 物化代价：
  `nodeCost = card + mat.flat + mat.perTuple * card`
- 中间结果基数：`card(S1 ⋈ S2) = card(S1) * card(S2) * Π(跨切分边的选择率)`。
- 计划总成本 = 所有连接节点代价之和。

## 算法

对 `2^n` 个关系子集做动态规划（n ≤ 12）：每个子集枚举所有二分划分
`S = S1 ∪ S2`（用最低位去重），跨划分无边时视为笛卡尔积——
`allowCrossProducts: false` 时剪枝，否则选择率取 1。复杂度约 `O(3^n)`，
n=12 时实测毫秒级。`optimizeLeftDeep` 提供仅左深树的对照枚举。

## 使用

```bash
npm install        # 安装 typescript（仅重新编译需要）
npm run build      # tsc 编译 src/ -> dist/
npm test           # 暴力枚举对照测试（40 组随机查询 + 手工用例）
node dist/cli.js                  # 内置演示：bushy vs 左深树、笛卡尔积开关
node dist/cli.js examples/query.json
```

查询输入为 JSON：

```json
{
  "relations":  [{ "name": "A", "cardinality": 1000 }],
  "predicates": [{ "left": "A", "right": "B", "selectivity": 0.001 }],
  "options": {
    "allowCrossProducts": false,
    "materialization": { "perTuple": 0.1, "flat": 5 }
  }
}
```

同一对关系上的多条谓词选择率会相乘合并。输出为计划树，每个节点标注
估算基数 `card`、本节点选择率 `sel`、本节点代价 `nodeCost`、累计代价
`cumCost`，以及最优总成本与枚举统计。

## 文件

- `src/optimizer.ts` — 核心：`optimize` / `optimizeLeftDeep` / `formatPlan`
- `src/cli.ts` — 命令行入口与演示
- `dist/` — 编译产物（当前为离线手工生成，与 `src/` 一一对应；`npm run build` 可重新生成）
- `test/verify.mjs` — 与穷举全部二叉树的暴力法对照验证
