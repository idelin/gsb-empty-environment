# 查询网关发布准入组件

该 Go 模块实现一个可导入的、带原子回滚的发布准入组件。它面向以下场景：

- 每个未知秘密属于任意合数模域 `Z/(M_j Z)`，`2 <= M_j <= 2^48`，不要求是素数幂。
- 每条待发布线性聚合拥有自己的合数模和带符号整系数。
- 支持跨代仿射映射、线性组合的模区间约束和位切片约束。
- 多个串通组策略同时生效，保护变量/线性量可以重叠。

核心安全判断使用**精确的联合投影像基数**：

```text
priorJointCardinality / postJointCardinality <= MaxShrink
```

实现不会把各单量缩小倍数相乘来代替联合投影基数；相互关联的保护量会按整个
元组计数。

## 导入

```go
import admission "formal.local/h26-20dcb4071f"
```

也可以使用显式子包路径：

```go
import admission "formal.local/h26-20dcb4071f/admission"
```

## 快速示例

```go
cfg := admission.Config{
    Variables: []admission.Variable{
        {Name: "a", Modulus: admission.I(6)},
        {Name: "b", Modulus: admission.I(6)},
    },
    Policies: []admission.CollusionPolicy{{
        Name: "ab",
        Protected: []admission.ProtectedForm{
            {Name: "a", Modulus: admission.I(6),
                Terms: []admission.Term{{Variable: "a", Coefficient: admission.I(1)}}},
            {Name: "b", Modulus: admission.I(6),
                Terms: []admission.Term{{Variable: "b", Coefficient: admission.I(1)}}},
        },
        MaxShrink: admission.Shrink(6, 1),
    }},
}
gate, err := admission.NewGate(cfg)
if err != nil { /* ... */ }

report, err := gate.Admit(&admission.Batch{
    ID: "release-1",
    Releases: []admission.ReleaseRow{{
        ID:      "sum",
        Modulus: admission.I(6),
        Terms: []admission.Term{
            {Variable: "a", Coefficient: admission.I(1)},
            {Variable: "b", Coefficient: admission.I(1)},
        },
        Value: admission.I(0),
    }},
})
```

若 `err != nil`，`gate.State()`、`gate.History()` 和状态摘要均保持调用前不变。
`gate.Preview(batch)` 执行同样的完整检查，但永不提交。

## 约束语义

- `ReleaseRow`：`sum c_i*x_i == Value (mod N)`。每项必须满足
  `N | c_i*M_i`，否则返回 `ErrNonWellDefined`。
- `MappingConstraint`：`Target == Multiplier*Source + Offset (mod N)`；
  `N` 缺省为目标变量模数，并同样执行良定义检查。
- `ModIntervalConstraint`：线性组合的模区间；`Wrap=true` 且 `Low > High`
  表示跨零环绕区间。
- `BitSliceConstraint`：对线性组合的最小非负余数取
  `((r >> Shift) & (2^Width-1)) == Value`。
- `ProtectedForm`：一个受保护的线性量。`Modulus` 为 `nil/0` 表示精确整数值；
  该模式走精确枚举而不是 SNF 快路径。

基线 `Config.Baseline` 表示先验知识。策略的“先验”就是基线可行集的联合投影，
之后所有已准入批次共同参与累计缩小倍数检查。

## 数学实现

1. 所有秘密模和约束模按 CRT 分解为素数幂。
2. 对每个素数 `p`，把该分量写成整数矩阵
   `[A | diag(p^v)]`，其 Smith 标准型不变量为 `d_1 | d_2 | ...`。
3. 该分量的核基数由

   ```text
   |ker| = p^(sum e_j) * product(d_i) / p^(sum v_i)
   ```

   计算；全系统核基数为各素数分量之积。
4. 对保护线性映射 `P`，联合投影基数使用

   ```text
   |P(K)| = |ker Phi| / |ker(Phi, P)|
   ```

   其中第二个核使用齐次化后的线性约束。该式直接给出联合像的基数，
   不是边际基数的乘积。

`AdmissionReport.Certificate` 输出每个素数幂的指数、Smith 不变量、
行列式指数、域/余域/像/核基数，以及策略先验/前态/后态的投影证书。
`Certificate.Verify()` 可只依赖证书本身重新核算所有基数并校验规范摘要；
不变量是 Smith 标准型的规范量，因此证书不依赖消元顺序。

## 精确枚举边界

模区间、位切片以及精确整数保护值会破坏纯子群结构。对这些情况，组件枚举
精确仿射解集，绝不用估计值代替计数。若解集超过 `Config.MaxEnumeration`
（默认 5,000,000），批次会以 `ErrEnumerationLimit` 明确拒绝，状态不变。

## 测试

```sh
go test ./...
```

测试包含：

- 模 6、12、15 的全赋值枚举对照；
- 混合素数幂核与更高次释放模（如 `M=8, N=16`）；
- 联合投影与边际乘积的反例；
- 重叠串通组、累计缩小与整批回滚；
- 模区间、位切片、跨代映射；
- 证书重构、篡改检测与输入顺序无关性。
