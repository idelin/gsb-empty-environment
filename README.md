# TOML 片段准入

`netsec` 是面向 Go 配置服务的 TOML 1.0.0 片段准入包。适用于“用户片段会合并进主配置”的场景：所有完整键路径必须以区分大小写的 `user` 段开头，防止通过点号键、引号键、转义键或重复表头改写 `[security]` 等主配置表。

```go
import (
	"errors"

	"example.org/netsec/q0499"
)

admitter := netsec.NewAdmitter()

if err := admitter.Admit(userFragment); err != nil {
	if errors.Is(err, netsec.ErrForbiddenRoot) {
		// 返回 400/403；该片段没有改变 admitter 的状态。
	}
}

// 后续片段共享同一份合并状态。
if err := admitter.Admit(nextFragment); err != nil {
	// ...
}
```

单片段校验可使用 `netsec.Validate(fragment)`；跨片段校验必须复用同一个 `Admitter`。

## 准入规则

- 输入先检查 64 KiB 上限，再拒绝非法 UTF-8；随后剥离开头 UTF-8 BOM、将 CRLF 归一为 LF。
- 键路径按 TOML 键语法解析：裸键、基本/字面量引号键、点号键，点号两侧允许水平空白。
- 引号键中的 `\uXXXX`、`\UXXXXXXXX` 等转义先解码，再按段做区分大小写的比较；例如 `"\u0075ser"."a" = 1` 通过，`"\u0073ecurity".x = 1` 拒绝。
- 同一路径重复定义、表头重复、表头与点号键重定义同一张表均拒绝。
- 已闭合的内联表不能再追加键或子表。
- 同一路径的 `[table]` 与 `[[table]]` 混用拒绝；数组表自身可跨片段继续追加元素。
- `Admit` 是事务式的：解析或合并失败不会留下部分修改。

公开错误可通过 `errors.Is` 判断：

`ErrFragmentTooLarge`、`ErrInvalidUTF8`、`ErrInvalidTOML`、`ErrForbiddenRoot`、`ErrDuplicateKey`、`ErrTableRedefined`、`ErrInlineTableAppend`、`ErrArrayTableMismatch`。
