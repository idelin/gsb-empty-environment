# 安全栈帧大小证明

该包为 Go 宿主提供固定帧长调用图的最坏栈分析。每个函数有一个整数参数
`n`，调用边用 `Delta` 计算被调参数（递归边通常为负），并可用 `Guard`
限定调用者参数范围。多个候选调用视为控制流分支；某个调用返回后，其帧全部
释放，因此兄弟调用取最大值而不是求和。

默认把入口参数视为非负深度预算：调用者和被调者参数都为非负时才考虑该边。
因此严格递减的环会在预算耗尽时终止。若要分析可为负的任意整数参数，设置
`Options.AllowNegativeParameters: true`；此时每个环还必须包含最终会失效的
下界守卫（`>`、`>=` 或 `==`）。

## 语义

- `CallNormal`：调用者帧在子树执行期间保持存活。
- `CallTail`：调用者帧在进入被调者前释放，尾调用链只保留当前帧。
- 递归环必须满足所有环的参数变化总和严格为负；存在零或正变化环时返回
  `StatusUnknown`，不会给出虚假的有限上界。默认的非负预算使递减环良基；
  任意整数模式还会检查环的终止守卫。
- `Analyze` 返回入口调用的最坏同时存活字节数、每个可达调用点的传递需求
  （不只是单帧），以及一条达到该峰值的调用路径。

## 示例

```go
p := netsec.NewProgram("walk").
	Function("walk", 128).
	CallWith("walk", netsec.CallNormal, -1, netsec.Guard{Op: netsec.OpGreater, Value: 0}).
	Build()

r := netsec.Analyze(p, 4)
// r.MaxStack == 128*5, r.Path[0].Function == "walk", r.Known == true
```

尾调用版本只需把 `CallNormal` 改为 `netsec.CallTail`，最坏栈保持为单帧；
见证路径仍会展开全部递减调用，便于核对释放语义。

## 宿主接口

- `Analyze` / `AnalyzeStrict`：泛型入口参数并可直接返回错误。
- `Check` / `AnalyzeProgram`：非泛型入口，适合动态加载的宿主适配层。
- `Options.StackLimit`：给出宿主栈容量后，结果和每个路径调用点都会标记是否
  放得下。
- `Options.MaxStates`：限制展开状态数，超过后按未知处理。
