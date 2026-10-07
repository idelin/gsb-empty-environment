# iflow — 脚本 IR 信息流分析库

对一个小型脚本 IR 做前向流分析：每个变量携带安全标签（有限格等级 +
允许接收者集合），赋值把表达式标签与当前控制依赖（pc）标签合并，
在每个 sink 处判定该流是否被允许，并给出最短传播链。

## 模型

- **有限显式格**：`NewLattice(names, covers)` 由覆盖关系构造，传递闭包后
  校验反对称性与唯一最小上界；不是格的声明会在构造时报错。
- **标签**：`Label{Level, Receivers}`。序：`l <= o` 当且仅当等级更低且
  接收者集合更大（更不受限）。Join = 等级取格上 join、接收者集合取交。
- **赋值合并**：`x := e` 后 `label(x) = join(label(e), pc)`。
- **pc 标签单调**：进入分支把条件标签并入 pc；**分支结束不自动丢弃 pc
  依赖**。这是有意的保守近似（见"限制"）。
- **降密**：只能发生在显式 `Declass` 节点，且
  1. `auth` 必须在 `Program.Authorizations` 中；
  2. 新标签必须是旧标签的"缩小"（等级不升、接收者只增）。
  违反任一条则拒绝并保持原标签，记入 `Report.Violations`；每次尝试
  （无论成败）都记入 `Report.Declass`，溯源图中保留降密节点（来源、
  原标签、新标签、授权）。
- **sink 判定**：`label(expr) join pc` 允许该接收者则放行；否则拒绝。
  两种情况下都输出从带标签输入到该 sink 的最短传播链（BFS）。

## 用法

```go
lat, _ := iflow.NewLattice(
    []string{"Public", "Internal", "Secret"},
    [][2]string{{"Public", "Internal"}, {"Internal", "Secret"}},
)
prog := &iflow.Program{
    Inputs: map[string]iflow.Label{"secret": iflow.NewLabel(lat, "Secret")},
    Body: []iflow.Instr{
        iflow.Assign("x", iflow.V("secret")),
        iflow.Sink("out", "public", iflow.V("x")),
    },
}
rep := iflow.Analyze(prog, lat) // rep.Sinks[0].Allowed == false, Chain 给出传播链
```

运行演示：`GO111MODULE=on go run ./cmd/iflowdemo`

## 两运行观察对照

`Run` 是具体解释器，`CompareRuns` 比较两次运行在 sink 处的观测。
测试（`tworun_test.go`）用它独立验证简单泄漏例：显式泄漏与分支隐式
泄漏在两组不同秘密输入下产生可区分的 sink 输出，与静态分析的拒绝
判定相互印证。

## 限制（明确不声称的内容)

- **不声称完整检测所有非干扰性**。pc 标签单调不降是有意的过近似：
  分支结束后给常量赋值也会被污染（见演示最后一例：两运行不可区分，
  但分析仍拒绝）。分析是保守的，可能误报。
- 两运行对照只是**该输入对**的动态证据：观测到差异可证明这对输入可
  区分，观测不到差异不能证明程序非干扰。
- 仅覆盖本 IR 的结构（整数表达式、结构化分支、显式 sink）；无循环、
  无过程调用、无指针别名。循环若引入需自行保证终止性。
- 只在单分支出现的变量在合并点按"另一分支保持原值"处理。
