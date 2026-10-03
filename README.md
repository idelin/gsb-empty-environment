# 带非时序回溯的 CSP 求解器

这是一个仅使用 Go 标准库实现的有限域二元 CSP 求解器。搜索策略为
Conflict-Directed Backjumping（CBJ）：变量域耗尽时，求解器根据冲突集
定位参与矛盾的最早约束来源中最深的变量，并直接跳到该变量，而不是
机械地返回上一变量。

## 算法要点

- 变量按 `Problem.Variables` 的顺序实例化。
- 每个被拒绝的取值都会记录与其冲突的已赋值变量。
- 子树失败时返回冲突集：
  - 当前变量不在冲突集中：失败与当前变量无关，直接跳过当前层。
  - 当前变量在冲突集中：合并子树带回的其他冲突原因，并尝试当前变量的下一个取值。
- 当前变量域耗尽时，跳到冲突集中索引最大的变量。
- `Stats.Backjumps` 记录跳跃源、目标、冲突集和被跳过的变量。

## 验收场景

`NewTenVariableDemoProblem` 构造 10 个变量：

- `x1` 的域为 `{0}`。
- `x2` 的域为 `{0, 1}`。
- `x3` 至 `x7` 各有 25 个取值。
- `x8` 的域为 `{0, 1}`。
- `x9`、`x10` 的域均为 `{0}`。
- 唯一约束位于 `x2` 与 `x8` 之间：只有 `x2=1` 时，`x8` 才有合法取值。

搜索第一次选择 `x2=0`，随后在第 8 步实例化 `x8` 时发现两个取值都失败。
约束检查只涉及 `x2`，因此冲突集为 `{x2}`。求解器直接从第 8 步跳到第
2 步，跳过第 3 至第 7 步，并将 `x2` 修正为 `1`。

如果采用时序回溯，会为 `x3` 至 `x7` 的 `25^5` 种组合重复检查 `x8`；
CBJ 在该场景中只执行 3 次一致性检查。

## 运行测试

```sh
go test -v ./...
```

本次运行结果：

```text
=== RUN   TestConflictDirectedBackjumpingSkipsIrrelevantVariables
--- PASS: TestConflictDirectedBackjumpingSkipsIrrelevantVariables (0.00s)
=== RUN   TestBackjumpMergesConflictSetsWhileUnwinding
--- PASS: TestBackjumpMergesConflictSetsWhileUnwinding (0.00s)
=== RUN   TestSolveValidatesConstraints
--- PASS: TestSolveValidatesConstraints (0.00s)
PASS
ok  	example.com/backjumpingcsp	0.724s
?   	example.com/backjumpingcsp/cmd/demo	[no test files]
```

## 运行样例

```sh
go run ./cmd/demo
```

输出：

```text
10 变量 CSP 冲突定向回跳演示
约束：x8 仅在 x2=1 时有合法取值；x3 至 x7 与该冲突无关
冲突集：{x2}
回跳：第 8 步 -> 第 2 步（跳过第 3, 4, 5, 6, 7 步）
解：x1=0, x2=1, x3=0, x4=0, x5=0, x6=0, x7=0, x8=0, x9=0, x10=0
统计：赋值 16 次，一致性检查 3 次，拒绝取值 2 个，回跳 1 次
```

## 基本用法

```go
problem := csp.Problem{
	Variables: []csp.Variable{
		{Name: "a", Domain: []int{0, 1}},
		{Name: "b", Domain: []int{0, 1}},
	},
	Constraints: []csp.BinaryConstraint{
		{
			Left:  0,
			Right: 1,
			Allow: func(a, b int) bool { return a != b },
		},
	},
}

result, err := csp.Solve(problem)
```

`Solve` 返回一个解及搜索统计；如果问题无解，`Result.Solved` 为
`false`。
