# 布尔规则修复器（boolean-rule-fixer）

处理「前提蕴含结论」形式的布尔约束：当某些规则被违反时，只允许翻转**可修改变量**，
每次翻转都有成本，求使全部规则成立的**最低成本修复方案**。

- 变量总数最多 25 个
- 输出改动清单、总成本，以及每条规则的复核结果
- 无解时输出由不可修改变量导致的**极小冲突核心**（minimal conflict core）

## 运行环境

Node.js ≥ 22.18 或 ≥ 23.6（原生 TypeScript 类型擦除，无需编译）。

```bash
node src/cli.ts examples/repairable.json   # 从文件读取
cat input.json | node src/cli.ts -         # 从标准输入读取
npm test                                   # 运行测试（含随机对拍）
```

退出码：`0` 已满足/修复成功；`1` 无解；`2` 输入非法。

## 输入格式

```json
{
  "variables": { "a": true, "b": false },
  "mutable": { "a": 4, "b": 1 },
  "rules": [
    { "id": "r1", "if": [{ "var": "a", "value": true }], "then": { "var": "b", "value": true } }
  ]
}
```

- `variables`：所有变量的当前取值（≤ 25 个）。
- `mutable`：允许翻转的变量及其成本（非负有限数）；未列出的变量**不可修改**。
- `rules`：蕴含规则列表。`if` 为字面量合取（可含负字面量，`value: false`），
  `then` 为单个字面量。规则被违反 ⟺ 前提全部成立且结论不成立。

## 输出

**有解**（`status: "satisfied" | "repaired"`）：

- `totalCost`：最低总成本；`changes`：每处翻转的变量、前后取值与成本
- `assignment`：修复后的完整赋值
- `verification`：每条规则逐项复核（前提各字面量是否成立、结论期望/实际值、是否满足）

**无解**（`status: "unsatisfiable"`）：

- `conflictCore`：极小冲突核心——仅翻转可修改变量无法同时满足的最小规则子集，
  附各规则在当前赋值下的复核细节
- `immutableVariablesInCore`：核心中涉及的不可修改变量

## 算法

- 赋值编码为位掩码；在可修改变量的翻转子空间上做**分支定界**搜索：
  增量维护各规则的违反状态，下界取「每条被违反规则剩余可翻转变量的最小成本」的最大值，
  并用贪心解作为初始上界。成本非负保证找到全局最优。
- 无解时用**删除式极小化**提取冲突核心：逐条尝试移除规则，若移除后仍不可行则永久移除，
  得到极小不可满足子集。
