# fmproj — 有理线性不等式投影工具

对有理系数线性不等式组做 Fourier–Motzkin 消去，把指定变量投影掉，
输出剩余变量上的等价约束系统。全程使用 `math/big.Rat` 精确有理运算，
无浮点误差。

## 特性

- **精确消去**：Fourier–Motzkin 配对消去 + 等式高斯代入（等式为一等公民，
  优先用于代入消元）。
- **严格/非严格边界区分**：约束分 `<=`（非严格）、`<`（严格）、`=`（等式）
  三种；消去时严格性沿组合传播——任一父约束严格则结果严格。
- **来源组合证据**：每条输出约束携带 `source`（原始约束下标集合），记录它
  由哪些输入约束组合而成。冗余约束经独立 LP 判定"被其余约束蕴含"后可删除，
  但完整保留在 `removed_redundant` 中（含证据与删除原因：`tautology` /
  `duplicate` / `implied_by_others`）。
- **预算保护**：`-budget`（工作集约束数上限）与 `-maxops`（组合操作数上限）
  任一耗尽时，返回 `status: "budget_exceeded"` 与**精确但未完成的中间系统**
  （`uneliminated_variables` 非空），`equivalent_projection: false`，退出码 2。
  绝不丢弃已生成约束来伪装成"更小的等价结果"。
- **独立核对**（`-verify`，默认开启）：用独立的精确单纯形求解器
  （两阶段、Bland 规则、精确有理数）与小维数有理样点交叉验证：
  1. `feasibility_match`：空集一致性（原系统可行 ⟺ 投影可行）；
  2. `axis_bounds_match`：每个保留变量的上/下确界一致（无界性核对）；
  3. `forward_samples`：原系统可行点（裕度中心 + 目标极值点）投影后满足输出；
  4. `lift_grid`：保留变量空间的有理网格点满足输出 **当且仅当** 能提升回
     原系统可行点（反向包含核对）。

  混合严格/非严格可行性用"裕度法"精确判定：引入 `t`，严格行 `a·x < b`
  放松为 `a·x + t <= b`，最大化 `t`（上界 1），最优值 `> 0` 当且仅当可行。

## 构建与测试

```sh
go build -o fmproj .
go test ./...
```

## 用法

```sh
fmproj -in examples/demo.json -eliminate y,z
fmproj -in sys.json -eliminate y -budget 5000 -maxops 1000000 -verify
cat sys.json | fmproj -eliminate y        # 标准输入亦可
```

### 输入格式

```json
{
  "variables": ["x", "y"],
  "constraints": [
    {"lhs": {"x": "1", "y": "2/3"}, "op": "<=", "rhs": "4"},
    {"lhs": {"y": "1"}, "op": ">", "rhs": "0"}
  ]
}
```

- 系数/右端：整数、小数或 `"p/q"` 分数（精确解析）。
- `op`：`<=`、`<`、`>=`、`>`、`=`。

### 输出格式

约束语义为 `Σ coeffs·vars + const  op  0`，系数已规范化为本原整数向量。

```json
{
  "status": "ok",
  "equivalent_projection": true,
  "feasible": true,
  "kept_variables": ["x"],
  "eliminated_variables": ["y"],
  "constraints": [
    {"coeffs": {"x": "1"}, "const": "-1", "op": "<=", "source": [0, 1]}
  ],
  "removed_redundant": [
    {"constraint": {"coeffs": {"x": "1"}, "const": "-2", "op": "<=", "source": [0, 2]},
     "reason": "implied_by_others"}
  ],
  "stats": {"ops": 2, "rounds": 1},
  "verification": {"ok": true, "checks": [...]}
}
```

- `status`: `ok` | `budget_exceeded`。
- `equivalent_projection` 仅在 `ok` 时为 `true`。
- `feasible: false` 表示消去中推出矛盾（投影为空集），`constraints` 中保留
  矛盾证据约束。
- 预算耗尽时附 `note` 与 `uneliminated_variables`，退出码为 2。

## 代码结构

- `constraint.go` — 约束类型、规范化、组合、求值
- `fm.go` — 消去引擎（代入/FM 配对、预算、冗余删除与证据保留）
- `simplex.go` — 独立精确单纯形 LP（两阶段 + Bland 规则）、混合可行性、闭包优化
- `verify.go` — 投影核对（空集/无界/正反两方向样点）
- `main.go` — CLI 与 JSON I/O
- `*_test.go` — 单元测试 + 随机系统交叉验证（单纯形 vs FM、投影全流程核对）
