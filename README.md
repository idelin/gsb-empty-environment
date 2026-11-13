# EAT 标识长度

`example.org/netsec/q0132` 是供 EAT 验证组件导入的策略校验库。它只负责声明层的策略与新鲜性检查，签名验证应由调用方在解析 EAT 之前或之后完成。

校验规则：

- `ueid` 的长度必须属于允许集合。默认集合为 EAT 规定的固定长度：`7`、`8`、`9`、`15`、`17`、`25`、`33` 字节（均包含类型字节）。可通过 `Policy.AllowedUEIDLengths` 覆盖。
- `Policy.RequireUEID` 为真时，必须存在独立的 `ueid`。仅提供 `sueid` 或 `sueids` 会被拒绝，不会发生回退或替代。
- `sueid` 与 `ueid` 使用相同的格式约束；可用 `Policy.AllowedSUEIDLengths` 单独配置。
- `nonce` 必须非空，并与验证方提供的挑战逐字节相同。比较使用常量时间实现。
- 只有策略、长度和 nonce 全部通过时，`Verify` 才返回 `nil`。

```go
policy := netsec.Policy{RequireUEID: true}

err := netsec.Verify(netsec.Claims{
	UEID:  ueid,
	Nonce: nonce,
}, policy, challenge)
if err != nil {
	// errors.Is(err, netsec.ErrUEIDRequired)
	// errors.Is(err, netsec.ErrUEIDLength)
	// errors.Is(err, netsec.ErrNonceMismatch)
}
```

也可先构造可复用的 `Verifier`：

```go
verifier, err := netsec.NewVerifier(policy)
if err != nil {
	return err
}
err = verifier.Verify(claims, challenge)
```

测试覆盖了错误长度的 `ueid`、仅提供 `sueid`、nonce 不匹配以及合法证明被接受的情形。
