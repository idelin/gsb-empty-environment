# Tetragon 跟踪策略同族调用覆盖校验

本包用于 Go 策略下发服务在保存 Tetragon 跟踪策略前做静态覆盖校验。它把策略钩子、组织要求保护的目标和同族调用表组合起来，报告某个目标经哪些同族调用仍然未被覆盖，并对不安全的路径选择器或 `Override` 配置给出警告或拒绝。

## 快速使用

```go
policy := netsec.Policy{Hooks: []netsec.Hook{
	{
		Call: "sys_openat",
		Args: []netsec.Arg{{Index: 1, Type: "string"}},
		Selectors: []netsec.Selector{
			{ArgIndex: 1, Operator: netsec.OperatorPrefix, Value: "/etc"},
		},
		Action: netsec.ActionSigkill,
	},
}}

targets := []netsec.Target{
	netsec.FileTarget("/etc/shadow", netsec.AccessRead),
}

report, err := netsec.Validate(policy, targets, netsec.DefaultFamilyTable())
if err != nil {
	// 结构错误，策略必须拒绝保存。
}
if !report.Complete {
	// report.Gaps 逐个列出目标与未覆盖的同族调用。
}
```

`Validate` 在需要拒绝策略时返回 `*ValidationError`，同时仍返回完整报告；覆盖缺口只写入 `report.Gaps`，是否据此拒绝保存由下发服务决定。`report.OK()` 仅在结构合规且覆盖完整时为真。

## 判定规则

- **调用名**：`openat`、`sys_openat`、`__x64_sys_openat` 等通过 `CanonicalCall` 归一为同一族调用名。
- **参数**：只把同族调用表中声明的路径参数用于覆盖判定。声明显式为 `int`、`flags` 等非路径类型的参数不会覆盖路径目标。
- **访问方式**：目标必须设置至少一个 `Access`（如 `AccessRead`、`AccessWrite`、`AccessModify`）。目标访问方式与调用路径参数的操作集合取交集；读取目标不会要求覆盖只写或只重命名的调用。
- **目录目标**：`Target.Directory` 为真或以 `/` 结尾时表示递归目录保护。
- **Prefix**：只接受规范化后的路径。`/etc/`、`/etc/./`、`/etc/../etc` 等值会标注 `prefix-not-normalized`，且不用于覆盖。报告始终附带 `prefix-normalized-only`，提示相对路径、dirfd 相对路径和 `..` 拼写可能绕过前缀匹配。
- **Equal**：只与规范化后的绝对目标完全一致；相对选择器不能证明绝对目标的覆盖。
- **Postfix**：可用于普通文件；不能单独用于目录保护，报告会给出 `postfix-directory-protection` 警告和对应缺口。
- **动作**：`Sigkill` 和返回码非零的 `Override` 视为阻断。`Override` 缺少返回码会返回 `override-missing-return-code` 并拒绝策略；返回码为 `0` 会给出 `override-return-zero`，且不视为覆盖。
- **同族调用**：默认表覆盖 open/openat/openat2/creat/truncate/rename/renameat/renameat2。unlink、chmod、xattr、symlink 等调用可通过自定义 `FamilyTable` 加入。

## 报告

`Report.Gaps` 中每一项都包含目标路径、目录语义、访问方式、同族调用名、相关路径参数下标和原因：

```go
type Gap struct {
	Target    string
	Directory bool
	Access    string
	Call      string
	PathArgs  []int
	Reason    string
}
```

`Report.Issues` 中区分 `error` 与 `warning`。结构错误使 `Accepted` 为假；警告用于提示可能放行或可能被绕过的策略写法。

## 验证场景

`coverage_test.go` 覆盖：

- 只钩 `openat` 时仍会报告 `open`、`openat2` 等未覆盖调用；
- 相对路径和含 `..` 的选择器不能覆盖绝对目标；
- 仅使用 `Postfix` 无法保护目录；
- 为全部相关同族调用配置阻断动作后得到完整覆盖；
- `Override` 缺少返回码、返回码为零、返回码非零，以及非路径参数选择器。

运行：

```sh
go test ./...
```
