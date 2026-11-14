# DKIM正文长度让恶意尾部进入邮件

邮件过滤器把DKIM验证通过视为整封正文可信，但攻击者可利用`l=`只签前缀并在尾部追加HTML或MIME边界。请实现供Go邮件网关调用的可导入DKIM覆盖判定包，在流式接收时同时执行simple/relaxed正文规范化，精确计算签名`l`覆盖的规范字节与原始MIME字节依赖。验证签名后返回已认证范围映射和安全策略决定：高信任通道可禁止`l`，兼容通道必须证明未覆盖尾部只含规范化丢弃的空行；任何可见字节、MIME边界、编码数据或解析不完整均不得标为整封已认证。多签名各自保留domain、selector、算法和覆盖，不用较短有效签名覆盖较长失败。用尾部HTML、边界续接、quoted-printable软换行、空行、分片CRLF和合法全覆盖验证界面不会夸大认证范围。

## 使用方式

调用方先完成 DKIM 密码学验证，再把验证结论交给本包判定正文认证范围：

```go
receiver := netsec.NewBodyReceiver()
for _, chunk := range chunks { // 可以任意切分，包括切在 CRLF 中间
	_, _ = receiver.Write(chunk)
}
views, _ := receiver.Finish()

claims := []netsec.SignatureVerification{
	{Header: dkimHeader, Verified: dkimVerifierResult},
}
assessment, _ := netsec.AssessViews(views, claims, netsec.PolicyCompatibility)
if assessment.Decision == netsec.DecisionAuthenticated {
	// 可见正文已由某一个完整签名覆盖，尾部没有未认证的可见字节。
}
```

也可以对已经完整缓冲的正文调用 `AnalyzeBody`。`BodyReceiver` 在一次接收中同时生成
`BodyViews.Simple` 和 `BodyViews.Relaxed`，每个结果都带 canonical-to-raw 映射。

## 判定规则

- `l=` 始终作用于 canonicalized body 的前 `l` 字节；`PrefixCoverage.RawEnd` 和
  `RawDependencies` 给出对应的原始 MIME 字节依赖。
- `PolicyHighTrust` 不接受 `l=`，即使它恰好覆盖当前 canonical body。
- `PolicyCompatibility` 只有在 `l == canonical body length` 且未覆盖的原始尾部
  `TailOnlyDiscarded` 为真时才认为整封正文可信。
- 未完成流、`l` 超出 canonical body、未验证签名、可见尾部字节、MIME 边界和编码
  数据都不会产生 `DecisionAuthenticated`。
- `Assessment.Signatures` 按输入顺序保留每个签名的 domain、selector、算法、正文
  规范化方式和覆盖；`Assessment.Authenticated` 使用包含序号的唯一键，因此同一
  domain/selector 的较短有效签名不会覆盖较长失败签名的记录。
