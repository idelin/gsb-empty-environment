# 有界正则匹配防护

`netsec` 是可嵌入 Go 网关的有界正则匹配器。规则被编译成 Thompson NFA，匹配时只推进状态集，不做回溯；因此 `(a+)+$` 一类模式不会因 `aaaa…!` 输入产生指数级尝试。

## 用法

```go
m, err := netsec.Compile(`(a+)+$`)
if err != nil {
	return err
}

result := m.Match(input, 1<<20)
if result.Blocked {
	switch result.Status {
	case netsec.StatusMatch:
		// 规则命中。
	case netsec.StatusBudgetExhausted:
		// 预算耗尽，按 fail-closed 拦截。
	}
}
```

`Result.Steps` 是本次匹配实际使用的步数，`Result.Budget` 是调用方传入的预算。每一次 epsilon/分支/锚点状态处理以及每一次字符状态检查都计为一步。预算为负数时按 0 处理。

## 结果语义

| 状态 | `Matched` | `Blocked` | `Allowed` | `Exhausted` | `FailClosed` |
| --- | --- | --- | --- | --- | --- |
| `StatusNoMatch` | `false` | `false` | `true` | `false` | `false` |
| `StatusMatch` | `true` | `true` | `false` | `false` | `false` |
| `StatusBudgetExhausted` | `false` | `true` | `false` | `true` | `true` |

预算耗尽时匹配结论未知，因此不会返回“放行”；`Blocked` 和 `FailClosed` 均为 `true`，且 `Steps == Budget`。

## 支持的模式子集

- 字面量、连接（`ab`）、并集（`a|b`）
- 分组（`(...)`）
- 字符类（`[abc]`、`[a-z]`、`[^a-z]`）以及 `\d`、`\s`、`\w` 及其大写形式
- `*`、`+`、`?` 三个量词
- 零宽整串锚点 `^` 和 `$`
- 转义字面量（如 `\.`、`\\`）和 `\n`、`\t`、`\xHH`、`\x{...}`
- `.`（任意非换行 rune，作为额外便利）

未带锚点的模式按子串搜索（`^`/`$` 可分别限制首尾）；`^...$` 表示整串匹配。数字或命名反向引用、重复次数 `{m,n}`、环视和 `(?:...)` 等超出子集的语法会在编译阶段返回 `*SyntaxError`，不会进入热路径。

## 复杂度与验证

状态集推进使最坏工作量为 `O((len(input)+1) * states)`，并由调用方预算硬性截断，不随疑似回溯路径数增长。测试覆盖：

- 64KB `aaaa…!` 恶意输入与 64KB 全 `a` 正常输入；
- 预算恰好等于实际步数、少一步、以及 0 预算的边界行为；
- 输入从 4KB 到 64KB 翻倍时 NFA 步数按线性增长；
- 编译期拒绝反向引用和子集外语法。

本机基准结果（Apple M1 Pro，`go test -bench BenchmarkCatastrophic64KB -benchtime=5x`）：

```text
BenchmarkCatastrophic64KB-10   10   1398325 ns/op   46.87 MB/s   144 B/op   4 allocs/op
```
