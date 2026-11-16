# ARC封印链实例连续性

攻击者在转发途中插入、删除或调换 ARC 集，想让断链显示为通过。为Go收件端写可导入的 ARC 链校验包：实例号须从1连续到N，每个 i 恰有 AS、AMS、AAR 各一；i=1 的 AS 其 cv 只能是 none，i>1 只能是 pass 或 fail；任一 cv=fail 或最新 AMS 验签失败则整链 fail；AS 签名覆盖 i 及以下全部 ARC 头。验签函数由调用方注入，测缺号、重复、cv 错位和调换。

## `arc` 包

可导入子包路径：`example.org/netsec/q0266/arc`。

校验规则（按顺序执行）：

- 实例号必须从 1 连续到 N，缺号即 fail；
- 每个实例 i 恰有 AAR、AMS、AS 各一个，重复或缺类型即 fail；
- 物理头顺序必须为 `AAR(1), AMS(1), AS(1), AAR(2), ...`，整集插入/删除/调换会在此被拒；
- `AS(1)` 的 `cv` 只能是 `none`，`i>1` 的 `cv` 只能是 `pass`/`fail`；
- 任一 `cv=fail` 立即整链 fail，且不触发任何密码学验签；
- 最新 `AMS(N)` 验签失败即整链 fail；每个 `AS(i)` 都要对覆盖头验签，覆盖集为实例号
  ≤ i 的全部 ARC 头（不含 `AS(i)` 自身），`AMS(i)` 覆盖实例号 < i 的头；
- 密码学验签由调用方通过 `arc.Verifiers` 注入，包本身不依赖任何 DKIM/密钥库。

最小用法：

```go
import "example.org/netsec/q0266/arc"

result, err := arc.Validate(arcHeaderLines, messageBody, arc.Verifiers{
	VerifyAMS: func(ams *arc.Header, covered []*arc.Header, body []byte) error {
		return dkimVerify(ams, covered, body) // 由调用方实现
	},
	VerifyAS: func(seal *arc.Header, covered []*arc.Header, body []byte) error {
		return dkimVerify(seal, covered, body) // 由调用方实现
	},
})
switch result.Status {
case arc.StatusPass:
	// 链可信
case arc.StatusFail:
	// result.Reason / result.FailingInstance 给出失败位置
case arc.StatusNone:
	// 消息未携带 ARC
}
```

结构/策略缺陷返回 `StatusFail` 且 `err == nil`；仅调用方误用（如未注入验签函数）返回非 nil `err`。
测试见 `arc/validate_test.go`，覆盖缺号、缺类型、重复、cv 错位、`cv=fail`、整集调换与内容调换。
