# Web Push 订阅与主题绑定（netsec）

针对两类攻击面的可导入 Go 组件：

- 攻击者提交指向内网的订阅端点（SSRF）；
- 攻击者借同名 Topic 覆盖他人待投递通知。

仅依赖 Go 标准库，模块路径 `example.org/netsec/q0053`，包名 `netsec`。

## 组件

| 文件 | 职责 |
| --- | --- |
| `guard.go` | 端点安全校验与安全拨号器：强制 https、主机白名单、解析 IP 复核、保留地址拒绝 |
| `vapid.go` | VAPID 公钥解析（原始点/SPKI/base64url）、SHA-256 指纹、ES256 JWT 与 Authorization 头 |
| `subscription.go` | 按租户隔离的内存订阅注册表，订阅绑定租户与 VAPID 指纹 |
| `topic.go` | Topic 校验（1–32 位 base64url）与按 `(订阅, Topic)` 隔离的待投递通知替换存储 |
| `encryption.go` | RFC 8291 `aes128gcm` 载荷加密（P-256 ECDH + HKDF-SHA256 + AES-128-GCM） |
| `sender.go` | 安全发送器：手动逐跳重定向，每跳重过 Guard 校验并按目标源重签 VAPID |

## 安全模型

### 端点 SSRF 防护（`Guard.ValidateEndpoint` / `Guard.DialContext`）

1. 必须是绝对 URL 且方案为 `https`，禁止携带 userinfo；
2. 主机必须是域名（拒绝 IP 字面量），且命中白名单；`*.example.com` 仅匹配一级子域；
3. 默认仅允许 443 端口（`AllowedPorts` 可调整）；
4. 解析出的所有 IP 都必须通过 `IsPublicIP`，环回、私网、链路本地、ULA、
   组播、TEST-NET、CGNAT、NAT64 等保留地址一律拒绝，多 A 记录中混入任一保留地址即整体拒绝；
5. `DialContext` 在每次实际建连前重新解析并复核 IP，抵御校验后的 DNS 重绑定，
   且拨号目标使用复核通过的 IP 而非原始主机名；
6. 发送器重定向不使用 `http.Client` 自动跟随，而是逐跳重新执行 1–5 的完整校验，
   内网重定向、非白名单重定向、https→http 降级都会在发出请求前被拒绝。

### 租户与 VAPID 绑定（`Registry`）

- 创建订阅必须提供租户 ID 与非零 VAPID 公钥 SHA-256 指纹；
- 读取/删除均校验租户归属，跨租户访问统一返回 `ErrUnknownSubscription`，不泄漏订阅是否存在；
- 订阅保存的是校验后端点的规范化拷贝，避免外部修改。

### Topic 替换隔离（`TopicStore`）

- `ValidateTopic` 只接受 `[A-Za-z0-9_-]`、长度 1–32 的无填充 base64url 字符串；
- 存储键为 `(subscriptionID, topic)`，同名 Topic 在不同订阅、不同租户间天然隔离；
- 所有操作经 `Registry.CheckTenant` 校验订阅归属，无法凭猜测的订阅 ID 操作他人 Topic；
- `Replace` 返回被替换的旧待投递通知，首次写入返回 `nil`，调用方可区分“替换”与“新建”。

### VAPID 与加密

- VAPID JWT（RFC 8292）：ES256 签名，`aud` 按每次请求的目标源生成，
   重定向换源时重新签发，避免授权令牌发往不可信主机；
- 载荷加密（RFC 8291）：每次生成临时 P-256 密钥与随机 salt，
   输出自描述 `aes128gcm` 头部 + 密文，测试中以用户代理私钥完整解密回验。

## 用法示例

```go
patterns, _ := netsec.ParseHostPattern("push.example.com")
guard := &netsec.Guard{
    Whitelist: []netsec.HostPattern{patterns},
    // Resolver 可选，默认 net.DefaultResolver；测试可注入静态解析器。
}

vapid, _ := netsec.NewVAPIDCredentials("mailto:ops@example.com")
fingerprint, _ := netsec.VAPIDFingerprint(&vapid.PrivateKey.PublicKey)

registry := netsec.NewRegistry(guard)
sub, err := registry.Create(ctx, netsec.CreateSubscriptionInput{
    TenantID:         "tenant-42",
    RawEndpoint:      "https://push.example.com/push/abc",
    VAPIDFingerprint: fingerprint,
    Keys:             uaKeys, // RFC 8291 的 p256dh + auth
})

sender := &netsec.Sender{Guard: guard, VAPID: vapid, Registry: registry, Topics: netsec.NewTopicStore()}
result, err := sender.Send(ctx, netsec.SendInput{
    TenantID: "tenant-42", SubscriptionID: sub.ID,
    Topic: "daily-digest", Payload: []byte("hello"), Urgency: netsec.UrgencyNormal,
})
```

## 测试

测试不依赖真实网络（沙箱禁止回环绑定）：DNS 由 `fakeResolver` 静态注入，
HTTP 由实现 `http.RoundTripper` 的进程内假传输层承载，并在每跳前调用
`Guard.CheckDialPolicy` 复现拨号策略。

重点用例：

- `TestSendRejectsIntranetRedirect`：重定向到解析为 `10.0.0.8` 的主机，返回 `ErrReservedIP`，内网目标绝不拨号；
- `TestSendRejectsNonWhitelistedRedirect` / `TestSendRejectsHTTPDowngradeRedirect`：非白名单主机与 http 降级拒绝；
- `TestTopicReplaceOnlyWithinSameSubscription`：跨租户同名 Topic 互不覆盖，同订阅同 Topic 才替换；
- `TestValidateTopic` / `TestSendRejectsOverlongTopic`：超长（33 位）与非 base64url 字符拒绝；
- `TestEncryptPayloadRoundTrip`：以用户代理私钥按 RFC 8291 完整解密，验证加密正确性；
- `TestVAPIDAuthorizationJWT`：校验 JWT 头、声明并用公钥验证 ES256 签名。

运行：

```sh
go test ./... -count=1
go test -race ./... -count=1
```
