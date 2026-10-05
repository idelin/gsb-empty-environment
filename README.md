# markov — 有限马尔可夫链分析器

接收有理转移概率与吸收状态集合，精确计算每个状态最终被各闭类吸收的概率
以及到达吸收态的期望步数。所有算术基于 `math/big.Rat`，概率再小（如
`1/10^40`）也不会丢失精度。

## 输入

JSON（文件参数或标准输入），见 `example.json`：

```json
{
  "states": 3,
  "absorbing": [2],
  "transitions": [
    {"from": 0, "to": 1, "prob": "1/3"},
    {"from": 0, "to": 2, "prob": "2/3"},
    {"from": 1, "to": 0, "prob": "1"}
  ]
}
```

- 概率为精确有理数（`"1/3"`、`"0.25"` 均可），每行必须恰好和为 1。
- 吸收状态隐式自环（概率 1），不得再声明出边。

## 输出

- `closedClasses`：所有闭通信类（Tarjan SCC + 闭性判定），标注是否吸收。
- `states[]`：每个状态的
  - `absorptionProbabilities`：到各闭类的精确吸收概率（线性方程组
    `(I - Q) h = r` 的有理解）；
  - `expectedSteps`：期望吸收步数（`(I - Q) t = 1` 的有理解）；若从该状态
    可达某个非吸收闭类，则为 `"Infinity"` —— 绝不把奇异方程硬解成有限值；
  - `reachableClosedClasses`：可达闭类证据（BFS 最短 witness 路径）。
- `verification`：独立代回核验 —— 行和为 1、`h = P h`、边界值、概率落在
  [0,1] 且每态和为 1、`t = 1 + P t`（吸收态边界 `t = 0`）、以及"可达非吸收
  闭类的状态一律为 Infinity"。

## 运行

```sh
go run . example.json        # 或 go build 后 ./markov example.json
go test ./...
```
