# testselect

在至多 20 个测试中选出**总成本最小**、能覆盖全部目标故障的测试集。

- 每个测试覆盖一组故障，可声明**前置**（`Requires`，被拉入同样计费）与**互斥**（`Excludes`，任一方声明即双向生效）关系。
- 选择必须满足前置闭包、不含互斥对，并覆盖全部目标故障。
- 成本并列时，按排序后的测试 ID 列表字典序决胜。
- 无法全覆盖时返回 `*UncoverableError`，`Missing` 列出任何合法选择都覆盖不到的故障。

## 用法

```go
sel, err := testselect.Select([]testselect.Test{
    {ID: "a", Cost: 4, Faults: []string{"f1"}},
    {ID: "b", Cost: 3, Faults: []string{"f2"}, Requires: []string{"a"}},
}, []string{"f1", "f2"})
// sel.Tests = ["a", "b"], sel.TotalCost = 7
```

## 实现

`Select` 对 2^n（n ≤ 20）个子集做穷举：前置/互斥并集、成本、覆盖位图均按
“去掉最低位的前驱子集”递推预计算，主循环每子集 O(1) 判定。20 个测试单次求解约 90ms。

测试（`select_test.go`）包含闭包、重复覆盖、互斥、并列决胜、缺失故障的定点用例，
以及 300 组随机用例与独立参照穷举实现的逐一对拍。

```sh
go test ./...
go test -run=NONE -bench=Select20 .
```
