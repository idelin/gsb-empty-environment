# provenance

Go 实现的数据来源追踪库（how-provenance，基于溯源半环 N[X]）。

## 核心思想

- 每个基础行在创建时被赋予一个独立符号 `x1, x2, ...`（`NewBaseRelation` + `VarAllocator`）。
- 查询算子在多重集（bag）语义下传播标注：
  - **并集** `Union`：多项式相加；
  - **投影** `Project`：映到同一元组的各行多项式相加；
  - **连接** `Join`：匹配行对的多项式相乘。
- 结果行的标注是一个多项式，同类项始终合并，系数为 `math/big.Int`
  大整数——重复贡献全部累积进系数，绝不退化为来源集合。

例如 `(x1 + x2)² = 2*x1*x2 + x1^2 + x2^2`，系数 2 表示该组合有两份独立见证。

## 删除增量

删除符号为 `x` 的基础行，等价于把输出多项式在 `x = 0` 处求值
（含 `x` 的单项式全部消失）：

```go
newOut, deltas := provenance.DeleteVar(out, varID)
```

`deltas` 给出每个受影响结果行的 `Old / New / Delta = New - Old`（系数 ≤ 0）。
`WithoutVar` 用于从基础关系中移除该行并重算，测试
`TestDeleteVarMatchesDirectRecompute` 对每个基础符号逐一核对：
增量结果与直接重算完全一致。

## 文件

- `poly.go` — 单项式 / 多项式，大整数系数，加减乘、`DropVar` 求值。
- `relation.go` — 行、关系、`Union` / `Project` / `Join` / `Normalize`。
- `delta.go` — 删除增量 `DeleteVar` 与重算辅助 `WithoutVar`。
- `provenance_test.go` — 多重集投影、并集、连接、大系数、删除增量核对。

## 测试

```sh
go test ./...
```
