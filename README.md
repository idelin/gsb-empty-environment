# decisiontable — 条件分支判定表优化器

多条件决策表压缩与冲突检测器（Go 标准库实现，无第三方依赖）。

## 功能

- **规则网格建模**：N 个条件维度（布尔 / 数值区间 / 枚举集合），每维度支持通配符 `Any()`。
- **冲突检测**：O(n²) 逐维度求交，发现重叠且动作矛盾的规则对，并精确输出重叠区域（如 `VIP=true 且 金额>500`）。
- **压缩合并**：反复合并"动作相同、仅一个维度不同且该维度约束可合并（集合并集 / 连续区间）"的规则对至不动点，生成通配符规则，减少匹配扫描行数。
- **判定树编译**：把压缩后的无冲突规则集编译为判定树（`BuildTree`），匹配从线性扫描降为逐维度分支跳转（`Evaluate`）。

## 结构

| 文件 | 说明 |
|---|---|
| `constraint.go` | `Constraint`/`Interval`：区间交并、集合约束、可读化 |
| `table.go` | `Table`/`Rule`：冲突检测 `Conflicts()`、压缩 `Compress()`、线性匹配 `Match()` |
| `tree.go` | 判定树：`BuildTree()` / `Evaluate()` / 树形打印 |
| `cmd/demo/main.go` | 验收场景演示：10 条业务线规则 |
| `decisiontable_test.go` | 全部测试（go test） |

## 运行

```sh
go run ./cmd/demo   # 运行样例
go test ./...       # 运行测试
```

## 运行样例（`go run ./cmd/demo`）

```
========== 1. 原始判定表 (10 条业务线规则) ==========
  规则1: VIP=true 且 金额≤100 => 0.95
  规则2: VIP=true 且 金额∈(100, 500] => 0.9
  规则3: VIP=true 且 金额>500 => 0.8
  规则4: VIP=false 且 金额≤100 => 1.0
  规则5: VIP=false 且 金额∈(100, 500] => 0.95
  规则6: VIP=false 且 金额>500 => 0.9
  规则7: VIP=true 且 金额>500 => 0.7
  规则8: VIP=true 且 金额≤100 且 渠道=online => 0.95
  规则9: VIP=false 且 金额∈(100, 500] 且 渠道=mobile => 0.95
  规则10: VIP=true 且 金额∈(100, 500] 且 渠道=online => 0.9

========== 2. 冲突检测 ==========
  冲突: 规则3(动作=0.8) 与 规则7(动作=0.7) 重叠于 [VIP=true 且 金额>500]

========== 3. 修复冲突 ==========
  业务裁定: VIP=true 且 金额>500 统一执行折扣 0.8，将规则7动作 0.7 -> 0.8
  复检: 冲突数 = 0 ✓

========== 4. 压缩合并 (相同结果的相邻网格 -> 通配符规则) ==========
  规则数: 10 -> 6
  规则1: VIP=true 且 金额≤100 => 0.95
  规则2: VIP=true 且 金额∈(100, 500] => 0.9
  规则3: VIP=true 且 金额>500 => 0.8
  规则4: VIP=false 且 金额≤100 => 1.0
  规则5: VIP=false 且 金额∈(100, 500] => 0.95
  规则6: VIP=false 且 金额>500 => 0.9

========== 5. 压缩后的极小判定树 ==========
按 VIP 分支:
├── VIP=false, 按 金额 分支:
│   ├── 金额≤100 => 1.0
│   ├── 金额∈(100, 500] => 0.95
│   └── 金额>500 => 0.9
└── VIP=true, 按 金额 分支:
    ├── 金额≤100 => 0.95
    ├── 金额∈(100, 500] => 0.9
    └── 金额>500 => 0.8
  (叶子节点数: 6)

========== 6. 匹配验证 (判定树 vs 线性扫描) ==========
  map[VIP:true 渠道:online 金额:600]   判定树=0.8 线性扫描=0.8 ✓
  map[VIP:true 金额:500]              判定树=0.9 线性扫描=0.9 ✓
  map[VIP:true 金额:80]               判定树=0.95 线性扫描=0.95 ✓
  map[VIP:false 渠道:mobile 金额:300]  判定树=0.95 线性扫描=0.95 ✓
  map[VIP:false 金额:999]             判定树=0.9 线性扫描=0.9 ✓
```

## 测试结果（`go test ./... -v`）

```
=== RUN   TestConflicts_FindsRule3VsRule7   # 精确定位 规则3(0.8) vs 规则7(0.7) @ VIP=true 且 金额>500
--- PASS: TestConflicts_FindsRule3VsRule7
=== RUN   TestConflicts_AfterFix            # 修复（改动作/删规则）后冲突归零
--- PASS: TestConflicts_AfterFix
=== RUN   TestCompress                      # 10 -> 6 条，压缩前后 2000 组随机输入语义等价
--- PASS: TestCompress
=== RUN   TestCompress_Idempotent           # 压缩幂等
--- PASS: TestCompress_Idempotent
=== RUN   TestMerge_BoolWildcard            # 布尔维合并为通配
--- PASS: TestMerge_BoolWildcard
=== RUN   TestMerge_NumberAdjacent          # 相接区间合并、空隙区间不合并
--- PASS: TestMerge_NumberAdjacent
=== RUN   TestInterval_Basics               # 区间交/并/边界
--- PASS: TestInterval_Basics
=== RUN   TestTree_Evaluate                 # 判定树边界值 + 2000 组随机输入对拍线性扫描
--- PASS: TestTree_Evaluate
=== RUN   TestTree_EnumBranch               # 枚举分支与默认分支
--- PASS: TestTree_EnumBranch
PASS
ok      decisiontable
```
