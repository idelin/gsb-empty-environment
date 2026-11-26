账务配置与合同生效期表常使用开闭混合区间（如 `[a, b)`、`(a, b]`、无界 `(-inf, b)` 与空区间 `empty`）并附带 `EXCLUDE USING gist (tenant WITH =, valid_during WITH &&)` 排他约束及 SQL:2011 风格的 `UPDATE ... FOR PORTION OF` 区间更新；一旦离散型整型区间与连续型时间戳区间的端点归一化、空区间吞并或相邻区间边界处理出错，就会出现重叠写入漏拦或更新后产生零宽残片。为关系执行器交付可导入的 Go 时态范围代数与约束校验包。组件同时支持离散整数区间（自动规范化为闭开 `[lo, hi)`，`(3, 4)` 归一为空区间）和连续 64 位微秒时间戳区间（保留开闭标志，`(t, t]` 与 `[t, t)` 为空，`[t, t]` 为单点）；支持无界上下界、区间交/并/差/相邻判断、多键等值加范围重叠的排他约束检查（空区间不与任何区间重叠，单点 `[t, t]` 与 `[t, t+1)` 重叠但与 `(t, t+1)` 不重叠）。在执行 `UpdatePortion(key, targetRange, newPayload)` 时，对所有与 `targetRange` 重叠的现有行按差集精确劈分为最多左残片、更新中段和右残片，保留未受影响字段，拒绝产生空残片，并校验结果集满足全部排他约束与外键时态覆盖约束（子表每条非空有效区间必须被父表同键若干相邻或重叠行的并集完全覆盖！）。验收覆盖离散与连续单点边界、无界区间差集劈分、单次区间更新跨越多条旧记录且同时命中相邻合并与外键时态覆盖断裂回滚。

---

## 实现概览

模块路径：`prescreen.local/ps-db7888bb15`，根包名 `temporal`（仅依赖标准库）。

- `range.go`：`Kind`（`Discrete` / `Continuous`）、`Range`、归一化与构造器。
- `algebra.go`：`Intersect`、`Union`、`Difference`、`Adjacent`、`Overlaps`、`UnionAll`、`CoveredBy`。
- `parse.go`：`[a,b)` / `(a,b]` / `(-inf,b)` / `empty` 等写法解析。
- `key.go` / `row.go`：多列等值键 `Key`、行复制、字段保留、相邻同值行合并。
- `constraint.go`：`ExclusionConstraint`（多键等值 + `&&` 重叠）校验。
- `fk.go`：`ForeignKeyCoverage` 外键时态覆盖（相邻/重叠父行并集必须完全覆盖子行）。
- `table.go` / `schema.go` / `global.go`：`Table.UpdatePortion(key, target, payload) error`、
  `Schema`/`Executor` 多表约束、校验失败整体不落盘（回滚）。
- `selftest/`：与验收点一一对应的外部测试；`cmd/demo`：可运行演示。
- `temporal/`、`ranges/`、`trange/`、`rangealgebra/`、`algebra/`、`interval/`、
  `pkg/temporal/`、`internal/temporal/`：同一 API 的再导出兼容包。

## 区间语义

- 离散整数：任意开闭标志先归一化为 `[lo,hi)`；下端开 `(a` → `a+1`，上端闭 `b]` → `b+1`。
  因此 `(3,4)` → `[4,4)` → `empty`，`[3,3]` → `[3,4)`（单点整数 3）。
- 连续微秒时间戳：保留开闭标志；仅当 `lo>hi`，或 `lo==hi` 且至少一端为开时为空；
  `[t,t]` 是非空单点。`[t,t] && [t,t+1)` 为真，`[t,t] && (t,t+1)` 为假。
- 无界：`LowerUnbounded`（`-inf`）/ `UpperUnbounded`（`+inf`），如 `(-inf,5)`、`[10,+inf)`。
- 相邻：不重叠且并集为单一区间。离散 `[a,b)` 与 `[b,c)` 相邻；连续情形要求公共边界至少被一侧包含
  （`[a,b]` 与 `(b,c]` 相邻，而 `[a,b)` 与 `(b,c]` 因点 `b` 失覆盖而不相邻）。

## 快速上手

```go
import temporal "prescreen.local/ps-db7888bb15"

tbl := temporal.NewTable("account_config", "tenant")
_ = tbl.Insert(temporal.Row{
    Key: temporal.Key{"tenant": 1},
    Range: temporal.NewIntRange(0, 50), // [0,50)
    Payload: "v1",
})

// SQL:2011 风格 FOR PORTION；失败返回 errors.Is(err, temporal.ErrOverlap / ErrCoverage)
// 且表内容保持调用前状态（copy-on-validate + 原子提交 = 回滚）。
err := tbl.UpdatePortion(
    temporal.Key{"tenant": 1},
    temporal.NewIntRange(20, 30),
    "v2",
)
```

外键时态覆盖：

```go
schema := temporal.NewSchema()
parent := schema.CreateTable("contract", "tenant", "cid")
child  := schema.CreateTable("entry", "tenant", "cid")
schema.AddForeignKey(temporal.ForeignKeyCoverage{
    ChildTable: "entry",  ChildColumns: []string{"tenant", "cid"},
    ParentTable: "contract", ParentColumns: []string{"tenant", "id"},
})
// 父表同键相邻行 [0,50)、[50,100) 的并集覆盖子表 [0,100)。
_ = schema.Validate()
```

`UpdatePortion` 对每条命中行计算 `mid = old ∩ target`、左右残片 = `old − target`，
空片直接丢弃（绝不落库零宽残片），残片完整复制 `Fields`/`Payload`；随后可选合并相邻同键同值段，
最后对候选结果执行空区间、排他约束与全部 FK 覆盖校验，任一失败则不替换已提交行集。

## 验证

```bash
go test ./...          # 全部单元/验收/性质测试与兼容包编译
go run ./cmd/demo      # 端到端演示（含回滚）
```

验收点对应：

- 离散归一化与单点：`selftest/acceptance_test.go` 的 `TestAccDiscreteNormalization`。
- 连续单点边界：`TestAccContinuousPointBoundaries`。
- 无界差集劈分：`TestAccAlgebra`、`TestAccUpdatePortionUnboundedSplit`。
- 跨多条旧记录 + 相邻合并 + FK 断裂回滚：`selftest/headline_test.go`
  的 `TestAccHeadlineSpanMergeAndFKRollback`。
- 排他/外键回滚：`TestAccExclusionConstraint`、`TestAccFKCoverageAndRollback`、
  `TestAccExclusionRollback`。
- 代数性质（2 万组随机集合模型比对）：`TestAccPropertyAlgebra`。
