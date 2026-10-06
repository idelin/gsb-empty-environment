# overflowsim

Go 容器溢流仿真库。每个 tick:

1. 向每个容器加入其配置的入液量;
2. 沿给定的有向无环溢流图,把超容量部分按有理比例(`math/big.Rat` 精确运算)
   分配给下游容器,未分配部分记为该容器的排放;
3. 按拓扑序反复传播,直到所有容器都不超容量(稳定态)。

总量守恒:`Total() = Σ 当前液位 + Σ 累计排放`,每 tick 恰好增加 Σ 入液量。

## 用法

```go
edges := make([][]overflow.Edge, 3)
edges[0] = []overflow.Edge{{To: 2, Fraction: overflow.Rat(1, 1)}}
edges[1] = []overflow.Edge{{To: 2, Fraction: overflow.Rat(1, 1)}}

n, err := overflow.NewNetwork(3,
    []*big.Rat{overflow.Rat(1, 1), overflow.Rat(1, 1), overflow.Rat(10, 1)}, // 容量
    []*big.Rat{overflow.Rat(3, 1), overflow.Rat(4, 1), overflow.Rat(0, 1)}, // 每 tick 入液
    edges)
if err != nil { log.Fatal(err) }

n.Tick() // 加液并传播到稳定
fmt.Println(n.Levels, n.Discharged, n.Total())
```

建模约束(构造时校验):容量/入液非负,边比例 ∈ [0,1],同一容器出边比例之和 ≤ 1,
溢流图必须为 DAG(有环则报错)。

## 测试

```sh
go test ./...
```

覆盖多路汇入、空容量部分吸收、有理拆分与排放、链式传播至稳定、
多 tick 总量守恒,以及非法模型(环、比例和 > 1)的拒绝。
