# Backup Policy Interpreter

TypeScript 实现的备份规则解释器：按优先级为每个文件选择动作（`backup` / `exclude`），
支持路径、大小、修改版本、标签四类条件，提供静态冲突检查、三值语义（unknown 不默认排除）、
增量策略更新（只重算可能受影响文件，输出新增下载与撤销计划），并以全扫描参考做核对。

## 模块

- `src/types.ts` — 规则、条件、文件事实、决策等类型；事实字段可缺失（未知）。
- `src/conditions.ts` — glob 匹配与条件的三值求值（`true | false | 'unknown'`）。
- `src/interpreter.ts` — 逐文件解释（`interpret`）与静态冲突检查（`checkPolicy`）。
- `src/engine.ts` — `PolicyEngine`：持有策略与每文件评估记录，支持增量 `updatePolicy`
  与 `verifyAgainstReference` 全扫描核对。

## 语义要点

- **优先级**：多条规则确定为真时，优先级最高者生效。
- **三值逻辑**：文件事实缺失（如无 `tags`）时相关条件为 `unknown`；若最高优先级的
  未定规则高于所有确定为真的规则，则整体结果为 `unknown`，**不默认排除**。
- **边界**：`size` / `version` 的 `min` / `max` 均为闭区间。
- **否定条件**：`pathNot`（路径不得匹配）、`tagsNone`（不得含任一标签）。
- **静态检查**：重复 id、自相矛盾规则（永不可满足）、同优先级重叠且动作不同（歧义，error）、
  高优先级完全覆盖低优先级（遮蔽，warning）。
- **增量更新**：按规则 id diff（新增/删除/修改），仅重算「引用了被删/改规则」或
  「可能匹配新增/修改规则」的文件；输出 `downloads`（新增备份）、`revocations`
  （撤销备份）、`unknowns`、`recomputed`。
- **参考核对**：`verifyAgainstReference()` 用全扫描从头重算并比对增量状态。

## 构建与测试

需要本机可用的 `tsc`（TypeScript 4.5+）：

```sh
tsc -p tsconfig.json && node dist/test/run.js
```

测试覆盖：优先级选择、标签缺失保持 unknown、大小/版本边界、否定条件、静态冲突
（歧义/遮蔽/不可满足/重复 id）、增量重算集合最小性、下载与撤销计划、
以及随机化差分测试（增量状态 vs 全扫描参考）。
