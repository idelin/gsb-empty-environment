# quota — 层级配额公平分配库

按树分配整数额度：内部节点有总上限 `Cap`，叶节点有需求 `Demand` 与权重 `Weight`。

## 特性

- **加权最大最小公平**：每个内部节点在兄弟间做注水（water-filling），
  份额 ∝ 权重，且不超过有效需求 `min(Cap, 子树需求之和)`。
- **不丢额度**：理想份额以精确有理数（`big.Rat`）沿树传递，只在叶节点
  向下取整；余数集中按叶编号轮转发放，总额恒等于 `min(根额度, 总需求)`，
  不会逐层取整丢掉可用额度。
- **跨批次游标**：余数轮转游标保存在 `Allocator` 中，多次 `Allocate`
  之间持续轮转，长期看余数公平地落到每个叶子。
- **公平性核验**：`Verify` 检查需求/容量约束、每叶与精确公平份额偏差 < 1、
  总额无丢失。
- **额度缩小重分配**：`SetCap` / `SetDemand` 后再调 `Allocate` 即按新
  约束全树重算，游标继续轮转。

## 用法

```go
root := &quota.Node{ID: 0, Cap: 10, Children: []*quota.Node{
    {ID: 1, Demand: 100, Weight: 1},
    {ID: 2, Demand: 100, Weight: 1},
    {ID: 3, Demand: 100, Weight: 1},
}}
a, _ := quota.NewAllocator(root)

alloc := a.Allocate()        // {1:4, 2:3, 3:3}，余数给叶 1
err := a.Verify(alloc)       // 公平性核验

a.SetCap(0, 7)               // 根额度缩小
alloc = a.Allocate()         // {1:2, 2:3, 3:2}，游标从叶 2 继续轮转
```

## 测试

```sh
GO111MODULE=on go test ./...
```
