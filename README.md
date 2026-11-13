# JWS未保护头不参与

kid或alg只写在未保护头里，验证仍换密钥或换算法。算法和密钥标识只认受保护头；未保护头的同名值忽略，两边不一致则失败。只在受保护头给出kid和alg的签名接受。可导入的Go库供验证组件调用。两边写入不同的alg。

## 使用

```go
payload, err := netsec.Verify(token, netsec.KeyFunc(func(kid, alg string) (any, error) {
    // kid 和 alg 只来自受保护头。
    return lookupKey(kid)
}))
```

`Verify` 同时接受 compact 和 JSON serialization。返回值是经过签名认证的
payload。需要签名元数据时使用 `VerifyResult`。

验证规则：

- `alg` 和 `kid` 只从受保护头读取；未保护头中的值不参与密钥或算法选择。
- 未保护头只出现 `alg` 或 `kid` 时不会生效；受保护头缺少 `alg` 会验证失败。
- 两个头中出现同名参数且值不同会失败；值相同时忽略未保护头中的副本。
- 受保护头中的 `alg`、`kid` 位于签名保护范围内，因此只在这里给出时正常接受。
- 拒绝 `alg=none`、重复 JSON 参数、未知 `crit` 扩展和不匹配的密钥类型。
