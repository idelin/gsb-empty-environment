# 安全多方集合泄漏计量

交集服务只打算暴露交集大小，但分批查询可能定位单个元素。为Go隐私测试宿主实现可导入的推断包，输入有限候选集合和每次子集计数回应，维护全部相容秘密集合，指出首次被唯一确定的成员；最多二十四元素、给定基数，预算耗尽保留上下界。用全子集参考验证负回应和重复查询。

## 推断包 `inference`

导入路径：`example.org/netsec/q0445/inference`

`Reasoner` 在固定候选全集（至多 `inference.MaxElements`，即 24 个元素）和
固定秘密集合基数上，逐条吸收“子集 → 交集大小”的查询回应。

- `inference.New(candidates, cardinality, budget)` 构造推断器；预算以单次
  搜索可检查的候选秘密集合数计，`NewDefault` 使用 `DefaultBudget`。
- `Observe(subset, count)` 录入一次回应，返回 `Result`。相容集合全部放得下
  预算时为 `ModeExact`，显式维护全部相容集合；预算耗尽后切换为
  `ModeBounded`，只保留经搜索证明的逐元素上下界，绝不臆测。
- `Result.NewlyForcedIn` / `NewlyForcedOut` 给出在本次回应中首次被唯一确定
  的成员；`ForcedIn` / `ForcedOut` 是累计结论，`Unknown` 为尚不能定性的元素。
- 同一子集重复查询且计数相同是空操作；计数冲突或回应排除全部候选集合时，
  返回包装了 `ErrInconsistent` 的错误，且后续 `Observe` 继续拒绝。
- `PossibleSets` / `PossibleCount` 仅在 `ModeExact` 下给出精确相容族；
  `Bounds` 返回每个元素的成员关系下界/上界（未知元素为 `[0,1]`）。

### 最小示例

```go
package main

import (
	"fmt"

	"example.org/netsec/q0445/inference"
)

func main() {
	r, err := inference.NewDefault([]string{"a", "b", "c", "d"}, 2)
	if err != nil {
		panic(err)
	}
	res, err := r.Observe([]string{"a", "b"}, 2) // 交集为 2，锁定 a、b
	if err != nil {
		panic(err)
	}
	fmt.Println(res.Mode, res.NewlyForcedIn) // ModeExact [a b]
}
```

## 测试

`inference/inference_test.go` 用全子集枚举作为参考实现，逐条核对精确模式的
相容族与强制成员，并在多档紧预算下用随机差分测试保证有界模式的结论始终是
参考结论的子集（可靠但不臆测）；另含负回应、重复查询、不一致序列、24 元素
上限与基数边界用例。

```sh
go test ./...
```
