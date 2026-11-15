# ARC 链校验

Go 收件端可导入的 ARC（RFC 8617）链校验包。校验结构规则并调用调用方注入的验签函数，防止转发途中插入、删除或调换 ARC 集后仍显示为通过。

```go
import "example.org/netsec/q0266"

headers := []netsec.Header{
	{Type: netsec.ARCAuthenticationResults, Value: "i=1; mx.example; dkim=pass"},
	{Type: netsec.ARCMessageSignature, Value: "i=1; a=rsa-sha256; b=..."},
	{Type: netsec.ARCSeal, Value: "i=1; a=rsa-sha256; cv=none; b=..."},
}

err := netsec.ValidateChain(headers, func(header netsec.Header, covered []netsec.Header) error {
	switch header.Type {
	case netsec.ARCSeal:
		// covered 按收件顺序包含 i 及之后（instance >= i）的全部 ARC 头，
		// 包括该 AS 自身；canonicalize 时应按 RFC 8617 去掉 b= 值。
		return verifySeal(header, covered)
	case netsec.ARCMessageSignature:
		// covered 为 nil；这是最新 AMS，签名对象是消息正文。
		return verifyLatestAMS(header, messageBytes)
	default:
		return errors.New("unexpected verification request")
	}
})
```

行为：

- 实例必须从 1 连续到 N；每个实例恰有一个 AS、AMS、AAR。
- `i=1` 的 AS 只允许 `cv=none`；`i>1` 只允许 `cv=pass` 或 `cv=fail`。
- 任一 `cv=fail` 直接拒绝整链。
- 每个 AS 都会验签，覆盖集合严格为 `instance >= i` 的 ARC 头。
- 仅最新 AMS 会验签；失败即拒绝整链。
- 注入的验签函数非空，且需自行关闭消息内容、公钥和 canonicalization 上下文。

原始头可用 `ParseHeaderLine` / `ParseHeaderLines` 解析；解析器要求头字段已完成折行展开。
