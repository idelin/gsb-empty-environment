# PASETO页脚密钥选择约束

攻击者能改写PASETO页脚中的kid，诱使服务用另一用途的密钥验证。为Go令牌网关实现v4.public解析包，供请求鉴权调用：footer须为合法JSON且kid只映射到登记为v4.public的Ed25519公钥，PAE覆盖头、载荷、页脚和隐含断言；未知键、重复kid、指向v4.local密钥的kid拒绝。用替换页脚、隐含断言不同和合法令牌验证。

## 实现

`paseto.go` 提供 PASETO v4.public 的严格解析与验签，仅依赖标准库：

- `KeyRegistry`：kid → 带用途标记的密钥。`RegisterPublicKey` 登记
  用途为 `v4.public` 的 Ed25519 公钥；`RegisterLocalKey` 登记
  v4.local 的 kid 占位，使同名 kid 永远不能用于 public 验签。重复 kid
  返回 `ErrKeyExists`。
- `ParseV4Public(token, implicitAssertion)`：鉴权入口。页脚必须存在且为
  仅含一个非空字符串 `kid` 字段的 JSON 对象；未知键、重复 `kid`、非对象、
  非字符串、尾随数据一律 `ErrInvalidFooter`。未登记 kid 返回
  `ErrUnknownKey`，指向 v4.local 等其他用途返回 `ErrKeyPurpose`。
- 验签对 `PAE("v4.public.", payload, footer, implicitAssertion)` 执行
  Ed25519 校验：替换页脚字节或改动隐含断言都会破坏签名，返回
  `ErrInvalidSignature`。页脚原文同时进入 PAE，形成第二道独立防线。
- `SignV4Public` 供令牌签发侧使用，强制执行与解析侧相同的页脚策略。

## 网关调用示例

```go
reg := netsec.NewKeyRegistry()
reg.RegisterPublicKey("signing-key", ed25519PublicKey)
reg.RegisterLocalKey("local-key", localSymmetricKey)

token, err := reg.ParseV4Public(request.Header.Get("Authorization"), implicitAssertion)
if err != nil {
    http.Error(w, "unauthorized", http.StatusUnauthorized)
    return
}
// token.KID / token.Payload 可用于后续鉴权决策
```

## 测试

`go test ./... -race -cover`（当前覆盖率 93.4%）覆盖：合法令牌往返、
替换页脚（指向 v4.local、未知 kid、同 kid 改字节）、隐含断言不同、
载荷篡改、页脚严格性（未知键/重复 kid 等 14 种非法形态）、缺失页脚、
畸形令牌、重复登记与 PAE 结构。
