# 证据链时间戳重签隔离

取证服务允许为旧证据追加可信时间证明，不能用新时间覆盖原采集时间。写供Go取证宿主调用的签名链包，原证据摘要不可变，重签只追加父摘要、签发时间和签发者；有效性同时校验每代时间不倒退及签发者当时有效。用标准库签名测试旧证据换内容、同时间分叉与过期证书重签，保留完整失败链。

## 使用

链头由原证据的 SHA-256 摘要和原采集时间组成，二者被每一代签名覆盖，`Append` 只追加父摘要、签发时间、签发证书和签名。

```go
issuer := netsec.Issuer{Certificate: cert, Signer: key}

chain, err := netsec.NewChainAt(evidence, capturedAt, issuedAt, issuer)
if err != nil {
	return err
}

chain, err = chain.Append(secondIssuedAt, issuer) // 返回新链，原链不变
if err != nil {
	return err
}

if err := chain.Verify(evidence, netsec.VerifyOptions{Roots: roots}); err != nil {
	return err
}
```

`Verify` 会聚合检查证据摘要、每一代父摘要、时间不倒退、签名和签发证书在**该代签发时**的有效性。失败返回 `*netsec.VerificationError`，`Violations` 保留整个链上的全部问题；`errors.Is` 可匹配 `ErrEvidenceMismatch`、`ErrFork`、`ErrSameTimeFork`、`ErrTimeRegression`、`ErrIssuerExpired` 等。

`Chain` 支持 `json.Marshal` / `netsec.ParseChain` 持久化；解码后仍需调用 `Verify`。同时间分叉可用 `netsec.VerifyGenerations` 对完整代数集合审计。
