# unitinfer

单位推导器：用基本量的整数指数向量表示单位，对表达式做单位约束推导，
输出最一般约束解，或在不可满足时输出最小冲突表达式集合。

## 表示

- 单位 = 基本量（维度）上的整数指数向量，如 `L^2*M*T^-2`。
- 单位项 `Term` 对单位变量是仿射线性的：`Const + Σ cᵢ·Xᵢ`。
  乘除对应项的加减，整数幂对应数乘，因此所有约束都化为
  整数线性方程组 `A x = b`（每个单位变量 × 每个基本量一个未知数）。
- 加减法（`Add`/`Sub`）生成两侧单位相等的约束；`Mul`/`Div`/`Pow`
  为纯项运算，不产生约束。
- 函数签名 `Signature` 可含签名局部单位变量（如 `square = ∀a. a -> a^2`），
  每次调用实例化出全新变量并生成参数/结果约束。

## 求解

- `matrix.go`：列式 Hermite 标准形（HNF）。由 `A·U = [B 0]` 同时得到
  特解（`B y = b` 逐行整除求解）与整数核的 Z-基（`U` 的零列部分），
  从而给出**最一般解** `x = x₀ + Σ λₖ·bₖ (λₖ ∈ Z)`。
- 欠定变量保持显式的自由方向（`Solution.Basis`），绝不默认成无量纲。
- 不可满足时，`MinimalConflict` 用删除式极小化给出子集极小的冲突约束集
  （每条约束都标注了产生它的表达式位置）。

## 验证

`Infer.Verify(sol, lo, hi)` 在小指数域 `[-K,K]` 上枚举全部整数解，
与符号解做双向包含检查：

1. 每个枚举解都是符号解的实例（`x - x₀` 落在核基的 Z-张成内）；
2. 每个声明的自由方向都是真的（盒内沿 `±bₖ` 走一步仍是解）；
3. 符号解声明为固定的坐标，在所有枚举解中必须取同一值——
   这正是"欠定问题被随意设成无量纲"会触发的失败。

`solve_random_test.go` 进一步对随机整数系统做求解器 vs 枚举的交叉验证。

## 用法

```go
in := unitinfer.NewInfer(3, []string{"L", "M", "T"})
t := in.Gen(unitinfer.Mul{L: unitinfer.Var{Name: "x"}, R: unitinfer.Var{Name: "y"}})
in.ConstrainEq(t, unitinfer.ConstTerm([]int{1, 0, 0}), "product is a length")
res := in.Solve()
// res.OK == true:  res.Solution 为最一般解（含自由方向）
// res.OK == false: res.Conflict 为最小冲突约束集
```

运行测试与演示：

```sh
go test ./...
go run ./cmd/demo
```
