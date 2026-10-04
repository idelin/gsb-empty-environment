# periodest

周期估计器：处理含未知样本的离散信号，在有限周期范围内寻找最能解释已知点的模式。

## 问题定义

- 输入信号 `signal []int`：已知样本为非负整数，未知样本用 `periodest.Unknown`（`-1`）标记。
- 候选周期范围 `[MinPeriod, MaxPeriod]`，并可用 `MaxAnomalies` 限定允许的异常点数量（负数表示不限）。
- 对每个候选周期 `p`，按相位 `i mod p` 聚合已知样本，取多数值作为模式位；与模式不一致的已知样本计为异常点。
- **未知样本不参与计数，绝不当作 0。**
- 排序目标：**最少异常优先，其次最短周期**。周期按升序扫描、仅在异常数严格更小时替换最优解，因此周期倍数歧义（如 2 与 4 同样完美）总是归约到基波周期。
- 输出 `UnknownPositions`：模式中仍未知的位置（该相位下没有任何已知样本）。全未知输入退化为最短周期、全位置未知。

## 用法

```go
res, err := periodest.Estimate(
    []int{1, periodest.Unknown, 0, 1, 1, 0},
    periodest.Config{MinPeriod: 1, MaxPeriod: 8, MaxAnomalies: -1},
)
// res.Period, res.Anomalies, res.Pattern, res.UnknownPositions
```

## CLI

```sh
echo '{"signal":[1,-1,0,1,1,0],"minPeriod":1,"maxPeriod":8}' | go run ./cmd/periodest
# {"period":3,"anomalies":0,"pattern":[1,1,0],"unknownPositions":[]}
```

## 测试

```sh
go test ./...
```
