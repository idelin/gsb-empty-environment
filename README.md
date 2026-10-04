# provenance

Go 实现的数据来源追踪库（how-provenance，来源半环 ℕ[X]），多重集语义。

- 每个基础行赋独立符号（`BaseRow.ID`），来源表示为符号上的多项式。
- 投影（`Project`）与并集（`Union`）做多项式加法，连接（`Join`，自然连接）做多项式乘法。
- 同类项自动合并，系数为 `math/big.Int`：重复贡献被计数，而不是退化为来源集合。
- `Relation.Delete(sym)` 增量计算删除某输入行后的结果（含该符号的项消失，即代入 sym=0），
  `Relation.DeleteDelta(sym)` 给出每个输出行丢失的贡献；测试中用去掉该行后
  直接重算整个查询来核对增量结果。

## 示例

```go
r := provenance.NewBaseRelation([]string{"A", "B"}, []provenance.BaseRow{
    {ID: "r1", Values: []string{"1", "x"}},
    {ID: "r2", Values: []string{"1", "y"}},
})
s := provenance.NewBaseRelation([]string{"B", "C"}, []provenance.BaseRow{
    {ID: "s1", Values: []string{"x", "u"}},
    {ID: "s2", Values: []string{"y", "u"}},
})
q := r.Join(s).Project("A", "C")
p, _ := q.Lookup("1", "u")
fmt.Println(p) // r1*s1 + r2*s2

after := q.Delete("r1")        // 增量删除 r1：p 变为 r2*s2
deltas := q.DeleteDelta("r1")  // 每个输出行因删除 r1 丢失的多项式
```

## 测试

```sh
GOCACHE=/tmp/gocache GO111MODULE=on go test ./...
```
