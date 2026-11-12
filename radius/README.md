# radius — RADIUS 客户端请求构造与应答校验

面向不可信网络中的 RADIUS 接入客户端，按 RFC 2865 / RFC 2869 / RFC 3579
实现 Access-Request 的发送编码与 Access-Accept/Access-Reject/Access-Challenge
的应答校验，防御通过可控 User-Name 诱导代理插入 Proxy-State 后伪造应答的攻击。

## 安全模型

- 每个 Access-Request 都带 Message-Authenticator（属性 80），且固定为属性区
  第一个属性；值为 `HMAC-MD5(shared-secret, 整包且 MA 值清零)`（RFC 2869 §5.14）。
- 应答必须同时通过两道密码学校验：
  - Message-Authenticator：`HMAC-MD5(secret, 应答整包且头部 Authenticator 替换为
    请求 Request Authenticator、MA 值清零)`（RFC 3579 §3.2）；
  - Response Authenticator：`MD5(Code||ID||Length||RequestAuth||Attributes||Secret)`
    （RFC 2865 §4.2）。
- Message-Authenticator 缺失、不在首位、重复或值长不是 16 字节，一律拒绝。
- 应答 Proxy-State（属性 33）必须与请求发出的值数量相同、按序、逐字节一致；
  附加、缺失或改动一律拒绝。所有摘要比较均使用恒定时间比较。

## 用法

```go
import "example.org/netsec/q0177/radius"

secret := []byte("shared-secret")
auth, err := radius.NewRandomRequestAuthenticator()
// 构造请求，Message-Authenticator 自动置于首位，Marshal 时回填 HMAC。
req, err := radius.NewAccessRequest(identifier, auth, secret, []radius.Attribute{
    {Type: radius.TypeUserName, Value: []byte("alice@example.com")},
    {Type: radius.TypeProxyState, Value: []byte("tracker-1")},
})
wire, err := req.Marshal(secret)

// 收到应答后解析并校验；任一失败返回可通过 errors.Is 判定的哨兵错误。
resp, err := radius.Parse(incoming)
if err := radius.VerifyResponse(req, resp, secret); err != nil {
    // 拒绝该应答，例如 ErrProxyStateMismatch / ErrMessageAuthenticatorMismatch
}
```

## 测试

`go test ./...` 覆盖题目要求的四类场景及边界：

- 无 Message-Authenticator 的应答 → `ErrMessageAuthenticatorMissing`；
- 应答附加 Proxy-State / 改动 Proxy-State / 丢失 Proxy-State → `ErrProxyStateMismatch`；
- 改动 Identifier → `ErrIdentifierMismatch`；
- 合法 Access-Accept/Reject/Challenge 全链路通过；
- 另含 MA 位置错误、重复、长度非法、MA 与 Response Authenticator 篡改、
  错误共享密钥、报文结构错误等用例。
