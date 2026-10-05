# 多项目发布规划器（release-planner）

在依赖约束与每批容量上限下，计算**批次数最少**的多项目发布计划，
输出同批原因与不可行见证。项目数上限 18。

## 问题建模

- 项目 A 依赖项目 B（`A.deps` 含 `B`）⇒ B 必须在**严格更早**的批次发布
  （依赖方同步发布要求：发布 A 时其依赖必须已经就绪）。
- **依赖环允许同批发布**：环上项目互相依赖、无法严格排序，唯一可行方式是
  把同一个强连通分量整体放进一批，分量绝不能拆到两批。
- 每批项目数不超过 `capacity`，目标为最小化批次数。
- 唯一不可行情形：某个依赖环的大小超过每批容量。此时输出见证：
  环所在的强连通分量 + 一条具体的依赖环路径。

## 算法

1. **Tarjan 强连通分量**（`src/scc.ts`）：把依赖环缩点，得到凝聚 DAG。
   分量大小 > 容量 ⇒ 不可行，用 DFS 在分量内找出一条具体依赖环作为见证。
2. **位掩码记忆化搜索**（`src/optimize.ts`）：状态为「已完成分量集合」
   （≤ 2^m，m ≤ 18）。每步从可安排集（前驱全部完成的分量）中枚举一个
   **极大容量可行子集**作为下一批——若 S ⊂ S′ 均可行，选 S′ 不会更差
   （已完成集合越大后续越不受限，批次数相同），故极大子集足够。
   剪枝：容量下界 `ceil(剩余/容量)`、最长链下界，达到下界即提前终止。
3. **暴力参考解**（`src/brute.ts`）：枚举批次数 k 并回溯验证可行性，
   仅用于核验。

## 用法

```bash
# 运行 CLI（JSON 输入，文件或标准输入）
node scripts/tsrun.js src/cli.ts examples/sample.json
node scripts/tsrun.js src/cli.ts examples/sample.json --text   # 人类可读
cat examples/sample.json | node scripts/tsrun.js src/cli.ts

# 全部核验
npm test
```

输入格式：

```json
{
  "capacity": 2,
  "projects": [
    { "id": "api", "deps": ["auth"] },
    { "id": "auth", "deps": [] }
  ]
}
```

输出：`ok: true` 时给出 `batchCount` 与每批的 `projects` 及 `reasons`
（`cycle`：环上项目必须同批；`capacity`：容量约束下为最少批次数而同批）；
`ok: false` 时给出 `witness`（不可行见证：分量、具体依赖环、大小、容量）。

## 核验

`npm test` 依次执行：

- **transpile-check**：全部 `.ts` 经内置转译器转译并通过 `node --check`；
- **unit**：8 组手工用例（链、环、菱形、自环、非法输入、见证字段等）；
- **exhaustive**：穷举 n ≤ 4 的全部有向图（另含 n ≤ 3 带自环），
  对容量 1..4 共 **18,780** 个实例，逐一与暴力解比较批次数，
  并独立校验计划合法性（容量、覆盖、严格先后、同环同批）与见证有效性
  （环边真实存在、分量强连通、大小超容量）；
- **random**：3,000 个随机实例（n ≤ 10）与暴力解对拍；
  200 个 n = 18 实例做合法性与性能抽查（单例最大耗时约 60ms）。

## 关于离线运行

本环境无网络，无法安装 `typescript` 包，因此 `scripts/tsrun.js` 实现了一个
针对本仓库「可擦除类型」子集的转译器（类型标注空白化，保留行号），
用于直接运行 `.ts` 源码与测试。源码是标准 TypeScript，在有网环境下可
`npm install --save-dev typescript && npx tsc --noEmit` 做完整类型检查
（`tsconfig.json` 已备好，strict 模式）。
