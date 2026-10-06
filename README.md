# overflow

Go 容器溢流仿真库。每 tick：

1. **加液**：把 `Tick(adds)` 的外部流入量加入对应容器；
2. **溢流分配**：沿有向无环溢流图的拓扑序，把每个容器超出容量的部分按边上的有理数比例（`math/big.Rat`，精确运算）分配给下游；
3. **排放**：未被任何边分配的部分（`1 - Σfractions`，或汇点的全部溢流）记为排放；
4. **稳定**：图为 DAG，单趟拓扑序遍历即传播到稳定状态（每个容器 ≤ 容量）。

## 守恒不变量

所有运算使用 `big.Rat`，总量精确守恒：

```
Σlevel(tick前) + Σadds == Σlevel(tick后) + discharged(tick)
```

`Network.Discharged` 累计所有 tick 的排放。

## 用法

```go
b := overflow.NewBuilder()
b.AddContainer("a", big.NewRat(10, 1))
b.AddContainer("c", big.NewRat(6, 1))
b.AddEdge("a", "c", big.NewRat(1, 2)) // a 溢流的 1/2 流向 c，其余排放
n, _ := b.Build()                     // 校验比例和 ≤ 1、图无环
discharged, _ := n.Tick(map[string]*big.Rat{"a": big.NewRat(25, 2)})
```

## 测试

`go test ./...` 覆盖：

- **总量守恒**：多 tick 场景每 tick 核对守恒式；
- **逐容器模型核对**：与独立的朴素不动点模拟器（反复扫描直至无流动）逐容器比对水位与排放；
- **多路汇入**：两个父容器同 tick 溢入同一子容器，子容器吸满后再溢出；
- **空容量**：下游剩余容量完全吸收溢流、零排放；
- **级联稳定**：长链单 tick 内收敛；环检测、比例和 > 1、负流入等校验。
