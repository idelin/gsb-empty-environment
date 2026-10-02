# Decision Table Optimizer（条件分支判定表优化器）

纯 Go 标准库实现的多条件决策表压缩与冲突检测器：

- **冲突检测**：对 N 维条件网格做两两重叠分析，发现动作矛盾的重叠规则，并输出精确的冲突区域（行号 + 条件交集）。
- **规则压缩**：吸收被包含的冗余规则（subsumption），并把仅差一个维度、动作相同的相邻规则合并为更宽的条件或通配符规则（`*`），迭代至不动点，提升匹配速度。
- **判定树编译**：把压缩后的规则集编译为极小判定树（每层选择区分度最大的维度切分，相邻等效区间自动折叠）。

## 结构

| 文件 | 说明 |
|---|---|
| `cond.go` | 条件抽象：通配符 `AnyCond`、枚举集合 `SetCond`、数值区间 `RangeCond`，支持 Overlaps / Covers / Union / Intersect |
| `table.go` | `Rule` / `Table`、`DetectConflicts`、`Compress` |
| `tree.go` | `BuildTree` 极小判定树编译与渲染 |
| `cmd/demo/main.go` | 10 条业务线规则的端到端运行样例 |
| `decisiontable_test.go` | `go test` 验收测试 |

## 运行

```sh
go test ./... -v     # 运行测试
go run ./cmd/demo    # 运行样例
```

## 运行样例（`go run ./cmd/demo`）

```
=== 1. Original decision table (10 business rules) ===
ID   VIP    Amount       Channel  Action
R1   false  (0, 100]     *        1.0
R2   false  (100, 500]   *        0.95
R3   true   (500, +∞)    *        0.8
R4   true   (100, 500]   *        0.9
R5   true   (0, 100]     *        0.95
R6   false  (500, +∞)    online   0.9
R7   true   (500, +∞)    offline  0.7
R8   false  (500, +∞)    offline  0.9
R9   true   (0, 100]     offline  0.95
R10  false  (500, 1000]  *        0.9
=== 2. Conflict detection ===
CONFLICT: rule 3 (discount 0.8) vs rule 7 (discount 0.7)
          overlap region: VIP=true, Amount=(500, +∞), Channel=offline
=== 3. Fix conflict: align rule 7 with rule 3 (discount 0.8) ===
rule 7: 0.7 -> 0.8
re-check: no conflicts remain
=== 4. Compress rules (merge into wildcard rules) ===
ID  VIP    Amount      Channel  Action
R1  false  (0, 100]    *        1.0
R2  false  (100, 500]  *        0.95
R3  true   (500, +∞)   *        0.8
R4  true   (100, 500]  *        0.9
R5  true   (0, 100]    *        0.95
R6  false  (500, +∞)   *        0.9
rules: 10 -> 6 (Channel dimension fully absorbed into wildcards)
=== 5. Minimal decision tree ===
if Amount in (0, 100]:
  if VIP = true → 0.95
  if VIP = false → 1.0
  else → (no match)
if Amount in (100, 500]:
  if VIP = true → 0.9
  if VIP = false → 0.95
  else → (no match)
if Amount > 500:
  if VIP = true → 0.8
  if VIP = false → 0.9
  else → (no match)
else → (no match)
```

冲突定位：当 `VIP=true 且 Amount>500` 时，规则 3 输出折扣 0.8 而规则 7 输出折扣 0.7，
检测器给出精确冲突行（R3/R7）与重叠区域 `VIP=true, Amount=(500, +∞), Channel=offline`。
修复后压缩将 10 条规则合并为 6 条通配符规则，Channel 维度被完全吸收，判定树仅 6 个叶子。

## 测试结果（`go test ./... -v`）

```
=== RUN   TestDetectConflictsFindsRule3VsRule7
--- PASS: TestDetectConflictsFindsRule3VsRule7 (0.00s)
=== RUN   TestNoFalsePositives
--- PASS: TestNoFalsePositives (0.00s)
=== RUN   TestNoConflictsAfterFix
--- PASS: TestNoConflictsAfterFix (0.00s)
=== RUN   TestCompressMergesToWildcardRules
--- PASS: TestCompressMergesToWildcardRules (0.00s)
=== RUN   TestDecisionTreeMinimal
--- PASS: TestDecisionTreeMinimal (0.00s)
=== RUN   TestRangeCondOps
--- PASS: TestRangeCondOps (0.00s)
=== RUN   TestSetCondOps
--- PASS: TestSetCondOps (0.00s)
=== RUN   TestCompressIsIdempotent
--- PASS: TestCompressIsIdempotent (0.00s)
PASS
ok  	decisiontable
```
